// Package openai is the codex provider over the Responses API instead of `codex exec`
// (providers.codex.transport: api, specs/api-transport/SPEC.md §4.2). It implements
// provider.Client; the wire is unexported.
//
// Every request streams with store: false, so OpenAI keeps nothing of the conversation, and
// uses nothing a LiteLLM gateway does not proxy on /v1/responses (no previous_response_id,
// conversations or background mode). This task's client runs calls without tools only:
// Supports is true for AuthorityNone alone.
package openai

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
	// DefaultBaseURL is the first-party endpoint, used when providers.codex.base_url is empty.
	DefaultBaseURL = "https://api.openai.com"
	responsesPath  = "/v1/responses"
)

// instructionsNone is the fixed instructions of a call without tools; frozen, since it opens
// the prompt's prefix.
const instructionsNone = "You answer one request from an automated orchestrator. You have no tools: " +
	"answer from the request alone, exactly in the form the request asks for."

// Client is the codex provider over HTTP. Until SetAuth it refuses every call.
type Client struct {
	mu     sync.RWMutex
	set    bool
	ep     httpapi.Endpoint
	prices map[string]httpapi.Price
}

func New() *Client { return &Client{} }

// SetAuth fixes the key, the endpoint (baseURL "" is DefaultBaseURL) and the operator's prices
// (providers.codex.prices) of every later call. The key is sent only as a bearer token and
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
		Header:  http.Header{"Authorization": {"Bearer " + key}},
		Key:     key,
	}
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

// Supports is true for AuthorityNone only (SPEC §10, phase 1).
func (c *Client) Supports(a domain.Authority) bool { return a == domain.AuthorityNone }

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
			Reason:    "The codex API transport runs calls without tools only so far (specs/api-transport/SPEC.md §10); route this action to transport: cli.",
		}
	}
	ep, prices, err := c.endpoint()
	if err != nil {
		return provider.Result{}, err
	}
	body, err := json.Marshal(buildRequest(req))
	if err != nil {
		return provider.Result{}, fmt.Errorf("codex API request: %w", err)
	}
	call := callError{req: req, endpoint: "POST " + ep.BaseURL + responsesPath}

	start := time.Now()
	resp, err := ep.Do(ctx, http.MethodPost, responsesPath, body)
	if err != nil {
		return provider.Result{Duration: time.Since(start)}, call.fromHTTP(err, time.Since(start))
	}
	defer resp.Body.Close()

	s := &stream{}
	var onText func(string)
	if req.Stream != nil || req.Activity != nil {
		writing := false
		onText = func(t string) {
			if !writing && req.Activity != nil {
				req.Activity("Writing")
			}
			writing = true
			if req.Stream != nil {
				_, _ = io.WriteString(req.Stream, t)
			}
		}
	}
	streamErr := readStream(resp.Body, s, onText)
	dur := time.Since(start)

	result := provider.Result{Duration: dur}
	model := req.Model
	if s.created != nil && s.created.Model != "" {
		model = s.created.Model
	}
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
			result.Usage = &u
		}
		out, err := text(f.Output)
		if err != nil && streamErr == nil {
			streamErr = fmt.Errorf("the response is not valid: %w", err)
		}
		result.Output = out
	}
	var cost float64
	if result.Usage != nil {
		cost = result.Usage.CostUSD
	}
	result.Call = &domain.CallReport{Models: []domain.ModelUse{{Model: model, CostUSD: cost}}, APIMS: dur.Milliseconds()}
	if req.Stream != nil && result.Output != "" && !strings.HasSuffix(result.Output, "\n") {
		_, _ = io.WriteString(req.Stream, "\n")
	}

	switch {
	case streamErr != nil:
		if ctx.Err() != nil {
			streamErr = ctx.Err()
		}
		// Responses reports usage only at the end: a call cut off has none to give.
		return result, call.failure(httpapi.Kind(streamErr), ep.Redact(streamErr.Error()), dur, streamErr)
	case s.errEvent != nil && s.final == nil:
		return result, call.failure(apperr.FailureAPI, ep.Redact(fmt.Sprintf("the API sent an error in the stream: %s: %s", s.errEvent.Code, s.errEvent.Message)), dur, nil)
	case s.final == nil:
		return result, call.failure(apperr.FailureAPI, "the stream ended before the response completed", dur, nil)
	}
	switch f := s.final; f.Status {
	case "completed":
		return result, nil
	case "incomplete":
		reason := "unknown"
		if f.IncompleteDetails != nil && f.IncompleteDetails.Reason != "" {
			reason = f.IncompleteDetails.Reason
		}
		return result, call.failure(apperr.FailureAPI, "the response is incomplete: "+reason, dur, nil)
	case "failed":
		detail := "the response failed"
		if e := f.Error; e != nil {
			detail = ep.Redact(fmt.Sprintf("the response failed: %s: %s", e.Code, e.Message))
		}
		kind := apperr.FailureAPI
		if e := f.Error; e != nil && e.Code == "rate_limit_exceeded" {
			kind = apperr.FailureRateLimit
		}
		return result, call.failure(kind, detail, dur, nil)
	default:
		return result, call.failure(apperr.FailureAPI, fmt.Sprintf("unexpected response status %q", f.Status), dur, nil)
	}
}

func buildRequest(req provider.Request) request {
	r := request{
		Model:        req.Model,
		Instructions: instructionsNone,
		Input:        []any{inputMessage{Role: "user", Content: []inputText{{Type: "input_text", Text: req.Prompt}}}},
		Stream:       true,
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
