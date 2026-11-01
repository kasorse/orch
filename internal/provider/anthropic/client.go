// Package anthropic is the claude provider over the Messages API instead of the claude CLI
// (providers.claude.transport: api, specs/api-transport/SPEC.md §4.1). It implements
// provider.Client; the wire is unexported.
//
// Every request streams, carries no anthropic-beta header (a gateway to Bedrock may refuse
// one) and no sampling parameter, and leaves thinking to the model's default. This task's
// client runs calls without tools only: Supports is true for AuthorityNone alone, so
// config.ValidateAuthorities refuses every other route until the tool loop exists.
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

// Client is the claude provider over HTTP. Until SetAuth it refuses every call.
type Client struct {
	mu     sync.RWMutex
	set    bool
	ep     httpapi.Endpoint
	prices map[string]httpapi.Price
}

func New() *Client { return &Client{} }

// SetAuth fixes the key, the endpoint (baseURL "" is DefaultBaseURL) and the operator's prices
// (providers.claude.prices) of every later call. The key is sent only as x-api-key and
// redacted from every error.
func (c *Client) SetAuth(key, baseURL string, prices map[string]httpapi.Price) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set, c.prices = true, prices
	c.ep = httpapi.Endpoint{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Header:  http.Header{"X-Api-Key": {key}, "Anthropic-Version": {apiVersion}},
		Key:     key,
	}
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

// Supports is true for AuthorityNone only (SPEC §10, phase 1).
func (c *Client) Supports(a domain.Authority) bool { return a == domain.AuthorityNone }

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
			Reason:    "The claude API transport runs calls without tools only so far (specs/api-transport/SPEC.md §10); route this action to transport: cli.",
		}
	}
	ep, prices, err := c.endpoint()
	if err != nil {
		return provider.Result{}, err
	}
	body, err := json.Marshal(buildRequest(req))
	if err != nil {
		return provider.Result{}, fmt.Errorf("claude API request: %w", err)
	}
	call := callError{req: req, endpoint: "POST " + ep.BaseURL + messagesPath}

	start := time.Now()
	resp, err := ep.Do(ctx, http.MethodPost, messagesPath, body)
	if err != nil {
		return provider.Result{Duration: time.Since(start)}, call.fromHTTP(err, time.Since(start))
	}
	defer resp.Body.Close()

	t := &turn{}
	obs := observer{
		onBlock: func(typ string) {
			if req.Activity == nil {
				return
			}
			switch typ {
			case "thinking", "redacted_thinking":
				req.Activity("Thinking")
			case "text":
				req.Activity("Writing")
			}
		},
	}
	if req.Stream != nil {
		obs.onText = func(s string) { _, _ = io.WriteString(req.Stream, s) }
	}
	streamErr := readStream(resp.Body, t, obs)
	dur := time.Since(start)
	if req.Stream != nil && t.text() != "" && !strings.HasSuffix(t.text(), "\n") {
		_, _ = io.WriteString(req.Stream, "\n")
	}

	usage := t.domainUsage()
	usage.Turns = 1
	cost, priced := httpapi.GatewayCost(resp.Header)
	if !priced {
		if p, ok := httpapi.Lookup(prices, req.Model); ok {
			cost = p.Cost(usage)
		}
	}
	usage.CostUSD = cost
	model := t.model
	if model == "" {
		model = req.Model
	}
	result := provider.Result{
		Output:   t.text(),
		Duration: dur,
		Usage:    &usage,
		Call:     &domain.CallReport{Models: []domain.ModelUse{{Model: model, CostUSD: cost}}, APIMS: dur.Milliseconds()},
	}

	if e := t.streamErr; e != nil && streamErr == nil {
		usage.Partial = true
		return result, call.failure(apperr.FailureAPI, ep.Redact(fmt.Sprintf("the API sent an error in the stream: %s: %s", e.Type, e.Message)), dur, nil)
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
		return result, call.failure(httpapi.Kind(streamErr), ep.Redact(streamErr.Error()), dur, streamErr)
	}
	switch t.stopReason {
	case "end_turn", "stop_sequence":
		return result, nil
	case "max_tokens":
		return result, call.failure(apperr.FailureAPI, fmt.Sprintf("the answer was cut at max_tokens (%d)", maxTokens), dur, nil)
	case "refusal":
		detail := "the model refused the request"
		if d := t.stopDetails; d != nil && d.Category != "" {
			detail += " (category " + d.Category + ")"
		}
		return result, call.failure(apperr.FailureAPI, detail, dur, nil)
	default:
		return result, call.failure(apperr.FailureAPI, fmt.Sprintf("unexpected stop_reason %q for a call without tools", t.stopReason), dur, nil)
	}
}

func buildRequest(req provider.Request) request {
	r := request{
		Model:     req.Model,
		MaxTokens: maxTokens,
		System:    []textBlock{{Type: "text", Text: systemNone}},
		Messages:  []message{{Role: "user", Content: []any{textBlock{Type: "text", Text: req.Prompt}}}},
		Stream:    true,
	}
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
