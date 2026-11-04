package httpapi

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

const testKey = "test-key-0123456789"

// server answers each request with the next of answers (the last one repeats) and counts them.
type answer struct {
	status int
	header map[string]string
	body   string
}

func newServer(t *testing.T, answers ...answer) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		a := answers[min(n, len(answers)-1)]
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(a.status)
		_, _ = io.WriteString(w, a.body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func endpoint(url string) Endpoint {
	return Endpoint{BaseURL: url, Header: http.Header{"X-Api-Key": {testKey}}, Key: testKey, Backoff: time.Millisecond}
}

func TestDo_SendsHeadersAndBody(t *testing.T) {
	var gotKey, gotType, gotBody, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotType, gotPath = r.Header.Get("X-Api-Key"), r.Header.Get("Content-Type"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	resp, err := endpoint(srv.URL+"/gw").Do(context.Background(), http.MethodPost, "/v1/messages", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotKey != testKey || gotType != "application/json" || gotBody != `{"a":1}` || gotPath != "/gw/v1/messages" {
		t.Errorf("key %q, type %q, body %q, path %q", gotKey, gotType, gotBody, gotPath)
	}
}

func TestDo_Retries(t *testing.T) {
	ok := answer{status: 200, body: "ok"}
	for name, tc := range map[string]struct {
		answers []answer
		calls   int32
		kind    apperr.FailureKind
		status  int // 0 = success
	}{
		"overloaded then ok":  {[]answer{{status: 529, body: `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`}, {status: 529}, ok}, 3, "", 0},
		"5xx exhausted":       {[]answer{{status: 500}}, MaxRetries + 1, "", 500},
		"429 then ok":         {[]answer{{status: 429, body: `{"error":{"type":"rate_limit_error","message":"slow"}}`}, ok}, 2, "", 0},
		"401 not retried":     {[]answer{{status: 401, body: `{"error":{"type":"authentication_error","message":"bad key ` + testKey + `"}}`}}, 1, apperr.FailureAuth, 401},
		"quota not retried":   {[]answer{{status: 429, body: `{"error":{"type":"insufficient_quota","code":"insufficient_quota","message":"no credit"}}`}}, 1, apperr.FailureAuth, 429},
		"400 not retried":     {[]answer{{status: 400, body: `{"error":{"type":"invalid_request_error","message":"no"}}`}}, 1, "", 400},
		"overloaded on a 400": {[]answer{{status: 400, body: `{"error":{"type":"overloaded_error"}}`}, ok}, 2, "", 0},
	} {
		t.Run(name, func(t *testing.T) {
			srv, calls := newServer(t, tc.answers...)
			resp, err := endpoint(srv.URL).Do(context.Background(), http.MethodPost, "/v1/x", []byte("{}"))
			if got := calls.Load(); got != tc.calls {
				t.Errorf("calls = %d, want %d", got, tc.calls)
			}
			if tc.status == 0 {
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				resp.Body.Close()
				return
			}
			var se *StatusError
			if !errors.As(err, &se) || se.Status != tc.status {
				t.Fatalf("want HTTP %d, got %v", tc.status, err)
			}
			if Kind(err) != tc.kind {
				t.Errorf("Kind = %q, want %q", Kind(err), tc.kind)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("the key is in the error: %v", err)
			}
		})
	}
}

// A Retry-After past the context's deadline is not waited for: the 429 comes back at once with
// its reset time, for the run's own wait (MVP.md §25.2).
func TestDo_RateLimitPastDeadline(t *testing.T) {
	srv, calls := newServer(t, answer{status: 429, header: map[string]string{"Retry-After": "3600"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := endpoint(srv.URL).Do(ctx, http.MethodPost, "/v1/x", []byte("{}"))
	var se *StatusError
	if !errors.As(err, &se) || Kind(err) != apperr.FailureRateLimit {
		t.Fatalf("want a rate limit, got %v", err)
	}
	if calls.Load() != 1 || time.Since(start) > 2*time.Second {
		t.Errorf("calls = %d after %s: the wait should not have been made", calls.Load(), time.Since(start))
	}
	if d := time.Until(se.ResetsAt); d < 59*time.Minute || d > 61*time.Minute {
		t.Errorf("ResetsAt in %s, want about an hour", d)
	}
}

func TestResetsAt(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	h := http.Header{}
	h.Set("anthropic-ratelimit-tokens-reset", "2026-11-01T12:05:00Z")
	h.Set("anthropic-ratelimit-requests-reset", "2026-11-01T12:01:00Z")
	if got := resetsAt(h, time.Minute, now); !got.Equal(now.Add(5 * time.Minute)) {
		t.Errorf("anthropic: %s", got)
	}
	h = http.Header{}
	h.Set("x-ratelimit-reset-tokens", "6m0s")
	h.Set("x-ratelimit-reset-requests", "20ms")
	if got := resetsAt(h, 0, now); !got.Equal(now.Add(6 * time.Minute)) {
		t.Errorf("openai: %s", got)
	}
	if got := resetsAt(http.Header{}, 0, now); !got.IsZero() {
		t.Errorf("nothing said: %s", got)
	}
}

func TestDo_RedirectNotFollowed(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Store(true) }))
	defer target.Close()
	srv, calls := newServer(t, answer{status: 302, header: map[string]string{"Location": target.URL}})
	_, err := endpoint(srv.URL).Do(context.Background(), http.MethodGet, "/v1/models", nil)
	if !errors.Is(err, errRedirect) || followed.Load() || calls.Load() != 1 {
		t.Fatalf("err %v, followed %v, calls %d", err, followed.Load(), calls.Load())
	}
}

func TestDo_ConnectionErrorRetriedThenReturned(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	ep := endpoint(url)
	ep.Key = strings.TrimPrefix(url, "http://") // the key must be redacted from any error
	_, err := ep.Do(context.Background(), http.MethodGet, "/v1/models", nil)
	if err == nil || strings.Contains(err.Error(), ep.Key) {
		t.Fatalf("want a redacted connection error, got %v", err)
	}
}

func TestDo_CancelledDuringWait(t *testing.T) {
	srv, _ := newServer(t, answer{status: 503})
	ep := endpoint(srv.URL)
	ep.Backoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := ep.Do(ctx, http.MethodGet, "/v1/x", nil)
	if Kind(err) != apperr.FailureCancelled {
		t.Fatalf("want cancelled, got %v", err)
	}
}

func TestSSE(t *testing.T) {
	stream := ": ping\n\nevent: message_start\ndata: {\"a\":1}\n\ndata: line1\ndata: line2\n\nevent: ignored-without-data\n\nevent: cut\ndata: last"
	s := NewSSE(strings.NewReader(stream))
	var got []Event
	for {
		ev, err := s.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	want := []Event{{"message_start", []byte(`{"a":1}`)}, {"", []byte("line1\nline2")}, {"cut", []byte("last")}}
	if len(got) != len(want) {
		t.Fatalf("got %d events: %q", len(got), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || string(got[i].Data) != string(want[i].Data) {
			t.Errorf("event %d = %q %q, want %q %q", i, got[i].Name, got[i].Data, want[i].Name, want[i].Data)
		}
	}
}

func TestSSE_LineOverBound(t *testing.T) {
	s := NewSSE(io.MultiReader(strings.NewReader("data: "), strings.NewReader(strings.Repeat("x", maxEventLine+1))))
	if _, err := s.Next(); err == nil || err == io.EOF {
		t.Fatalf("want an error for an oversized line, got %v", err)
	}
}

func TestCost(t *testing.T) {
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	p, ok := Lookup(nil, "claude-opus-5-5")
	if !ok {
		t.Fatal("claude-opus-5-5 is not in the table")
	}
	u := domain.Usage{InputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheCreationTokens: 1_000_000, OutputTokens: 500_000, ReasoningTokens: 500_000}
	if got := p.Cost(u); !near(got, 4+0.20+5+20) {
		t.Errorf("opus cost = %v", got)
	}
	haiku, _ := Lookup(nil, "claude-haiku-5-5")
	if got := haiku.Cost(domain.Usage{InputTokens: 100_000, OutputTokens: 1_000_000}); !near(got, 0.01+0.50) {
		t.Errorf("haiku short = %v", got)
	}
	if got := haiku.Cost(domain.Usage{InputTokens: 100_001, OutputTokens: 1_000_000}); !near(got, 100_001*0.50/1e6+2.50) {
		t.Errorf("haiku long = %v", got)
	}
	over := map[string]Price{"claude-opus-5-5": {Input: 1, Output: 1}, "bedrock/x": {Input: 2, Output: 3}}
	if p, _ := Lookup(over, "claude-opus-5-5"); p.Input != 1 {
		t.Error("the override does not win over the table")
	}
	if _, ok := Lookup(nil, "bedrock/eu.anthropic.claude-opus-5-5"); ok {
		t.Error("a gateway name must not be priced by the table")
	}
}

func TestGatewayCost(t *testing.T) {
	for v, want := range map[string]bool{"0.0123": true, "0": true, "": false, "-1": false, "NaN": false, "Inf": false, "abc": false} {
		h := http.Header{}
		if v != "" {
			h.Set("x-litellm-response-cost", v)
		}
		got, ok := GatewayCost(h)
		if ok != want {
			t.Errorf("%q: ok = %v", v, ok)
		}
		if ok {
			if f, _ := strconv.ParseFloat(v, 64); got != f {
				t.Errorf("%q: %v", v, got)
			}
		}
	}
}

func TestCheckModels(t *testing.T) {
	pages := map[string]string{
		"":                           `{"data":[{"id":"claude-opus-5-5"}],"has_more":true,"last_id":"claude-opus-5-5"}`,
		"after_id=claude-opus-5-5":   `{"data":[{"id":"claude-sonnet-5-5"}],"has_more":false}`,
		"after_id=claude-sonnet-5-5": `{}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		key := ""
		if a := r.URL.Query().Get("after_id"); a != "" {
			key = "after_id=" + a
		}
		_, _ = io.WriteString(w, pages[key])
	}))
	defer srv.Close()
	ep := endpoint(srv.URL)
	if err := CheckModels(context.Background(), ep, "claude", []string{"claude-sonnet-5-5", "claude-opus-5-5"}); err != nil {
		t.Fatalf("both served across two pages: %v", err)
	}
	err := CheckModels(context.Background(), ep, "claude", []string{"claude-opus-5-5", "bedrock/x", "bedrock/x"})
	var ce *apperr.ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "does not serve bedrock/x\n") || !strings.Contains(err.Error(), "It serves: claude-opus-5-5, claude-sonnet-5-5") {
		t.Fatalf("want a config error naming bedrock/x once, got %v", err)
	}

	openai, _ := newServer(t, answer{status: 200, body: `{"object":"list","data":[{"id":"gpt-5.5","object":"model"}]}`})
	if err := CheckModels(context.Background(), endpoint(openai.URL), "codex", []string{"gpt-5.5"}); err != nil {
		t.Fatalf("the OpenAI shape: %v", err)
	}
	empty, _ := newServer(t, answer{status: 200, body: `{"object":"list"}`})
	if err := CheckModels(context.Background(), endpoint(empty.URL), "codex", []string{"gpt-5.5"}); err == nil || !strings.Contains(err.Error(), "no data list") {
		t.Fatalf("want no data list, got %v", err)
	}
	refused, _ := newServer(t, answer{status: 401, body: `{"error":{"type":"authentication_error","message":"invalid x-api-key"}}`})
	if err := CheckModels(context.Background(), endpoint(refused.URL), "claude", []string{"x"}); Kind(err) != apperr.FailureAuth {
		t.Fatalf("want a refused key, got %v", err)
	}
}

// The package imports only the standard library, domain and apperr (SPEC §3).
func TestImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".go" || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(p, "/")
			switch {
			case p == "github.com/kasorse/orch/internal/domain", p == "github.com/kasorse/orch/internal/apperr":
			case strings.HasPrefix(first, "orchestrator") || strings.Contains(first, "."):
				t.Errorf("%s imports %q: httpapi may import only domain, apperr and the standard library", e.Name(), p)
			}
		}
	}
}
