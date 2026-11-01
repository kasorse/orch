// Package anthropic is the claude provider over the Messages API instead of the claude CLI
// (providers.claude.transport: api, specs/api-transport/SPEC.md §4.1). It implements
// provider.Client; the wire is unexported.
//
// Every request streams, carries no anthropic-beta header (a gateway to Bedrock may refuse
// one) and no sampling parameter, and leaves thinking to the model's default. This task's
// client runs calls without repository tools only: Supports is true for AuthorityNone and
// AuthorityWeb, so config.ValidateAuthorities refuses read and edit routes until the tool loop
// exists.
package anthropic

import (
	"context"
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
)

const (
	// DefaultBaseURL is the first-party endpoint, used when providers.claude.base_url is empty.
	DefaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	messagesPath   = "/v1/messages"
	// maxTokens leaves the model room: the request streams, so a large cap costs nothing until
	// it is used (SPEC §4.1).
	maxTokens = 64000
)

// systemNone is the fixed system text of a call without tools. It is frozen: it opens the
// prompt's prefix, so it never carries anything per call.
const systemNone = "You answer one request from an automated orchestrator. You have no tools: " +
	"answer from the request alone, exactly in the form the request asks for."

// systemWeb and systemWebOff are the fixed system texts of a web call with and without the
// server web tools (providers.claude.web_tools, SPEC §6).
const (
	systemWeb = "You answer one request from an automated orchestrator. You can search the web and " +
		"fetch web pages with your tools; you have no access to any repository or file. Answer " +
		"exactly in the form the request asks for."
	systemWebOff = "You answer one request from an automated orchestrator. You have no tools and no web " +
		"access: answer from what you know, say where your knowledge may be out of date, and answer " +
		"exactly in the form the request asks for."
)

// Client is the claude provider over HTTP. Until SetKey it refuses every call.
type Client struct {
	mu       sync.RWMutex
	set      bool
	ep       httpapi.Endpoint
	prices   map[string]httpapi.Price
	webTools bool
}

// New returns the client of the endpoint (baseURL "" is DefaultBaseURL) with the operator's
// prices (providers.claude.prices), so ReportsCost can answer in the fail-fast chain before the
// key is read, and whether web calls get the server web tools (providers.claude.web_tools).
// Until SetKey it refuses every call.
func New(baseURL string, webTools bool, prices map[string]httpapi.Price) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{ep: httpapi.Endpoint{BaseURL: strings.TrimRight(baseURL, "/")}, prices: prices, webTools: webTools}
}

// SetKey fixes the key of every later call. It is sent only as x-api-key and redacted from
// every error.
func (c *Client) SetKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set = true
	c.ep.Header, c.ep.Key = http.Header{"X-Api-Key": {key}, "Anthropic-Version": {apiVersion}}, key
}

func (c *Client) endpoint() (httpapi.Endpoint, map[string]httpapi.Price, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.set {
		return httpapi.Endpoint{}, nil, apperr.NewConfigError("the claude API client was not given its key before the first call (specs/api-transport/SPEC.md §9)", nil)
	}
	return c.ep, c.prices, nil
}

// Endpoint is the client's endpoint, for the model check (httpapi.CheckModels).
func (c *Client) Endpoint() (httpapi.Endpoint, error) {
	ep, _, err := c.endpoint()
	return ep, err
}

// Supports is true for AuthorityNone and AuthorityWeb: neither gets a repository tool, and web
// gets only the server's web tools, or none (SPEC §5.1, §6). Read and edit wait for the tool loop.
func (c *Client) Supports(a domain.Authority) bool {
	return a == domain.AuthorityNone || a == domain.AuthorityWeb
}

// ReportsCost reports whether a call to model gets a cost: a price in providers.claude.prices
// or the built-in table. A gateway's own figure cannot be known before the first call.
func (c *Client) ReportsCost(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := httpapi.Lookup(c.prices, model)
	return ok
}

