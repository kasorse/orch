// Package httpapi is the provider-neutral part of the API transport (specs/api-transport/
// SPEC.md §3, §8): one HTTP round trip with its retries, the SSE reader, the redaction of the
// key, the cost of a response and the model check. The wire of each provider lives in its own
// package (provider/anthropic, provider/openai); this one knows no request or answer shape
// beyond the common error envelope and GET /v1/models.
//
// It imports only the standard library, domain and apperr.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orchestrator/internal/apperr"
)

const (
	// MaxRetries is how often a transient failure is retried (SPEC §8).
	MaxRetries = 3
	// maxErrorBody bounds what is read of an error answer.
	maxErrorBody = 64 << 10
	// maxErrorMessage bounds the message an error carries.
	maxErrorMessage = 2000
	redacted        = "[REDACTED]"
)

// errRedirect is what a redirect becomes: the transport never follows one (SPEC §8).
var errRedirect = errors.New("the endpoint answered with a redirect, which is not followed")

// Endpoint is one provider's API: the base URL (no trailing slash; paths start with /v1), the
// headers every request carries (the credential among them) and the key, so that it can be
// redacted from every error. The zero HTTPClient is one that follows no redirect.
type Endpoint struct {
	BaseURL    string
	Header     http.Header
	Key        string
	HTTPClient *http.Client
	// Backoff is the wait before the first retry without a Retry-After; it doubles for each
	// further retry, with jitter. Zero is one second.
	Backoff time.Duration
}

// StatusError is an HTTP answer outside 2xx. Type, Code and Message come from the common
// error envelope ({"error": {"type", "code", "message"}}) when the body has one; Message is
// redacted and bounded.
type StatusError struct {
	Status  int
	Type    string
	Code    string
	Message string
	// RetryAfter is the answer's Retry-After; ResetsAt is when a rate limit resets, from
	// Retry-After or the providers' reset headers, zero when the answer did not say.
	RetryAfter time.Duration
	ResetsAt   time.Time
}

func (e *StatusError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "HTTP %d", e.Status)
	for _, s := range []string{e.Type, e.Code} {
		if s != "" {
			b.WriteString(" " + s)
		}
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	return b.String()
}

// Kind is the failure kind a provider adapter reports for err: FailureAuth for a refused
// credential, a permission or billing error and OpenAI's insufficient_quota (waiting does not
// refill an account), FailureRateLimit for any other 429, FailureTimeout and FailureCancelled
// for the context, "" for everything else.
func Kind(err error) apperr.FailureKind {
	var se *StatusError
	switch {
	case errors.As(err, &se) && isAuth(se):
		return apperr.FailureAuth
	case errors.As(err, &se) && se.Status == http.StatusTooManyRequests:
		return apperr.FailureRateLimit
	case errors.Is(err, context.DeadlineExceeded):
		return apperr.FailureTimeout
	case errors.Is(err, context.Canceled):
		return apperr.FailureCancelled
	}
	return ""
}

func isAuth(e *StatusError) bool {
	switch {
	case e.Status == http.StatusUnauthorized, e.Status == http.StatusPaymentRequired, e.Status == http.StatusForbidden:
		return true
	case e.Code == "insufficient_quota":
		return true
	}
	switch e.Type {
	case "authentication_error", "permission_error", "billing_error", "insufficient_quota":
		return true
	}
	return false
}

func retryable(e *StatusError) bool {
	if isAuth(e) {
		return false
	}
	switch e.Status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return e.Type == "overloaded_error"
}

// Redact replaces the endpoint's key in s.
func (ep Endpoint) Redact(s string) string {
	if ep.Key == "" {
		return s
	}
	return strings.ReplaceAll(s, ep.Key, redacted)
}

func (ep Endpoint) client() *http.Client {
	if ep.HTTPClient != nil {
		return ep.HTTPClient
	}
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect }}
}

// Do sends one request, retrying a transient failure (SPEC §8): 408, 429, 500, 502, 503,
// 504, 529 and an overloaded error up to MaxRetries times, and a connection error before any
// answer. It waits Retry-After when the answer has one, else an exponential backoff with
// jitter. A wait that would pass the context's deadline is not made: the error is returned
// with its ResetsAt, so a rate limit reaches the run's own wait (MVP.md §25.2). It returns the
// response of a 2xx answer with its body unread: a stream is never retried once it started.
// Every error it returns is redacted.
func (ep Endpoint) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	backoff := ep.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	for attempt := 0; ; attempt++ {
		resp, err := ep.attempt(ctx, method, path, body)
		if err == nil {
			return resp, nil
		}
		var se *StatusError
		isStatus := errors.As(err, &se)
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, errRedirect), isStatus && !retryable(se), attempt == MaxRetries:
			return nil, err
		}
		wait := time.Duration(float64(backoff<<attempt) * (0.5 + rand.Float64()))
		if isStatus && se.RetryAfter > 0 {
			wait = se.RetryAfter
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
			return nil, err
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (ep Endpoint) attempt(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, ep.BaseURL+path, reader)
	if err != nil {
		return nil, errors.New(ep.Redact(err.Error()))
	}
	for name, values := range ep.Header {
		req.Header[name] = values
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ep.client().Do(req)
	if err != nil {
		if errors.Is(err, errRedirect) {
			return nil, errRedirect
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New(ep.Redact(err.Error()))
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return nil, ep.statusError(resp, raw, time.Now())
}

func (ep Endpoint) statusError(resp *http.Response, raw []byte, now time.Time) *StatusError {
	se := &StatusError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), now)}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil && (envelope.Error.Type != "" || envelope.Error.Message != "") {
		se.Type = ep.Redact(envelope.Error.Type)
		if code, ok := envelope.Error.Code.(string); ok {
			se.Code = ep.Redact(code)
		}
		se.Message = ep.Redact(envelope.Error.Message)
	} else {
		se.Message = ep.Redact(strings.TrimSpace(string(raw)))
	}
	se.Message = truncate(se.Message, maxErrorMessage)
	if se.Status == http.StatusTooManyRequests {
		se.ResetsAt = resetsAt(resp.Header, se.RetryAfter, now)
	}
	return se
}

// resetsAt is the latest reset the answer names: Retry-After, Anthropic's
// anthropic-ratelimit-*-reset (RFC 3339) and OpenAI's x-ratelimit-reset-* (a duration).
func resetsAt(h http.Header, retryAfter time.Duration, now time.Time) time.Time {
	var latest time.Time
	if retryAfter > 0 {
		latest = now.Add(retryAfter)
	}
	for name, values := range h {
		lower := strings.ToLower(name)
		for _, v := range values {
			var t time.Time
			switch {
			case strings.HasPrefix(lower, "anthropic-ratelimit-") && strings.HasSuffix(lower, "-reset"):
				t, _ = time.Parse(time.RFC3339, strings.TrimSpace(v))
			case strings.HasPrefix(lower, "x-ratelimit-reset-"):
				if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
					t = now.Add(d)
				}
			}
			if t.After(latest) {
				latest = t
			}
		}
	}
	return latest
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + fmt.Sprintf("... [truncated, %d of %d bytes shown]", n, len(s))
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
