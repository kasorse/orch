package anthropic

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

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/httpapi"
)

var update = flag.Bool("update", false, "rewrite the golden request bodies")

const testKey = "sk-ant-api03-test-0123456789"

// fake serves recorded streams: each request gets the next answer (the last one repeats), and
// its headers and body are kept.
type fake struct {
	srv     *httptest.Server
	calls   atomic.Int32
	headers []http.Header
	bodies  [][]byte
}

type answer struct {
	status int
	header map[string]string
	stream string // a file under testdata/streams, or a literal body
	hold   bool   // stop after the first event and wait for the client to go away
}

func newFake(t *testing.T, answers ...answer) *fake {
	t.Helper()
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.calls.Add(1)) - 1
		body, _ := io.ReadAll(r.Body)
		f.headers, f.bodies = append(f.headers, r.Header.Clone()), append(f.bodies, body)
		a := answers[min(n, len(answers)-1)]
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		status := a.status
		if status == 0 {
			status = 200
			w.Header().Set("Content-Type", "text/event-stream")
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
		if a.hold {
			first, _, _ := strings.Cut(content, "\n\n")
			_, _ = io.WriteString(w, first+"\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, content)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func client(f *fake, prices map[string]httpapi.Price) *Client {
	c := New(f.srv.URL+"/", true, prices)
	c.SetKey(testKey)
	c.ep.Backoff = time.Millisecond
	return c
}

func noneRequest() provider.Request {
	return provider.Request{Prompt: "ACTION: translate\nTranslate this.", Model: "claude-opus-5-5", Authority: domain.AuthorityNone, WorkingDir: "/repo"}
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
		t.Fatalf("%v (run go test ./internal/provider/anthropic -update)", err)
	}
	if !bytes.Equal(pretty.Bytes(), want) {
		t.Errorf("request body %s changed:\n%s", name, pretty.String())
	}
}

func TestExecute_Plain(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse"})
	var live bytes.Buffer
	var activity []string
	req := noneRequest()
	req.Stream = &live
	req.Activity = func(s string) { activity = append(activity, s) }
	res, err := client(f, nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != `{"answer": 42}` || live.String() != "{\"answer\": 42}\n" {
		t.Errorf("Output %q, live %q", res.Output, live.String())
	}
	if strings.Join(activity, ",") != "Thinking,Writing" {
		t.Errorf("activity = %q", activity)
	}
	u := res.Usage
	if u == nil || u.InputTokens != 1200 || u.OutputTokens != 350 || u.Turns != 1 || u.Partial {
		t.Fatalf("Usage = %+v", u)
	}
	if want := (1200*4.0 + 350*20.0) / 1e6; u.CostUSD != want {
		t.Errorf("CostUSD = %v, want %v from the built-in price", u.CostUSD, want)
	}
	if c := res.Call; c == nil || len(c.Models) != 1 || c.Models[0].Model != "claude-opus-5-5" || c.Models[0].CostUSD != u.CostUSD {
		t.Errorf("Call = %+v", res.Call)
	}
	h := f.headers[0]
	if h.Get("X-Api-Key") != testKey || h.Get("Anthropic-Version") != apiVersion || h.Get("Anthropic-Beta") != "" || h.Get("Authorization") != "" {
		t.Errorf("headers = %v", h)
	}
	golden(t, "none", f.bodies[0])
}

func TestExecute_EffortAndGatewayCost(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse", header: map[string]string{"x-litellm-response-cost": "0.0042"}})
	req := noneRequest()
	req.Model, req.Effort = "bedrock/eu.anthropic.claude-opus-5-5", "low"
	res, err := client(f, nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.CostUSD != 0.0042 {
		t.Errorf("CostUSD = %v, want the gateway's 0.0042", res.Usage.CostUSD)
	}
	golden(t, "none-effort", f.bodies[0])
}

func TestExecute_OperatorPrice(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse"})
	req := noneRequest()
	req.Model = "bedrock/eu.anthropic.claude-opus-5-5"
	c := client(f, map[string]httpapi.Price{req.Model: {Input: 10, Output: 100}})
	res, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := (1200*10.0 + 350*100.0) / 1e6; res.Usage.CostUSD != want {
		t.Errorf("CostUSD = %v, want %v", res.Usage.CostUSD, want)
	}
	if !c.ReportsCost(req.Model) || c.ReportsCost("bedrock/other") {
		t.Error("ReportsCost does not follow the prices")
	}
}

func TestExecute_Failures(t *testing.T) {
	for name, tc := range map[string]struct {
		answers []answer
		kind    apperr.FailureKind
		detail  string
		status  int
		partial bool
	}{
		"refusal":      {[]answer{{stream: "refusal.sse"}}, apperr.FailureAPI, "the model refused the request (category cyber)", 0, false},
		"max_tokens":   {[]answer{{stream: "max_tokens.sse"}}, apperr.FailureAPI, "cut at max_tokens (64000)", 0, false},
		"stream error": {[]answer{{stream: "stream_error.sse"}}, apperr.FailureAPI, "overloaded_error: Overloaded", 0, true},
		"broken":       {[]answer{{stream: "broken.sse"}}, apperr.FailureAPI, "ended before message_stop", 0, true},
		"invalid":      {[]answer{{stream: "invalid.sse"}}, apperr.FailureAPI, "the stream is not valid: content_block_delta for block 3", 0, true},
		"401": {[]answer{{status: 401, stream: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key ` + testKey + `"}}`}},
			apperr.FailureAuth, "authentication_error", 401, false},
		"400": {[]answer{{status: 400, stream: `{"type":"error","error":{"type":"invalid_request_error","message":"effort is not supported"}}`}},
			apperr.FailureAPI, "effort is not supported", 400, false},
		"429 exhausted": {[]answer{{status: 429, stream: `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`}},
			apperr.FailureRateLimit, "rate_limit_error", 429, false},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, tc.answers...)
			res, err := client(f, nil).Execute(context.Background(), noneRequest())
			var pe *apperr.ProviderError
			if !errors.As(err, &pe) {
				t.Fatalf("want a ProviderError, got %v", err)
			}
			if pe.Kind != tc.kind || !strings.Contains(pe.Detail, tc.detail) || pe.APIStatus != tc.status {
				t.Errorf("kind %q, detail %q, status %d", pe.Kind, pe.Detail, pe.APIStatus)
			}
			if pe.Endpoint != "POST "+f.srv.URL+"/v1/messages" || pe.Executable != "" || pe.PromptBytes != len(noneRequest().Prompt) {
				t.Errorf("endpoint %q, executable %q, prompt %d", pe.Endpoint, pe.Executable, pe.PromptBytes)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Errorf("the key is in the error:\n%v", err)
			}
			if tc.partial && (res.Usage == nil || !res.Usage.Partial || res.Usage.InputTokens != 10) {
				t.Errorf("want a partial usage, got %+v", res.Usage)
			}
		})
	}
}

func TestExecute_RetriesThenAnswers(t *testing.T) {
	f := newFake(t, answer{status: 529, stream: `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`}, answer{stream: "plain.sse"})
	if _, err := client(f, nil).Execute(context.Background(), noneRequest()); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 || !bytes.Equal(f.bodies[0], f.bodies[1]) {
		t.Errorf("calls = %d, the same body resent: %v", f.calls.Load(), bytes.Equal(f.bodies[0], f.bodies[1]))
	}
}

func TestExecute_CancelledMidStream(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse", hold: true})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := client(f, nil).Execute(ctx, noneRequest())
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || pe.Kind != apperr.FailureTimeout {
		t.Fatalf("want a timeout, got %v", err)
	}
	if res.Usage == nil || !res.Usage.Partial || res.Usage.InputTokens != 1200 {
		t.Errorf("want the partial usage of message_start, got %+v", res.Usage)
	}
}

func TestExecute_Refusals(t *testing.T) {
	for _, a := range []domain.Authority{domain.AuthorityEdit, ""} {
		req := noneRequest()
		req.Authority = a
		_, err := client(newFake(t, answer{stream: "plain.sse"}), nil).Execute(context.Background(), req)
		var ae *apperr.AuthorityError
		if !errors.As(err, &ae) {
			t.Errorf("authority %q: want an AuthorityError, got %v", a, err)
		}
	}
	_, err := New("", true, nil).Execute(context.Background(), noneRequest())
	var ce *apperr.ConfigError
	if !errors.As(err, &ce) {
		t.Errorf("without SetKey: want a ConfigError, got %v", err)
	}
}

func TestNew_DefaultBaseURL(t *testing.T) {
	c := New("", true, nil)
	c.SetKey(testKey)
	if ep, _ := c.Endpoint(); ep.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q", ep.BaseURL)
	}
}

func webRequest() provider.Request {
	req := noneRequest()
	req.Prompt, req.Authority = "ACTION: research\nWhat is new in Go 1.26?", domain.AuthorityWeb
	return req
}

func TestExecute_WebWithServerTools(t *testing.T) {
	f := newFake(t, answer{stream: "web.sse"})
	var activity []string
	var live bytes.Buffer
	req := webRequest()
	req.Activity = func(s string) { activity = append(activity, s) }
	req.Stream = &live
	res, err := client(f, nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Searching. Go 1.26 adds X." {
		t.Errorf("Output = %q", res.Output)
	}
	if c := res.Call; c.Tools["WebSearch"] != 1 || strings.Join(c.WebTools, ",") != "WebFetch,WebSearch" {
		t.Errorf("Tools %v, WebTools %v", c.Tools, c.WebTools)
	}
	if !slicesContain(activity, "WebSearch go 1.26 release notes") || !strings.Contains(live.String(), "→ WebSearch go 1.26 release notes\n") {
		t.Errorf("activity %q, live %q", activity, live.String())
	}
	golden(t, "web", f.bodies[0])
}

func TestExecute_WebWithoutTools(t *testing.T) {
	f := newFake(t, answer{stream: "plain.sse"})
	c := New(f.srv.URL, false, nil)
	c.SetKey(testKey)
	res, err := c.Execute(context.Background(), webRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Call.WebTools != nil || res.Call.Tools != nil {
		t.Errorf("WebTools %v, Tools %v: a call without web tools offers none", res.Call.WebTools, res.Call.Tools)
	}
	golden(t, "web-off", f.bodies[0])
}

// pause_turn: the conversation goes back as it is, the assistant's blocks unchanged, and the
// usage of both responses adds up.
func TestExecute_PauseTurn(t *testing.T) {
	f := newFake(t, answer{stream: "pause.sse"}, answer{stream: "after_pause.sse"})
	res, err := client(f, nil).Execute(context.Background(), webRequest())
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 2 || res.Output != "Go 1.26 adds X." {
		t.Fatalf("calls %d, Output %q", f.calls.Load(), res.Output)
	}
	if u := res.Usage; u.Turns != 2 || u.InputTokens != 6000 || u.OutputTokens != 150 {
		t.Errorf("Usage = %+v", u)
	}
	var second struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(f.bodies[1], &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Messages) != 2 || second.Messages[1].Role != "assistant" || len(second.Messages[1].Content) != 2 ||
		second.Messages[1].Content[0]["type"] != "server_tool_use" || second.Messages[1].Content[1]["type"] != "web_search_tool_result" {
		t.Fatalf("the second request does not carry the paused turn: %s", f.bodies[1])
	}
	if input, _ := second.Messages[1].Content[0]["input"].(map[string]any); input["query"] != "go 1.26 release notes" {
		t.Errorf("the tool's input was not sent back: %v", second.Messages[1].Content[0])
	}
	if !bytes.HasPrefix(f.bodies[1], f.bodies[0][:bytes.Index(f.bodies[0], []byte(`"messages"`))]) {
		t.Error("the second request's prefix before messages changed")
	}
}

func TestExecute_PauseLimit(t *testing.T) {
	f := newFake(t, answer{stream: "pause.sse"})
	_, err := client(f, nil).Execute(context.Background(), webRequest())
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || !strings.Contains(pe.Detail, "paused the turn 5 times") || f.calls.Load() != maxPauses+1 {
		t.Fatalf("calls %d, err %v", f.calls.Load(), err)
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
