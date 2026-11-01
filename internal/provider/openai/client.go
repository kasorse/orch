// Package openai is the codex provider over the Responses API instead of `codex exec`
// (providers.codex.transport: api, specs/api-transport/SPEC.md §4.2). It implements
// provider.Client; the wire is unexported.
//
// Every request streams with store: false, so OpenAI keeps nothing of the conversation, and
// uses nothing a LiteLLM gateway does not proxy on /v1/responses (no previous_response_id,
// conversations or background mode). A call with repository tools is a loop the client runs
// itself (SPEC §5): the model calls a function, the client runs it from internal/provider/tools
// inside the working directory and sends the output back with the response's own items.
package openai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/httpapi"
	"orchestrator/internal/provider/tools"
)

const (
	// DefaultBaseURL is the first-party endpoint, used when providers.codex.base_url is empty.
	DefaultBaseURL = "https://api.openai.com"
	responsesPath  = "/v1/responses"
)

// instructionsNone is the fixed instructions of a call without tools; frozen, since it opens
// the prompt's prefix.
const instructionsNone = "You answer one request from an automated orchestrator. You have no tools: " +
	"answer from the request alone, exactly in the form the request asks for."

// Client is the codex provider over HTTP. Until SetKey it refuses every call.
type Client struct {
	mu       sync.RWMutex
	set      bool
	ep       httpapi.Endpoint
	prices   map[string]httpapi.Price
	webTools bool
}

// New returns the client of the endpoint (baseURL "" is DefaultBaseURL) with the operator's
// prices (providers.codex.prices), so ReportsCost can answer in the fail-fast chain before the
// key is read, and whether web calls get the server's web search (providers.codex.web_tools).
// Until SetKey it refuses every call.
func New(baseURL string, webTools bool, prices map[string]httpapi.Price) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{ep: httpapi.Endpoint{BaseURL: strings.TrimRight(baseURL, "/")}, prices: prices, webTools: webTools}
}

// SetKey fixes the key of every later call. It is sent only as a bearer token and redacted from
// every error.
func (c *Client) SetKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set = true
	c.ep.Header, c.ep.Key = http.Header{"Authorization": {"Bearer " + key}}, key
}

func (c *Client) endpoint() (httpapi.Endpoint, map[string]httpapi.Price, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.set {
		return httpapi.Endpoint{}, nil, apperr.NewConfigError("the codex API client was not given its key before the first call (specs/api-transport/SPEC.md §9)", nil)
	}
	return c.ep, c.prices, nil
}

// Endpoint is the client's endpoint, for the model check (httpapi.CheckModels).
func (c *Client) Endpoint() (httpapi.Endpoint, error) {
	ep, _, err := c.endpoint()
	return ep, err
}

// Supports is true for every authority (SPEC §5.1): none gets no tools, web only the server's
// web search or none (§6), read the confined read tools, edit them with apply_patch.
func (c *Client) Supports(a domain.Authority) bool {
	switch a {
	case domain.AuthorityNone, domain.AuthorityWeb, domain.AuthorityRead, domain.AuthorityEdit:
		return true
	}
	return false
}

// ReportsCost reports whether a call to model gets a cost: a price in providers.codex.prices or
// the built-in table.
func (c *Client) ReportsCost(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := httpapi.Lookup(c.prices, model)
	return ok
}

