package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
)

func webRequest() provider.Request {
	req := noneRequest()
	req.Prompt, req.Authority = "ACTION: research\nWhat is new in Go 1.26?", domain.AuthorityWeb
	return req
}

func webStream() string {
	search := map[string]any{"type": "web_search_call", "id": "ws_1", "status": "completed", "action": map[string]any{"type": "search", "query": "go 1.26 release notes"}}
	msg := map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Go 1.26 adds X."}}}
	done, _ := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": search})
	final, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
		"id": "resp", "model": "gpt-5.5", "status": "completed", "output": []any{search, msg},
		"usage": map[string]any{"input_tokens": 500, "output_tokens": 40},
	}})
	return fmt.Sprintf("event: response.output_item.done\ndata: %s\n\nevent: response.completed\ndata: %s\n\n", done, final)
}

func TestWeb_WithSearch(t *testing.T) {
	f := newFake(t, answer{stream: webStream()})
	var activity []string
	var live bytes.Buffer
	req := webRequest()
	req.Activity = func(s string) { activity = append(activity, s) }
	req.Stream = &live
	res, err := client(f, "", nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Go 1.26 adds X." || res.Call.Tools["WebSearch"] != 1 || strings.Join(res.Call.WebTools, ",") != "WebSearch" {
		t.Errorf("Output %q, Tools %v, WebTools %v", res.Output, res.Call.Tools, res.Call.WebTools)
	}
	if strings.Join(activity, ",") != "WebSearch go 1.26 release notes" || !strings.Contains(live.String(), "→ WebSearch go 1.26 release notes\n") {
		t.Errorf("activity %q, live %q", activity, live.String())
	}
	golden(t, "web", f.bodies[0])
}

func TestWeb_WithoutSearch(t *testing.T) {
	f := newFake(t, answer{stream: completed(0, "From what I know.")})
	c := New(f.srv.URL, false, nil)
	c.SetKey(testKey)
	res, err := c.Execute(context.Background(), webRequest())
	if err != nil {
		t.Fatal(err)
	}
	if res.Call.WebTools != nil {
		t.Errorf("WebTools = %v", res.Call.WebTools)
	}
	golden(t, "web-off", f.bodies[0])
}

// No call gets both repository access and the network (SPEC §5.1): only web carries web_search.
func TestWeb_OnlyWebCallsSearch(t *testing.T) {
	for _, a := range []domain.Authority{domain.AuthorityNone, domain.AuthorityRead, domain.AuthorityWeb} {
		f := newFake(t, answer{stream: completed(0, "ok")})
		req := readRequest(t)
		req.Authority = a
		if _, err := client(f, "", nil).Execute(context.Background(), req); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		if got := bytes.Contains(f.bodies[0], []byte(`"web_search"`)); got != (a == domain.AuthorityWeb) {
			t.Errorf("%s: carries web_search = %v", a, got)
		}
		if a == domain.AuthorityWeb && bytes.Contains(f.bodies[0], []byte(`"read_file"`)) {
			t.Errorf("a web call carries a repository tool")
		}
	}
}
