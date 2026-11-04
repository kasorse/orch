package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
)

const testKey = "sk-jev-test-0123456789"

// testClient points a client at srv with instant retries; sleeps records every wait.
func testClient(t *testing.T, url string, maxRetries int) (*httpClient, *[]time.Duration) {
	t.Helper()
	c := newHTTPClient(Options{BaseURL: url, Timeout: 2 * time.Second, MaxRetries: maxRetries}, testKey)
	var sleeps []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return ctx.Err()
	}
	return c, &sleeps
}

func testRequest() systemOneRequest {
	return systemOneRequest{
		Model: "jev-1.13.0",
		State: map[string]any{"request": "fix the nil pointer in ProductRepository"},
		Questions: map[string]question{
			"stage_testing": {Type: questionNoul, Instructions: "Does fulfilling `request` require running tests?"},
		},
	}
}

const okBody = `{"model":"jev-1.13.0","answers":{"stage_testing":{"type":"noul","noul":0.8}},"usage":{"input_tokens":10,"output_tokens":1}}`

func asDecisionError(t *testing.T, err error) *apperr.DecisionError {
	t.Helper()
	var derr *apperr.DecisionError
	if !errors.As(err, &derr) {
		t.Fatalf("expected *apperr.DecisionError, got %T: %v", err, err)
	}
	if derr.Provider != ProviderName {
		t.Errorf("Provider = %q, want %q", derr.Provider, ProviderName)
	}
	return derr
}

func TestDecide_SendsRequestAndDecodesAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != systemOnePath {
			t.Errorf("got %s %s, want POST %s", r.Method, r.URL.Path, systemOnePath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q, want Bearer token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		if body["model"] != "jev-1.13.0" {
			t.Errorf("model = %v", body["model"])
		}
		q := body["questions"].(map[string]any)["stage_testing"].(map[string]any)
		if q["type"] != "noul" {
			t.Errorf("question type = %v", q["type"])
		}
		if _, ok := q["criteria"]; ok {
			t.Error("nil criteria must be omitted, not sent as null")
		}
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL+"/", 1) // trailing slash must not double up
	resp, err := c.decide(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if resp.Model != "jev-1.13.0" {
		t.Errorf("Model = %q", resp.Model)
	}
	a := resp.Answers["stage_testing"]
	if a.Type != questionNoul || a.Noul == nil || *a.Noul != 0.8 {
		t.Errorf("answer = %+v", a)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestDecide_StatusMapping(t *testing.T) {
	cases := []struct {
		status   int
		kind     apperr.DecisionFailureKind
		attempts int // with MaxRetries = 1
	}{
		{408, apperr.DecisionTimeout, 2},
		{429, apperr.DecisionRateLimited, 2},
		{500, apperr.DecisionUnavailable, 2},
		{502, apperr.DecisionUnavailable, 2},
		{503, apperr.DecisionUnavailable, 2},
		{529, apperr.DecisionUnavailable, 2},
		{401, apperr.DecisionAuth, 1},
		{403, apperr.DecisionAuth, 1},
		{400, apperr.DecisionBadRequest, 1},
		{404, apperr.DecisionBadRequest, 1},
		{422, apperr.DecisionBadRequest, 1},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":"nope"}`)
			}))
			defer srv.Close()

			c, _ := testClient(t, srv.URL, 1)
			_, err := c.decide(context.Background(), testRequest())
			derr := asDecisionError(t, err)
			if derr.Kind != tc.kind {
				t.Errorf("Kind = %s, want %s", derr.Kind, tc.kind)
			}
			if derr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", derr.StatusCode, tc.status)
			}
			if got := int(calls.Load()); got != tc.attempts {
				t.Errorf("server saw %d requests, want %d", got, tc.attempts)
			}
			if derr.Attempts != tc.attempts {
				t.Errorf("Attempts = %d, want %d", derr.Attempts, tc.attempts)
			}
			if derr.Body != `{"error":"nope"}` {
				t.Errorf("Body = %q", derr.Body)
			}
		})
	}
}

