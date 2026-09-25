// Package jev is the Jev decision provider (specs/JEV integration/TASKS.md T06/T07).
// Jev's wire format stays inside this package: the transport types below are
// unexported, and Client's methods are too, so nothing outside can build a request or
// read a raw answer - the compiler enforces the isolation SPEC.md AC13 asks for.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/apperr"
)

// ProviderName identifies Jev in errors, traces, and TaskClassification.Provider.
const ProviderName = "jev"

const (
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"

	// maxErrorBody bounds the response body excerpt carried by a DecisionError.
	maxErrorBody = 2 << 10
	// maxResponseBody bounds how much of any response is read at all. A classification
	// answer is a few KB; anything near this is not a Jev answer.
	maxResponseBody = 4 << 20
	// baseBackoff is the wait before the first retry when the service sent no
	// Retry-After; it doubles for each further retry.
	baseBackoff = 200 * time.Millisecond

	redacted = "[REDACTED]"
)

// Question types (TASKS.md §2).
const (
	questionChoice = "choice"
	questionScore  = "score"
	questionNoul   = "noul"
)

// systemOneRequest is the body of POST /v1/systemone. Every question is evaluated
// independently against the same State, so one request carries all of them.
type systemOneRequest struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]question `json:"questions"`
}

// question is one Jev question. Criteria's shape depends on Type: a map of option ->
// description for choice, an ordered array of 2-10 levels for score, and an optional
// {"true": ..., "false": ...} object for noul.
type question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type systemOneResponse struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Usage   usage             `json:"usage"`
}

// answer is the union of the three answer shapes; Type says which fields are set.
// Pointers tell a missing field apart from a zero, which the mapper (T07) must reject.
//   - choice: Choice, Probabilities (option -> p), Confidence
//   - score:  Score, Legend (level index -> level text), Probabilities (level index -> p), Confidence
//   - noul:   Noul (no confidence)
type answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type modelsResponse struct {
	Models []modelInfo `json:"models"`
}

type modelInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

// Client is the transport to Jev: the real HTTP client (NewHTTPClient) or the offline
// mock (NewMockClient). Its methods are unexported on purpose - see the package doc.
type Client interface {
	decide(ctx context.Context, req systemOneRequest) (systemOneResponse, error)
	listModels(ctx context.Context) ([]modelInfo, error)
}

// Options configures the HTTP client. It mirrors config.JevConfig's transport fields;
// the API key is passed separately so it never sits in a value that could be logged.
type Options struct {
	BaseURL string
	// Timeout bounds one HTTP attempt. Retries get a fresh Timeout each, and a
	// Retry-After longer than Timeout is not waited for.
	Timeout time.Duration
	// MaxRetries is how many times a transient failure is retried (0 = no retry).
	MaxRetries int
}

type httpClient struct {
	baseURL    string
	apiKey     string
	timeout    time.Duration
	maxRetries int
	http       *http.Client
	// sleep waits between retries; tests replace it to avoid real delays.
	sleep func(ctx context.Context, d time.Duration) error
}

// NewHTTPClient builds the real Jev client. apiKey is read from the environment by the
// caller (app) and is only ever sent as the Authorization header: it is redacted from
// every error this client returns.
func NewHTTPClient(opts Options, apiKey string) Client {
	return newHTTPClient(opts, apiKey)
}

func newHTTPClient(opts Options, apiKey string) *httpClient {
	return &httpClient{
		baseURL:    strings.TrimRight(opts.BaseURL, "/"),
		apiKey:     apiKey,
		timeout:    opts.Timeout,
		maxRetries: opts.MaxRetries,
		http:       &http.Client{},
		sleep:      sleepCtx,
	}
}

func (c *httpClient) decide(ctx context.Context, req systemOneRequest) (systemOneResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return systemOneResponse{}, &apperr.DecisionError{
			Provider: ProviderName, Kind: apperr.DecisionBadRequest,
			Err: fmt.Errorf("encode request: %w", err),
		}
	}
	var resp systemOneResponse
	if err := c.do(ctx, http.MethodPost, systemOnePath, body, &resp); err != nil {
		return systemOneResponse{}, err
	}
	return resp, nil
}

