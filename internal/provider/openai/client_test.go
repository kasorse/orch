package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/provider/httpapi"
)

var update = flag.Bool("update", false, "rewrite the golden request bodies")

const testKey = "sk-proj-test-0123456789"

// prefixes are the base URLs every case runs against: the first-party shape and a gateway's,
// with a path prefix (acceptance of AT04: nothing assumes api.openai.com).
var prefixes = []string{"", "/litellm"}

type fake struct {
	srv     *httptest.Server
	calls   atomic.Int32
	paths   []string
	headers []http.Header
	bodies  [][]byte
}

type answer struct {
	status int
	header map[string]string
	stream string // a file under testdata/streams, or a literal body
}

func newFake(t *testing.T, answers ...answer) *fake {
	t.Helper()
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.calls.Add(1)) - 1
		body, _ := io.ReadAll(r.Body)
		f.paths, f.headers, f.bodies = append(f.paths, r.URL.Path), append(f.headers, r.Header.Clone()), append(f.bodies, body)
		a := answers[min(n, len(answers)-1)]
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		status := a.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		content := a.stream
		if strings.HasSuffix(content, ".sse") {
			raw, err := os.ReadFile(filepath.Join("testdata", "streams", content))
			if err != nil {
				t.Error(err)
				return
			}
			content = string(raw)
		}
		_, _ = io.WriteString(w, content)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func client(f *fake, prefix string, prices map[string]httpapi.Price) *Client {
	c := New(f.srv.URL+prefix, true, prices)
	c.nativePatch = true // the fake stands for the first-party endpoint
	c.SetKey(testKey)
	c.ep.Backoff = time.Millisecond
	return c
}

func noneRequest() provider.Request {
	return provider.Request{Prompt: "ACTION: decide-classify\nClassify this.", Model: "gpt-5.5", Authority: domain.AuthorityNone, WorkingDir: "/repo"}
}

func golden(t *testing.T, name string, body []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}
	pretty.WriteByte('\n')
	path := filepath.Join("testdata", "requests", name+".json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/provider/openai -update)", err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Errorf("request body %s changed:\n%s", name, pretty.String())
	}
}