func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if !c.Supports(req.Authority) {
		return provider.Result{}, &apperr.AuthorityError{
			Provider:  string(provider.Codex),
			Authority: string(req.Authority),
			Reason:    "The request does not carry one of the defined authorities; every call site must set one (specs/permissions/SPEC.md §5).",
		}
	}
	ep, prices, err := c.endpoint()
	if err != nil {
		return provider.Result{}, err
	}
	r := buildRequest(req, c.webTools)
	call := callError{req: req, endpoint: "POST " + ep.BaseURL + responsesPath}
	set, err := repositoryTools(req, &r)
	if err != nil {
		return provider.Result{}, call.failure(apperr.FailureAPI, err.Error(), 0, err)
	}
	// Until the adapter speaks MCP (AT11), a configured server is reported as one that did not
	// start: the call runs without its tools, which the call report says.
	mcpFailed := tools.MCPServerNames(req.Extensions.MCPConfig)

	start := time.Now()
	var usage *domain.Usage
	var texts []string
	model := req.Model
	counts := map[string]int{}
	result := func() provider.Result {
		res := provider.Result{Output: strings.Join(texts, ""), Duration: time.Since(start), Usage: usage}
		var cost float64
		if usage != nil {
			u := *usage
			res.Usage, cost = &u, u.CostUSD
		}
		res.Call = &domain.CallReport{Models: []domain.ModelUse{{Model: model, CostUSD: cost}}, APIMS: time.Since(start).Milliseconds(), MCPFailed: mcpFailed}
		if offersWebSearch(r.Tools) {
			res.Call.WebTools = []string{"WebSearch"}
		}
		if len(counts) > 0 {
			res.Call.Tools = counts
		}
		return res
	}

	for n := 0; ; n++ {
		if n == maxTurns {
			return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("the call reached %d model turns", maxTurns), time.Since(start), nil)
		}
		body, err := json.Marshal(r)
		if err != nil {
			return provider.Result{}, fmt.Errorf("codex API request: %w", err)
		}
		resp, err := ep.Do(ctx, http.MethodPost, responsesPath, body)
		if err != nil {
			return result(), call.fromHTTP(err, time.Since(start))
		}
		s := &stream{onItem: onSearch(req)}
		streamErr := readStream(resp.Body, s, onText(req))
		resp.Body.Close()

		if s.created != nil && s.created.Model != "" {
			model = s.created.Model
		}
		var calls []outputCall
		if f := s.final; f != nil {
			if f.Model != "" {
				model = f.Model
			}
			if f.Usage != nil {
				u := f.Usage.domain()
				u.Turns = 1
				cost, priced := httpapi.GatewayCost(resp.Header)
				if !priced {
					if p, ok := httpapi.Lookup(prices, req.Model); ok {
						cost = p.Cost(u)
					}
				}
				u.CostUSD = cost
				if usage == nil {
					usage = &domain.Usage{}
				}
				usage.Add(u)
			}
			out, fc, searches, err := parseOutput(f.Output)
			if err != nil && streamErr == nil {
				streamErr = fmt.Errorf("the response is not valid: %w", err)
			}
			if searches > 0 {
				counts["WebSearch"] += searches
			}
			if out != "" {
				texts = append(texts, out)
			}
			calls = fc
		}

		switch {
		case streamErr != nil:
			if ctx.Err() != nil {
				streamErr = ctx.Err()
			}
			// Responses reports usage only at the end of a response: the one cut off adds none.
			return result(), call.failure(httpapi.Kind(streamErr), ep.Redact(streamErr.Error()), time.Since(start), streamErr)
		case s.errEvent != nil && s.final == nil:
			return result(), call.failure(apperr.FailureAPI, ep.Redact(fmt.Sprintf("the API sent an error in the stream: %s: %s", s.errEvent.Code, s.errEvent.Message)), time.Since(start), nil)
		case s.final == nil:
			return result(), call.failure(apperr.FailureAPI, "the stream ended before the response completed", time.Since(start), nil)
		}
		switch f := s.final; f.Status {
		case "completed":
		case "incomplete":
			reason := "unknown"
			if f.IncompleteDetails != nil && f.IncompleteDetails.Reason != "" {
				reason = f.IncompleteDetails.Reason
			}
			return result(), call.failure(apperr.FailureAPI, "the response is incomplete: "+reason, time.Since(start), nil)
		case "failed":
			detail := "the response failed"
			if e := f.Error; e != nil {
				detail = ep.Redact(fmt.Sprintf("the response failed: %s: %s", e.Code, e.Message))
			}
			kind := apperr.FailureAPI
			if e := f.Error; e != nil && e.Code == "rate_limit_exceeded" {
				kind = apperr.FailureRateLimit
			}
			return result(), call.failure(kind, detail, time.Since(start), nil)
		default:
			return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("unexpected response status %q", f.Status), time.Since(start), nil)
		}

		if len(calls) == 0 {
			if req.Stream != nil {
				if out := strings.Join(texts, ""); out != "" && !strings.HasSuffix(out, "\n") {
					_, _ = io.WriteString(req.Stream, "\n")
				}
			}
			return result(), nil
		}
		if set == nil {
			return result(), call.failure(apperr.FailureAPI, "the model called a tool the call did not offer", time.Since(start), nil)
		}
		// The response's items go back unchanged (reasoning items carry their encrypted content),
		// then one output per call (SPEC §4.2).
		for _, item := range s.final.Output {
			r.Input = append(r.Input, item)
		}
		for _, oc := range calls {
			display, what := tools.Display(oc.Name, json.RawMessage(oc.Arguments)), tools.Label(oc.Name, json.RawMessage(oc.Arguments))
			if oc.Patch != nil {
				display = tools.PatchDisplay(*oc.Patch)
				what = display + " " + oc.Patch.Path
			}
			if req.Activity != nil {
				req.Activity(what)
			}
			if req.Stream != nil {
				_, _ = io.WriteString(req.Stream, "→ "+what+"\n")
			}
			counts[display]++
			if oc.Patch != nil {
				r.Input = append(r.Input, applyPatch(set, oc))
				continue
			}
			out, isErr := set.Call(ctx, oc.Name, json.RawMessage(oc.Arguments))
			if isErr {
				out = "error: " + out
			}
			r.Input = append(r.Input, functionCallOutput{Type: "function_call_output", CallID: oc.CallID, Output: out})
		}
		if ctx.Err() != nil {
			return result(), call.failure(httpapi.Kind(ctx.Err()), ctx.Err().Error(), time.Since(start), ctx.Err())
		}
	}
}