func TestDecide_529RetriedExactlyOnceThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(529)
			return
		}
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	c, sleeps := testClient(t, srv.URL, 1)
	if _, err := c.decide(context.Background(), testRequest()); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("server saw %d requests, want 2", calls.Load())
	}
	if len(*sleeps) != 1 || (*sleeps)[0] != baseBackoff {
		t.Errorf("sleeps = %v, want [%s]", *sleeps, baseBackoff)
	}
}

func TestDecide_NoRetryWhenMaxRetriesZero(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(529)
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 0)
	_, err := c.decide(context.Background(), testRequest())
	if asDecisionError(t, err).Kind != apperr.DecisionUnavailable || calls.Load() != 1 {
		t.Errorf("err = %v, calls = %d", err, calls.Load())
	}
}

func TestDecide_BackoffDoublesAcrossRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c, sleeps := testClient(t, srv.URL, 3)
	_, err := c.decide(context.Background(), testRequest())
	if got := asDecisionError(t, err).Attempts; got != 4 {
		t.Errorf("Attempts = %d, want 4", got)
	}
	want := []time.Duration{baseBackoff, 2 * baseBackoff, 4 * baseBackoff}
	if len(*sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", *sleeps, want)
	}
	for i := range want {
		if (*sleeps)[i] != want[i] {
			t.Errorf("sleeps = %v, want %v", *sleeps, want)
		}
	}
}

func TestDecide_HonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	c, sleeps := testClient(t, srv.URL, 1)
	if _, err := c.decide(context.Background(), testRequest()); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(*sleeps) != 1 || (*sleeps)[0] != time.Second {
		t.Errorf("sleeps = %v, want [1s]", *sleeps)
	}
}

func TestDecide_RetryAfterBeyondTimeoutIsNotWaitedFor(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer srv.Close()

	c, sleeps := testClient(t, srv.URL, 1)
	_, err := c.decide(context.Background(), testRequest())
	if asDecisionError(t, err).Kind != apperr.DecisionRateLimited {
		t.Errorf("err = %v", err)
	}
	if calls.Load() != 1 || len(*sleeps) != 0 {
		t.Errorf("calls = %d, sleeps = %v; want one call, no wait", calls.Load(), *sleeps)
	}
}

func TestDecide_NonJSONIsInvalidResponse(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "<html>gateway</html>")
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 1)
	_, err := c.decide(context.Background(), testRequest())
	derr := asDecisionError(t, err)
	if derr.Kind != apperr.DecisionInvalidResponse || derr.StatusCode != 200 {
		t.Errorf("Kind = %s, StatusCode = %d", derr.Kind, derr.StatusCode)
	}
	if calls.Load() != 1 {
		t.Errorf("invalid response must not be retried, server saw %d requests", calls.Load())
	}
}

func TestDecide_ErrorBodyTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = io.WriteString(w, strings.Repeat("x", 10*maxErrorBody))
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 1)
	_, err := c.decide(context.Background(), testRequest())
	derr := asDecisionError(t, err)
	if !strings.HasPrefix(derr.Body, strings.Repeat("x", maxErrorBody)+"... [truncated") {
		t.Errorf("Body not truncated to %d bytes: len=%d", maxErrorBody, len(derr.Body))
	}
	if len(derr.Body) > maxErrorBody+100 {
		t.Errorf("Body too long: %d bytes", len(derr.Body))
	}
}

func TestDecide_PerAttemptTimeout(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// The server notices the client hanging up only once the body is consumed.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release) // unblock handlers before Close waits for them

	c, sleeps := testClient(t, srv.URL, 1)
	c.timeout = 50 * time.Millisecond
	_, err := c.decide(context.Background(), testRequest())
	derr := asDecisionError(t, err)
	if len(*sleeps) != 1 || (*sleeps)[0] != c.timeout {
		t.Errorf("sleeps = %v, want backoff capped at the timeout", *sleeps)
	}
	if derr.Kind != apperr.DecisionTimeout {
		t.Errorf("Kind = %s, want timeout: %v", derr.Kind, err)
	}
	if calls.Load() != 2 {
		t.Errorf("timeout is transient: server saw %d requests, want 2", calls.Load())
	}
}