func TestExecute_Plain(t *testing.T) {
	for _, prefix := range prefixes {
		t.Run("prefix="+prefix, func(t *testing.T) {
			f := newFake(t, answer{stream: "plain.sse"})
			var live bytes.Buffer
			var activity []string
			req := noneRequest()
			req.Stream = &live
			req.Activity = func(s string) { activity = append(activity, s) }
			res, err := client(f, prefix, nil).Execute(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if res.Output != `{"answer": 42}` || live.String() != "{\"answer\": 42}\n" {
				t.Errorf("Output %q, live %q", res.Output, live.String())
			}
			if strings.Join(activity, ",") != "Writing" {
				t.Errorf("activity = %q", activity)
			}
			u := res.Usage
			want := domain.Usage{InputTokens: 500, CacheReadTokens: 1500, OutputTokens: 100, ReasoningTokens: 200, Turns: 1}
			want.CostUSD = (500*5.0 + 1500*0.5 + 300*30.0) / 1e6
			if u == nil || *u != want {
				t.Fatalf("Usage = %+v, want %+v", u, want)
			}
			if c := res.Call; c == nil || len(c.Models) != 1 || c.Models[0].Model != "gpt-5.5-2026-09-01" || c.Models[0].CostUSD != want.CostUSD {
				t.Errorf("Call = %+v", res.Call)
			}
			if f.paths[0] != prefix+"/v1/responses" {
				t.Errorf("path = %q", f.paths[0])
			}
			if h := f.headers[0]; h.Get("Authorization") != "Bearer "+testKey || h.Get("X-Api-Key") != "" || h.Get("Openai-Beta") != "" {
				t.Errorf("headers = %v", h)
			}
			golden(t, "none", f.bodies[0])
		})
	}
}

func TestExecute_EffortAndGatewayCost(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse", header: map[string]string{"x-litellm-response-cost": "0.0031"}})
	req := noneRequest()
	req.Model, req.Effort = "openai/gpt-5.5", "high"
	res, err := client(f, "/litellm", nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.CostUSD != 0.0031 {
		t.Errorf("CostUSD = %v, want the gateway's 0.0031", res.Usage.CostUSD)
	}
	golden(t, "none-effort", f.bodies[0])
}

func TestExecute_Failures(t *testing.T) {
	for name, tc := range map[string]struct {
		answer answer
		kind   apperr.FailureKind
		detail string
		status int
		usage  bool
	}{
		"incomplete":  {answer{stream: "incomplete.sse"}, apperr.FailureAPI, "the response is incomplete: max_output_tokens", 0, true},
		"failed":      {answer{stream: "failed.sse"}, apperr.FailureAPI, "the response failed: server_error: The server had an error", 0, false},
		"failed rate": {answer{stream: "failed_rate.sse"}, apperr.FailureRateLimit, "rate_limit_exceeded", 0, false},
		"error event": {answer{stream: "error_event.sse"}, apperr.FailureAPI, "the API sent an error in the stream: server_error: Something broke", 0, false},
		"broken":      {answer{stream: "broken.sse"}, apperr.FailureAPI, "the stream ended before the response completed", 0, false},
		"invalid":     {answer{stream: "invalid.sse"}, apperr.FailureAPI, "the response has no status", 0, false},
		"401": {answer{status: 401, stream: `{"error":{"message":"Incorrect API key provided: ` + testKey + `","type":"invalid_request_error","code":"invalid_api_key"}}`},
			apperr.FailureAuth, "invalid_api_key", 401, false},
		"quota": {answer{status: 429, stream: `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`},
			apperr.FailureAuth, "insufficient_quota", 429, false},
		"429": {answer{status: 429, stream: `{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`},
			apperr.FailureRateLimit, "rate_limit_exceeded", 429, false},
	} {
		for _, prefix := range prefixes {
			t.Run(name+"/prefix="+prefix, func(t *testing.T) {
				f := newFake(t, tc.answer)
				res, err := client(f, prefix, nil).Execute(context.Background(), noneRequest())
				var pe *apperr.ProviderError
				if !errors.As(err, &pe) {
					t.Fatalf("want a ProviderError, got %v", err)
				}
				if pe.Kind != tc.kind || !strings.Contains(pe.Detail, tc.detail) || pe.APIStatus != tc.status {
					t.Errorf("kind %q, detail %q, status %d", pe.Kind, pe.Detail, pe.APIStatus)
				}
				if pe.Endpoint != "POST "+f.srv.URL+prefix+"/v1/responses" || pe.Provider != "codex" {
					t.Errorf("endpoint %q, provider %q", pe.Endpoint, pe.Provider)
				}
				if strings.Contains(err.Error(), testKey) {
					t.Errorf("the key is in the error:\n%v", err)
				}
				if tc.usage != (res.Usage != nil) {
					t.Errorf("usage = %+v, want one: %v", res.Usage, tc.usage)
				}
			})
		}
	}
}

func TestExecute_RetriesThenAnswers(t *testing.T) {
	f := newFake(t, answer{status: 503, stream: `{"error":{"message":"busy","type":"server_error"}}`}, answer{stream: "plain.sse"})
	if _, err := client(f, "", nil).Execute(context.Background(), noneRequest()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 || !bytes.Equal(f.bodies[0], f.bodies[1]) {
		t.Errorf("calls = %d, the same body resent: %v", f.calls.Load(), bytes.Equal(f.bodies[0], f.bodies[1]))
	}
}

func TestExecute_OperatorPrice(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse"})
	req := noneRequest()
	req.Model = "openai/gpt-5.5"
	c := client(f, "", map[string]httpapi.Price{req.Model: {Input: 1, Output: 2, CacheRead: 0}})
	res, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := (500*1.0 + 300*2.0) / 1e6; res.Usage.CostUSD != want {
		t.Errorf("CostUSD = %v, want %v", res.Usage.CostUSD, want)
	}
	if !c.ReportsCost(req.Model) || !c.ReportsCost("gpt-5.5") || c.ReportsCost("openai/other") {
		t.Error("ReportsCost does not follow the prices and the table")
	}
}

func TestExecute_Refusals(t *testing.T) {
	for _, a := range []domain.Authority{"admin", ""} {
		req := noneRequest()
		req.Authority = a
		_, err := client(newFake(t, answer{stream: "plain.sse"}), "", nil).Execute(context.Background(), req)
		var ae *apperr.AuthorityError
		if !errors.As(err, &ae) {
			t.Errorf("authority %q: want an AuthorityError, got %v", a, err)
		}
	}
	var ce *apperr.ConfigError
	if _, err := New("", true, nil).Execute(context.Background(), noneRequest()); !errors.As(err, &ce) {
		t.Errorf("without SetKey: want a ConfigError, got %v", err)
	}
	c := New("", true, nil)
	c.SetKey(testKey)
	if ep, _ := c.Endpoint(); ep.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q", ep.BaseURL)
	}
}