// maxTurns is the backstop of SPEC §5: execution.timeout bounds a call, this bounds its responses.
const maxTurns = 500

// instructionsEdit is the fixed instructions of a call with the edit tools.
const instructionsEdit = "You work for an automated orchestrator on one repository, through your tools: read_file, " +
	"grep and glob to read, apply_patch to create, change and delete files; every path is relative to the " +
	"repository root. You cannot run commands or reach the network. Answer exactly in the form the request asks for."

// instructionsRead is the fixed instructions of a call with the read tools.
const instructionsRead = "You work for an automated orchestrator on one repository, through your tools read_file, " +
	"grep and glob; every path is relative to the repository root. You cannot run commands, change files " +
	"or reach the network. Answer exactly in the form the request asks for."

// instructionsWeb and instructionsWebOff are the fixed instructions of a web call with and
// without the server's web search (providers.codex.web_tools, SPEC §6).
const (
	instructionsWeb = "You answer one request from an automated orchestrator. You can search the web with " +
		"your tool; you have no access to any repository or file. Answer exactly in the form the request asks for."
	instructionsWebOff = "You answer one request from an automated orchestrator. You have no tools and no web " +
		"access: answer from what you know, say where your knowledge may be out of date, and answer " +
		"exactly in the form the request asks for."
)