func (c *httpClient) listModels(ctx context.Context) ([]modelInfo, error) {
	var resp modelsResponse
	if err := c.do(ctx, http.MethodGet, modelsPath, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Models, nil
}

// do runs the request, retrying transient failures up to maxRetries times. The wait
// before a retry is the service's Retry-After when it sent one, else an exponential
// backoff capped at one request's timeout. A Retry-After longer than that timeout is
// not waited for: the failure is returned instead. Cancellation and an expired parent deadline are never retried.
func (c *httpClient) do(ctx context.Context, method, path string, body []byte, out any) error {
	for attempt := 1; ; attempt++ {
		retryAfter, derr := c.attempt(ctx, method, path, body, out)
		if derr == nil {
			return nil
		}
		derr.Attempts = attempt
		if !derr.Kind.Transient() || attempt > c.maxRetries || ctx.Err() != nil {
			return derr
		}
		wait := min(baseBackoff<<(attempt-1), c.timeout)
		if retryAfter > 0 {
			if retryAfter > c.timeout {
				return derr
			}
			wait = retryAfter
		}
		if err := c.sleep(ctx, wait); err != nil {
			return c.contextError(ctx, attempt)
		}
	}
}

// attempt performs one HTTP round trip. retryAfter is the parsed Retry-After header of
// a failed response, or zero.
func (c *httpClient) attempt(ctx context.Context, method, path string, body []byte, out any) (time.Duration, *apperr.DecisionError) {
	actx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(actx, method, c.baseURL+path, reqBody)
	if err != nil {
		return 0, c.fail(apperr.DecisionBadRequest, 0, "", fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, c.transportError(ctx, actx, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return 0, c.transportError(ctx, actx, fmt.Errorf("read response: %w", err))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			return 0, c.fail(apperr.DecisionInvalidResponse, resp.StatusCode, string(raw), fmt.Errorf("decode response: %w", err))
		}
		return 0, nil
	}

	// The status code is rendered by DecisionError itself; the text adds a cause only
	// for statuses Go knows a name for (529 "overloaded" has none).
	var cause error
	if text := http.StatusText(resp.StatusCode); text != "" {
		cause = errors.New(text)
	}
	return parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		c.fail(statusKind(resp.StatusCode), resp.StatusCode, string(raw), cause)
}

func statusKind(status int) apperr.DecisionFailureKind {
	switch {
	case status == http.StatusRequestTimeout:
		return apperr.DecisionTimeout
	case status == http.StatusTooManyRequests:
		return apperr.DecisionRateLimited
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return apperr.DecisionAuth
	case status >= 400 && status < 500:
		return apperr.DecisionBadRequest
	case status >= 500: // includes 529 "overloaded"
		return apperr.DecisionUnavailable
	default: // 1xx/3xx never carry an answer
		return apperr.DecisionInvalidResponse
	}
}

// transportError classifies a failure that produced no HTTP response. ctx is the
// caller's context, actx the per-attempt one derived from it.
func (c *httpClient) transportError(ctx, actx context.Context, err error) *apperr.DecisionError {
	if ctx.Err() != nil {
		return c.contextError(ctx, 0)
	}
	var netErr net.Error
	if errors.Is(actx.Err(), context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return c.fail(apperr.DecisionTimeout, 0, "", fmt.Errorf("no response within %s: %w", c.timeout, err))
	}
	return c.fail(apperr.DecisionConnection, 0, "", err)
}

// contextError reports the caller's own context ending: Ctrl-C is a cancellation, an
// expired run deadline a (non-retried) timeout.
func (c *httpClient) contextError(ctx context.Context, attempts int) *apperr.DecisionError {
	kind := apperr.DecisionTimeout
	if errors.Is(ctx.Err(), context.Canceled) {
		kind = apperr.DecisionCancelled
	}
	derr := c.fail(kind, 0, "", ctx.Err())
	derr.Attempts = attempts
	return derr
}

// fail builds a DecisionError with the key scrubbed from both the cause and the body
// excerpt: neither should ever contain it, but a proxy or server echoing request
// headers back must not be able to leak it into logs or artifacts.
func (c *httpClient) fail(kind apperr.DecisionFailureKind, status int, body string, err error) *apperr.DecisionError {
	if err != nil && c.apiKey != "" && strings.Contains(err.Error(), c.apiKey) {
		err = errors.New(c.redact(err.Error()))
	}
	return &apperr.DecisionError{
		Provider:   ProviderName,
		Kind:       kind,
		StatusCode: status,
		Body:       truncate(c.redact(body), maxErrorBody),
		Err:        err,
	}
}

func (c *httpClient) redact(s string) string {
	if c.apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.apiKey, redacted)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + fmt.Sprintf("... [truncated, %d of %d bytes shown]", n, len(s))
}

// parseRetryAfter accepts both forms RFC 9110 allows: delay-seconds and an HTTP-date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