func TestDecide_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens there any more

	c, sleeps := testClient(t, url, 1)
	_, err := c.decide(context.Background(), testRequest())
	derr := asDecisionError(t, err)
	if derr.Kind != apperr.DecisionConnection {
		t.Errorf("Kind = %s, want connection: %v", derr.Kind, err)
	}
	if derr.Attempts != 2 || len(*sleeps) != 1 {
		t.Errorf("connection failures are transient: Attempts = %d, sleeps = %v", derr.Attempts, *sleeps)
	}
}

func TestDecide_UnknownHost(t *testing.T) {
	c, _ := testClient(t, "http://jev.invalid", 0)
	_, err := c.decide(context.Background(), testRequest())
	if kind := asDecisionError(t, err).Kind; kind != apperr.DecisionConnection {
		t.Errorf("Kind = %s, want connection: %v", kind, err)
	}
}

func TestDecide_CancelledIsNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		cancel() // Ctrl-C while the request is in flight
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, sleeps := testClient(t, srv.URL, 3)
	_, err := c.decide(ctx, testRequest())
	derr := asDecisionError(t, err)
	if derr.Kind != apperr.DecisionCancelled {
		t.Errorf("Kind = %s, want cancelled", derr.Kind)
	}
	if !errors.Is(err, context.Canceled) {
		t.Error("cancellation must unwrap to context.Canceled")
	}
	if calls.Load() != 1 || len(*sleeps) != 0 {
		t.Errorf("calls = %d, sleeps = %v; want no retry", calls.Load(), *sleeps)
	}
}

func TestDecide_CancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(529)
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 1)
	c.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	_, err := c.decide(ctx, testRequest())
	if kind := asDecisionError(t, err).Kind; kind != apperr.DecisionCancelled {
		t.Errorf("Kind = %s, want cancelled", kind)
	}
}

func TestDecide_KeyNeverInErrors(t *testing.T) {
	// A hostile or misconfigured server echoing the request headers back must not
	// leak the key into any error, for any status.
	statuses := []int{200, 400, 401, 403, 408, 422, 429, 500, 529}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "echo: "+r.Header.Get("Authorization")) // not JSON either
			}))
			defer srv.Close()

			c, _ := testClient(t, srv.URL, 1)
			_, err := c.decide(context.Background(), testRequest())
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("API key leaked into error: %s", err.Error())
			}
			if !strings.Contains(err.Error(), redacted) {
				t.Errorf("expected echoed key to be redacted, got: %s", err.Error())
			}
		})
	}

	t.Run("transport error", func(t *testing.T) {
		c, _ := testClient(t, "http://jev.invalid", 0)
		_, err := c.decide(context.Background(), testRequest())
		if err == nil || strings.Contains(err.Error(), testKey) {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != modelsPath {
			t.Errorf("got %s %s, want GET %s", r.Method, r.URL.Path, modelsPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"models":[{"name":"jev-latest","description":"latest","release_date":"2026-05-01"}]}`)
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 1)
	models, err := c.listModels(context.Background())
	if err != nil {
		t.Fatalf("listModels: %v", err)
	}
	if len(models) != 1 || models[0].Name != "jev-latest" || models[0].ReleaseDate != "2026-05-01" {
		t.Errorf("models = %+v", models)
	}
}

func TestListModels_Auth(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 1)
	_, err := c.listModels(context.Background())
	if kind := asDecisionError(t, err).Kind; kind != apperr.DecisionAuth || calls.Load() != 1 {
		t.Errorf("Kind = %s, calls = %d; want auth, no retry", kind, calls.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"":                              0,
		"3":                             3 * time.Second,
		"-1":                            0,
		"soon":                          0,
		"Fri, 25 Sep 2026 12:00:02 GMT": 2 * time.Second,
		"Fri, 25 Sep 2026 11:00:00 GMT": 0, // in the past
	}
	for in, want := range cases {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDecide_NonStandardStatusHasNoDanglingCause(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(529)
	}))
	defer srv.Close()

	c, _ := testClient(t, srv.URL, 0)
	_, err := c.decide(context.Background(), testRequest())
	if msg := err.Error(); strings.HasSuffix(msg, ": ") || !strings.HasSuffix(msg, "HTTP 529") {
		t.Errorf("message = %q", msg)
	}
}
