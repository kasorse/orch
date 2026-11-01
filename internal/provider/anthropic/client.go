// Package anthropic is the claude provider over the Messages API instead of the claude CLI
// (providers.claude.transport: api, specs/api-transport/SPEC.md §4.1). It implements
// provider.Client; the wire is unexported.
//
// Every request streams, carries no anthropic-beta header (a gateway to Bedrock may refuse
// one) and no sampling parameter, and leaves thinking to the model's default. A call with
// repository tools is a loop the client runs itself (SPEC §5): the model asks for a tool, the
// client runs it from internal/provider/tools inside the working directory and sends the result
// back. Supports is true for none, web and read; edit waits for its tools (AT10).
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
	"orchestrator/internal/provider/tools"
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

// systemRead is the fixed system text of a call with the read tools.
const systemRead = "You work for an automated orchestrator on one repository, through your tools read_file, " +
	"grep and glob; every path is relative to the repository root. You cannot run commands, change files " +
	"or reach the network. Answer exactly in the form the request asks for."

// maxTurns is the backstop of SPEC §5: execution.timeout bounds a call, this bounds its responses.
const maxTurns = 500

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

// Supports is true for none, web and read (SPEC §5.1): none and web get no repository tool, web
// only the server's web tools or none (§6), read the confined read tools. Edit waits for its tools.
func (c *Client) Supports(a domain.Authority) bool {
	return a == domain.AuthorityNone || a == domain.AuthorityWeb || a == domain.AuthorityRead
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
			Reason:    "The claude API transport has no edit tools yet (specs/api-transport/SPEC.md §10); route this action to transport: cli.",
		}
	}
	ep, prices, err := c.endpoint()
	if err != nil {
		return provider.Result{}, err
	}
	r := buildRequest(req, c.webTools)
	call := callError{req: req, endpoint: "POST " + ep.BaseURL + messagesPath}
	obs := observe(req)
	toolset, err := repositoryTools(req, &r)
	if err != nil {
		return provider.Result{}, call.failure(apperr.FailureAPI, err.Error(), 0, err)
	}
	// Until the adapter speaks MCP (AT11), a configured server is reported as one that did not
	// start: the call runs without its tools, which the call report says.
	mcpFailed := tools.MCPServerNames(req.Extensions.MCPConfig)

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
		rep.MCPFailed = mcpFailed
		return provider.Result{Output: strings.Join(texts, ""), Duration: time.Since(start), Usage: &u, Call: rep}
	}

	pauses := 0
	for n := 0; ; n++ {
		if n == maxTurns {
			return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("the call reached %d model turns", maxTurns), time.Since(start), nil)
		}
		body, err := marshal(r, toolset != nil)
		if err != nil {
			return provider.Result{}, fmt.Errorf("claude API request: %w", err)
		}
		resp, err := ep.Do(ctx, http.MethodPost, messagesPath, body)
		if err != nil {
			res := result()
			if n == 0 {
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
			pauses++
			r.Messages = append(r.Messages, message{Role: "assistant", Content: blocksOf(t.blocks)})
			continue
		case "tool_use":
			if toolset == nil {
				return result(), call.failure(apperr.FailureAPI, "the model asked for a tool the call did not offer", time.Since(start), nil)
			}
			results := runTools(ctx, t.blocks, toolset, req, tools)
			if ctx.Err() != nil {
				usage.Partial = true
				return result(), call.failure(httpapi.Kind(ctx.Err()), ctx.Err().Error(), time.Since(start), ctx.Err())
			}
			if len(results) == 0 {
				return result(), call.failure(apperr.FailureAPI, "stop_reason tool_use without a tool_use block", time.Since(start), nil)
			}
			pauses = 0
			r.Messages = append(r.Messages, message{Role: "assistant", Content: blocksOf(t.blocks)}, message{Role: "user", Content: results})
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
			return result(), call.failure(apperr.FailureAPI, fmt.Sprintf("unexpected stop_reason %q", t.stopReason), time.Since(start), nil)
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

// toolName is the name a call report and the console use for a tool, as the claude CLI names
// its own (WebSearch, WebFetch, Read, Grep, Glob).
func toolName(name string) string {
	switch name {
	case "web_search":
		return "WebSearch"
	case "web_fetch":
		return "WebFetch"
	}
	return tools.DisplayName(name)
}

// repositoryTools gives a read call the confined read tools (SPEC §5.1): their definitions go
// into the request, sorted by name, and the set runs the model's calls. Other calls get none.
func repositoryTools(req provider.Request, r *request) (tools.Set, error) {
	if req.Authority != domain.AuthorityRead {
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
		r.Tools = append(r.Tools, clientTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	r.System = []textBlock{{Type: "text", Text: systemRead}}
	return tools.NewSet(read), nil
}

// runTools runs the tool_use blocks of one response in order and returns their results, every
// call answered, a refused or unknown one as is_error (SPEC §4.1). counts gets each call by its
// CLI name; Activity and Stream are told what runs.
func runTools(ctx context.Context, blocks []map[string]any, set tools.Set, req provider.Request, counts map[string]int) []any {
	var results []any
	for _, b := range blocks {
		if b["type"] != "tool_use" {
			continue
		}
		name := str(b["name"])
		input, _ := json.Marshal(b["input"])
		what := tools.Label(name, input)
		if req.Activity != nil {
			req.Activity(what)
		}
		if req.Stream != nil {
			_, _ = io.WriteString(req.Stream, "→ "+what+"\n")
		}
		counts[tools.DisplayName(name)]++
		out, isErr := set.Call(ctx, name, input)
		results = append(results, &toolResult{Type: "tool_result", ToolUseID: str(b["id"]), Content: out, IsError: isErr})
	}
	return results
}

// marshal renders the request. With cache, the last block of the first user message, of the
// previous user message and of the last one carry an ephemeral cache_control for this request
// only (SPEC §4.1): the cache looks back 20 blocks, and one turn of parallel tool results can
// pass that. The marks are removed again, so the history never holds more than three.
func marshal(r request, cache bool) ([]byte, error) {
	if !cache {
		return json.Marshal(r)
	}
	var marked []cacheable
	users := 0
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role != "user" || len(m.Content) == 0 {
			continue
		}
		if users < 2 || i == 0 {
			if c, ok := m.Content[len(m.Content)-1].(cacheable); ok {
				c.mark(true)
				marked = append(marked, c)
			}
		}
		users++
	}
	defer func() {
		for _, c := range marked {
			c.mark(false)
		}
	}()
	return json.Marshal(r)
}

func blocksOf(blocks []map[string]any) []any {
	out := make([]any, len(blocks))
	for i, b := range blocks {
		out[i] = b
	}
	return out
}

// offeredWebTools names the web tools a request offered, for call.web_tools (SPEC §6).
func offeredWebTools(tools []any) []string {
	var names []string
	for _, t := range tools {
		if st, ok := t.(serverTool); ok {
			names = append(names, toolName(st.Name))
		}
	}
	return names
}

func buildRequest(req provider.Request, webTools bool) request {
	system := systemNone
	r := request{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Messages:  []message{{Role: "user", Content: []any{&userText{Type: "text", Text: req.Prompt}}}},
		Stream:    true,
	}
	if req.Authority == domain.AuthorityWeb {
		system = systemWebOff
		if webTools {
			system = systemWeb
			r.Tools = []any{
				serverTool{Type: "web_fetch_20260209", Name: "web_fetch", MaxUses: webToolUses},
				serverTool{Type: "web_search_20260209", Name: "web_search", MaxUses: webToolUses},
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
