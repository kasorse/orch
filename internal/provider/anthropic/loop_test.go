package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/provider/tools"
)

// stream renders one response: blocks are text strings or tool calls {id, name, input}.
type toolCall struct{ id, name, input string }

func stream(stop string, cacheRead int, blocks ...any) string {
	var b strings.Builder
	ev := func(typ, data string) { fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", typ, data) }
	ev("message_start", fmt.Sprintf(`{"type":"message_start","message":{"id":"m","model":"claude-opus-5-5","usage":{"input_tokens":100,"cache_read_input_tokens":%d,"cache_creation_input_tokens":10,"output_tokens":1}}}`, cacheRead))
	for i, blk := range blocks {
		switch v := blk.(type) {
		case string:
			text, _ := json.Marshal(v)
			ev("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"text","text":""}}`, i))
			ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"text_delta","text":%s}}`, i, text))
		case toolCall:
			partial, _ := json.Marshal(v.input)
			ev("content_block_start", fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":"tool_use","id":%q,"name":%q,"input":{}}}`, i, v.id, v.name))
			ev("content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%s}}`, i, partial))
		}
		ev("content_block_stop", fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
	}
	ev("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":20}}`, stop))
	ev("message_stop", `{"type":"message_stop"}`)
	return b.String()
}

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"a.go": "package a\n\nfunc Add() {}\n", ".env": "SECRET=1\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readRequest(t *testing.T) provider.Request {
	return provider.Request{Prompt: "ACTION: investigate\nWhere is Add?", Model: "claude-opus-5-5", Authority: domain.AuthorityRead,
		WorkingDir: repo(t), DenyRead: []string{".orchestrator/runs"}}
}

type sentBody struct {
	System   json.RawMessage `json:"system"`
	Tools    json.RawMessage `json:"tools"`
	Messages []struct {
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
	} `json:"messages"`
}

func parse(t *testing.T, body []byte) sentBody {
	t.Helper()
	var b sentBody
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoop_ReadTools(t *testing.T) {
	f := newFake(t,
		answer{stream: stream("tool_use", 0, "Looking.", toolCall{"t1", "read_file", `{"path":"a.go","offset":null,"limit":null}`}, toolCall{"t2", "read_file", `{"path":".env","offset":null,"limit":null}`})},
		answer{stream: stream("tool_use", 90, toolCall{"t3", "grep", `{"pattern":"^func","path":null,"glob":null}`}, toolCall{"t4", "no_such", `{}`})},
		answer{stream: stream("tool_use", 95, toolCall{"t5", "glob", `{"pattern":"**/*.go"}`})},
		answer{stream: stream("end_turn", 99, "Add is in a.go:3.")},
	)
	var activity []string
	req := readRequest(t)
	req.Activity = func(s string) { activity = append(activity, s) }
	res, err := client(f, nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Looking.Add is in a.go:3." {
		t.Errorf("Output = %q", res.Output)
	}
	if u := res.Usage; u.Turns != 4 || u.InputTokens != 400 || u.CacheReadTokens != 284 || u.CacheCreationTokens != 40 || u.OutputTokens != 80 {
		t.Errorf("Usage = %+v", u)
	}
	if c := res.Call.Tools; c["Read"] != 2 || c["Grep"] != 1 || c["Glob"] != 1 || c["no_such"] != 1 {
		t.Errorf("Tools = %v", c)
	}
	if want := "Writing,Read a.go,Read .env,Grep ^func,no_such,Glob **/*.go,Writing"; strings.Join(activity, ",") != want {
		t.Errorf("activity = %q, want %q", strings.Join(activity, ","), want)
	}

	bodies := make([]sentBody, len(f.bodies))
	for i, raw := range f.bodies {
		bodies[i] = parse(t, raw)
		if !bytes.Equal(bodies[i].System, bodies[0].System) || !bytes.Equal(bodies[i].Tools, bodies[0].Tools) {
			t.Errorf("request %d: system or tools changed: the cache prefix breaks", i)
		}
		// At most three breakpoints, never accumulating, all on user messages.
		marks := bytes.Count(raw, []byte(`"cache_control"`))
		if want := min(i+1, 3); marks != want {
			t.Errorf("request %d has %d cache breakpoints, want %d", i, marks, want)
		}
	}
	// The second request answers both calls of the first, in one user message.
	second := bodies[1].Messages
	if len(second) != 3 || second[1].Role != "assistant" || second[2].Role != "user" || len(second[2].Content) != 2 {
		t.Fatalf("second request messages = %+v", second)
	}
	r1, r2 := second[2].Content[0], second[2].Content[1]
	if r1["tool_use_id"] != "t1" || r1["is_error"] != nil || !strings.Contains(r1["content"].(string), "func Add() {}") {
		t.Errorf("first result = %v", r1)
	}
	if r2["tool_use_id"] != "t2" || r2["is_error"] != true || strings.Contains(r2["content"].(string), "SECRET") {
		t.Errorf("the denied read = %v", r2)
	}
	if third := bodies[2].Messages[4].Content; third[1]["tool_use_id"] != "t4" || third[1]["is_error"] != true || !strings.Contains(third[1]["content"].(string), `there is no tool "no_such"`) {
		t.Errorf("the unknown tool's result = %v", third)
	}
	if tools := string(bodies[0].Tools); !strings.Contains(tools, `"name":"glob"`) || !strings.Contains(tools, `"input_schema"`) || strings.Contains(tools, "strict") {
		t.Errorf("tools = %s", tools)
	}
	golden(t, "read-turn-4", f.bodies[3])
}

func TestLoop_TurnCap(t *testing.T) {
	f := newFake(t, answer{stream: stream("tool_use", 0, toolCall{"t", "glob", `{"pattern":"*.go"}`})})
	_, err := client(f, nil).Execute(context.Background(), readRequest(t))
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || !strings.Contains(pe.Detail, "reached 500 model turns") || f.calls.Load() != maxTurns {
		t.Fatalf("calls %d, err %v", f.calls.Load(), err)
	}
}

func TestLoop_ToolUseWithoutTools(t *testing.T) {
	f := newFake(t, answer{stream: stream("tool_use", 0, toolCall{"t", "read_file", `{}`})})
	_, err := client(f, nil).Execute(context.Background(), noneRequest())
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || !strings.Contains(pe.Detail, "did not offer") {
		t.Fatalf("err %v", err)
	}
}

// Until AT11, a configured MCP server is reported as one that did not start.
func TestLoop_MCPServersReported(t *testing.T) {
	f := newFake(t, answer{stream: stream("end_turn", 0, "ok")})
	req := readRequest(t)
	req.Extensions.MCPConfig = `{"mcpServers": {"orch": {"command": "x"}, "graphify": {"command": "y"}}}`
	res, err := client(f, nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Call.MCPFailed, ",") != "graphify,orch" {
		t.Errorf("MCPFailed = %v", res.Call.MCPFailed)
	}
}

// A call without repository tools writes no cache: one turn has nobody to read it.
func TestLoop_NoCacheWithoutTools(t *testing.T) {
	f := newFake(t, answer{stream: stream("end_turn", 0, "ok")})
	if _, err := client(f, nil).Execute(context.Background(), noneRequest()); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(f.bodies[0], []byte("cache_control")) {
		t.Errorf("a call without tools carries a cache breakpoint: %s", f.bodies[0])
	}
}

// No call gets both repository access and the network (SPEC §5.1): only web carries web tools.
func TestWebToolsOnlyOnWeb(t *testing.T) {
	for _, a := range []domain.Authority{domain.AuthorityNone, domain.AuthorityRead, domain.AuthorityWeb} {
		f := newFake(t, answer{stream: stream("end_turn", 0, "ok")})
		req := readRequest(t)
		req.Authority = a
		if _, err := client(f, nil).Execute(context.Background(), req); err != nil {
			t.Fatalf("%s: %v", a, err)
		}
		web := bytes.Contains(f.bodies[0], []byte(`"web_search"`)) || bytes.Contains(f.bodies[0], []byte(`"web_fetch"`))
		if web != (a == domain.AuthorityWeb) {
			t.Errorf("%s: carries web tools = %v", a, web)
		}
		if a == domain.AuthorityWeb && bytes.Contains(f.bodies[0], []byte(`"read_file"`)) {
			t.Errorf("a web call carries a repository tool")
		}
	}
}

func TestLoop_Edit(t *testing.T) {
	f := newFake(t,
		answer{stream: stream("tool_use", 0,
			toolCall{"e1", "str_replace_based_edit_tool", `{"command":"str_replace","path":"a.go","old_str":"func Add() {}","new_str":"func Add() int { return 1 }"}`},
			toolCall{"e2", "str_replace_based_edit_tool", `{"command":"create","path":".git/hooks/pre-commit","file_text":"#!/bin/sh\nexit 0\n"}`},
			toolCall{"e3", "str_replace_based_edit_tool", `{"command":"view","path":"a.go"}`})},
		answer{stream: stream("end_turn", 0, `{"summary_short": "Add returns 1"}`)},
	)
	req := readRequest(t)
	req.Authority = domain.AuthorityEdit
	res, err := client(f, nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(req.WorkingDir, "a.go")); !strings.Contains(string(got), "func Add() int { return 1 }") {
		t.Errorf("a.go = %q", got)
	}
	if _, err := os.Stat(filepath.Join(req.WorkingDir, ".git/hooks/pre-commit")); err == nil {
		t.Error("the editor wrote a git hook")
	}
	if c := res.Call.Tools; c["Edit"] != 1 || c["Write"] != 1 || c["Read"] != 1 {
		t.Errorf("Tools = %v", c)
	}
	second := parse(t, f.bodies[1]).Messages[2].Content
	if second[1]["is_error"] != true || !strings.Contains(second[1]["content"].(string), ".git is not accessible") {
		t.Errorf("the hook write's result = %v", second[1])
	}
	tools := string(parse(t, f.bodies[0]).Tools)
	if !strings.Contains(tools, `{"type":"text_editor_20250728","name":"str_replace_based_edit_tool"}`) || strings.Contains(tools, `"read_file"`) || !strings.Contains(tools, `"grep"`) {
		t.Errorf("edit tools = %s", tools)
	}
	golden(t, "edit", f.bodies[0])
}

// With user_settings a read call gets the skill tool and its result is the skill's text; without
// it, or on a web call, there is no skill tool (SPEC §5.4).
func TestLoop_Skill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "graphify")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("Use graphify query first.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := []tools.Skill{{Name: "graphify", Description: "a code graph", Dir: dir}}
	f := newFake(t,
		answer{stream: stream("tool_use", 0, toolCall{"s1", "skill", `{"name":"graphify","path":null}`})},
		answer{stream: stream("end_turn", 0, "done")},
	)
	c := client(f, nil)
	c.SetSkills(skills)
	req := readRequest(t)
	req.Extensions.UserSettings = true
	res, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(f.bodies[0], []byte(`"name":"skill"`)) || !bytes.Contains(f.bodies[1], []byte("Use graphify query first.")) {
		t.Errorf("the skill was not offered or not answered:\n%s\n%s", f.bodies[0], f.bodies[1])
	}
	if res.Call.Tools["Skill"] != 1 {
		t.Errorf("tools = %v", res.Call.Tools)
	}
	for _, tc := range []struct {
		a  domain.Authority
		us bool
	}{{domain.AuthorityRead, false}, {domain.AuthorityWeb, true}, {domain.AuthorityNone, true}} {
		f := newFake(t, answer{stream: stream("end_turn", 0, "ok")})
		c := client(f, nil)
		c.SetSkills(skills)
		req := readRequest(t)
		req.Authority, req.Extensions.UserSettings = tc.a, tc.us
		if _, err := c.Execute(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(f.bodies[0], []byte(`"name":"skill"`)) {
			t.Errorf("%s with user_settings %v offers the skill tool", tc.a, tc.us)
		}
	}
}