// onSearch tells Activity and Stream what a finished web search looked for.
func onSearch(req provider.Request) func(json.RawMessage) {
	if req.Stream == nil && req.Activity == nil {
		return nil
	}
	return func(raw json.RawMessage) {
		var item struct {
			Type   string `json:"type"`
			Action struct {
				Query string `json:"query"`
				URL   string `json:"url"`
			} `json:"action"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "web_search_call" {
			return
		}
		what := strings.TrimSpace("WebSearch " + item.Action.Query + item.Action.URL)
		if req.Activity != nil {
			req.Activity(what)
		}
		if req.Stream != nil {
			_, _ = io.WriteString(req.Stream, "→ "+what+"\n")
		}
	}
}

func offersWebSearch(tools []any) bool {
	for _, t := range tools {
		if _, ok := t.(webSearchTool); ok {
			return true
		}
	}
	return false
}

// onText tells Activity once per response that the model writes, and streams the text.
func onText(req provider.Request) func(string) {
	if req.Stream == nil && req.Activity == nil {
		return nil
	}
	writing := false
	return func(t string) {
		if !writing && req.Activity != nil {
			req.Activity("Writing")
		}
		writing = true
		if req.Stream != nil {
			_, _ = io.WriteString(req.Stream, t)
		}
	}
}

// applyPatch runs one apply_patch operation and renders its output item: completed, or failed
// with a message the model can act on.
func applyPatch(set *tools.Set, oc outputCall) applyPatchOutput {
	out := applyPatchOutput{Type: "apply_patch_call_output", CallID: oc.CallID, Status: "completed"}
	root := set.Root()
	if root == nil {
		out.Status, out.Output = "failed", "apply_patch is not offered to this call"
		return out
	}
	msg, err := root.ApplyPatch(*oc.Patch)
	if err != nil {
		out.Status, msg = "failed", err.Error()
	}
	out.Output = msg
	return out
}

// repositoryTools gives a read call the confined read tools as strict function tools, sorted by
// name, and an edit call apply_patch besides, with the reasoning replay and the cache key a loop
// needs (SPEC §4.2). Other calls get none.
func repositoryTools(req provider.Request, r *request) (*tools.Set, error) {
	if req.Authority != domain.AuthorityRead && req.Authority != domain.AuthorityEdit {
		return nil, nil
	}
	if req.WorkingDir == "" {
		return nil, errors.New("a call with repository tools needs a working directory")
	}
	root, err := tools.NewRoot(req.WorkingDir, req.DenyRead)
	if err != nil {
		return nil, fmt.Errorf("the working directory cannot be used: %w", err)
	}
	read := tools.ReadTools(root)
	for _, t := range read {
		r.Tools = append(r.Tools, functionTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: t.Schema, Strict: true})
	}
	r.Instructions = instructionsRead
	set := tools.NewSet(read)
	if req.Authority == domain.AuthorityEdit {
		r.Tools = append(r.Tools, patchTool{Type: "apply_patch"})
		r.Instructions = instructionsEdit
		set = set.WithRoot(root)
	}
	r.Include = []string{"reasoning.encrypted_content"}
	parallel := true
	r.ParallelToolCalls = &parallel
	sum := sha256.Sum256([]byte("codex\x00" + req.Model + "\x00" + string(req.Authority)))
	r.PromptCacheKey = hex.EncodeToString(sum[:16])
	return set, nil
}

func buildRequest(req provider.Request, webTools bool) request {
	r := request{
		Model:        req.Model,
		Instructions: instructionsNone,
		Input:        []any{inputMessage{Role: "user", Content: []inputText{{Type: "input_text", Text: req.Prompt}}}},
		Stream:       true,
	}
	if req.Authority == domain.AuthorityWeb {
		r.Instructions = instructionsWebOff
		if webTools {
			r.Instructions = instructionsWeb
			r.Tools = []any{webSearchTool{Type: "web_search"}}
		}
	}
	if req.Effort != "" {
		r.Reasoning = &reasoning{Effort: req.Effort}
	}
	return r
}

func readStream(body io.Reader, s *stream, onText func(string)) error {
	sse := httpapi.NewSSE(body)
	for {
		ev, err := sse.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the stream broke: %w", err)
		}
		if string(ev.Data) == "[DONE]" {
			return nil
		}
		if err := s.apply(ev.Data, onText); err != nil {
			return fmt.Errorf("the stream is not valid: %w", err)
		}
		if s.final != nil {
			return nil
		}
	}
}

// callError builds the ProviderError of one call. Action is left blank: the dispatcher fills it.
type callError struct {
	req      provider.Request
	endpoint string
}

func (c callError) failure(kind apperr.FailureKind, detail string, dur time.Duration, err error) *apperr.ProviderError {
	if kind == "" {
		kind = apperr.FailureAPI
	}
	return &apperr.ProviderError{
		Provider:    string(provider.Codex),
		Model:       c.req.Model,
		Kind:        kind,
		Endpoint:    c.endpoint,
		PromptBytes: len(c.req.Prompt),
		WorkingDir:  c.req.WorkingDir,
		Duration:    dur,
		Detail:      detail,
		Err:         err,
	}
}

func (c callError) fromHTTP(err error, dur time.Duration) *apperr.ProviderError {
	pe := c.failure(httpapi.Kind(err), err.Error(), dur, err)
	var se *httpapi.StatusError
	if errors.As(err, &se) {
		pe.APIStatus, pe.ResetsAt = se.Status, se.ResetsAt
	}
	return pe
}