func (c *Client) Execute(ctx context.Context, req provider.Request) (provider.Result, error) {
	if !c.Supports(req.Authority) {
		return provider.Result{}, &apperr.AuthorityError{
			Provider:  string(provider.Claude),
			Authority: string(req.Authority),
			Reason:    "The claude API transport runs calls without repository tools only so far (specs/api-transport/SPEC.md §10); route this action to transport: cli.",
		}
	}
	ep, prices, err := c.endpoint()
	if err != nil {
		return provider.Result{}, err
	}
	r := buildRequest(req, c.webTools)
	call := callError{req: req, endpoint: "POST " + ep.BaseURL + messagesPath}
	obs := observe(req)

	start := time.Now()
	var usage domain.Usage
	var texts []string
	var model string
	tools := map[string]int{}
	result := func() provider.Result {
		if model == "" {
			model = req.Model
		}
		u := usage
		rep := &domain.CallReport{Models: []domain.ModelUse{{Model: model, CostUSD: u.CostUSD}}, APIMS: time.Since(start).Milliseconds(), WebTools: offeredWebTools(r.Tools)}
		if len(tools) > 0 {
			rep.Tools = tools
		}
		return provider.Result{Output: strings.Join(texts, ""), Duration: time.Since(start), Usage: &u, Call: rep}
	}

	for pauses := 0; ; pauses++ {
		body, err := json.Marshal(r)
		if err != nil {
			return provider.Result{}, fmt.Errorf("claude API request: %w", err)
		}
		resp, err := ep.Do(ctx, http.MethodPost, messagesPath, body)
		if err != nil {
			res := result()
			if pauses == 0 {
				res.Usage = nil
			}
			return res, call.fromHTTP(err, time.Since(start))
		}
		t := &turn{}
		streamErr := readStream(resp.Body, t, obs)
		resp.Body.Close()

		u := t.domainUsage()
		u.Turns = 1
		cost, priced := httpapi.GatewayCost(resp.Header)
		if !priced {
			if p, ok := httpapi.Lookup(prices, req.Model); ok {
				cost = p.Cost(u)
			}
		}
		u.CostUSD = cost
		usage.Add(u)
		if t.model != "" {
			model = t.model
		}
		if text := t.text(); text != "" {
			texts = append(texts, text)
		}
		for _, b := range t.blocks {
			if b["type"] == "server_tool_use" {
				tools[toolName(str(b["name"]))]++
			}
		}

		if e := t.streamErr; e != nil && streamErr == nil {
			usage.Partial = true
			return result(), call.failure(apperr.FailureAPI, ep.Redact(fmt.Sprintf("the API sent an error in the stream: %s: %s", e.Type, e.Message)), time.Since(start), nil)
		}
		if streamErr == nil && !t.stopped {
			streamErr = errors.New("the stream ended before message_stop")
		}
		if streamErr != nil {
			if ctx.Err() != nil {
				streamErr = ctx.Err()
			}
			// A call cut off reports what it streamed until then: the output tokens are a lower bound.
			usage.Partial = true
			return result(), call.failure(httpapi.Kind(streamErr), ep.Redact(streamErr.Error()), time.Since(start), streamErr)
		}

		switch t.stopReason {
		case "end_turn", "stop_sequence":
			if req.Stream != nil {
				if out := strings.Join(texts, ""); out != "" && !strings.HasSuffix(out, "\n") {
					_, _ = io.WriteString(req.Stream, "\n")
				}
			}
			return result(), nil
		case "pause_turn":
			// The server's own tool loop paused (SPEC §4.1): the conversation goes back as it is,
			// with no added message, and the server resumes it.
			if pauses == maxPauses {
				return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("the server paused the turn %d times in a row", maxPauses+1), time.Since(start), nil)
			}
			r.Messages = append(r.Messages, message{Role: "assistant", Content: blocksOf(t.blocks)})
			continue
		case "max_tokens":
			return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("the answer was cut at max_tokens (%d)", maxTokens), time.Since(start), nil)
		case "refusal":
			detail := "the model refused the request"
			if d := t.stopDetails; d != nil && d.Category != "" {
				detail += " (category " + d.Category + ")"
			}
			return result(), call.failure(apperr.FailureAPI, detail, time.Since(start), nil)
		default:
			return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("unexpected stop_reason %q for a call without repository tools", t.stopReason), time.Since(start), nil)
		}
	}
}

// maxPauses bounds the pause_turn resumptions of one call (SPEC §4.1).
const maxPauses = 4

// webToolUses bounds each server web tool per request.
const webToolUses = 10

// observe is what a call tells its caller live: Thinking and Writing as blocks start, a web
// tool's query or URL once its input is complete, and the text as it streams.
func observe(req provider.Request) observer {
	var obs observer
	if req.Activity != nil {
		obs.onBlock = func(typ string) {
			switch typ {
			case "thinking", "redacted_thinking":
				req.Activity("Thinking")
			case "text":
				req.Activity("Writing")
			}
		}
	}
	if req.Activity != nil || req.Stream != nil {
		obs.onDone = func(b map[string]any) {
			if b["type"] != "server_tool_use" {
				return
			}
			input, _ := b["input"].(map[string]any)
			what := toolName(str(b["name"]))
			if arg := str(input["query"]) + str(input["url"]); arg != "" {
				what += " " + arg
			}
			if req.Activity != nil {
				req.Activity(what)
			}
			if req.Stream != nil {
				_, _ = io.WriteString(req.Stream, "→ "+what+"\n")
			}
		}
	}
	if req.Stream != nil {
		obs.onText = func(s string) { _, _ = io.WriteString(req.Stream, s) }
	}
	return obs
}

// toolName is the name a call report and the console use for a server tool, as the claude CLI
// names its own (WebSearch, WebFetch).
func toolName(name string) string {
	switch name {
	case "web_search":
		return "WebSearch"
	case "web_fetch":
		return "WebFetch"
	}
	return name
}

func blocksOf(blocks []map[string]any) []any {
	out := make([]any, len(blocks))
	for i, b := range blocks {
		out[i] = b
	}
	return out
}

// offeredWebTools names the web tools a request offered, for call.web_tools (SPEC §6).
func offeredWebTools(tools []serverTool) []string {
	var names []string
	for _, t := range tools {
		names = append(names, toolName(t.Name))
	}
	return names
}

func buildRequest(req provider.Request, webTools bool) request {
	system := systemNone
	r := request{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages:  []message{{Role: "user", Content: []any{textBlock{Type: "text", Text: req.Prompt}}}},
		Stream:    true,
	}
	if req.Authority == domain.AuthorityWeb {
		system = systemWebOff
		if webTools {
			system = systemWeb
			r.Tools = []serverTool{
				{Type: "web_fetch_20260209", Name: "web_fetch", MaxUses: webToolUses},
				{Type: "web_search_20260209", Name: "web_search", MaxUses: webToolUses},
			}
		}
	}
	r.System = []textBlock{{Type: "text", Text: system}}
	if req.Effort != "" {
		r.OutputConfig = &outputConfig{Effort: req.Effort}
	}
	return r
}

func readStream(body io.Reader, t *turn, obs observer) error {
	sse := httpapi.NewSSE(body)
	for {
		ev, err := sse.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("the stream broke: %w", err)
		}
		if err := t.apply(ev.Name, ev.Data, obs); err != nil {
			return fmt.Errorf("the stream is not valid: %w", err)
		}
		if t.stopped {
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
		Provider:    string(provider.Claude),
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
