package openai

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

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/tools"
)

// call is a function_call item of a response; a string is a message's text.
type call struct{ id, name, args string }

// completed renders one streamed response with a reasoning item (its encrypted content) and the
// given items, ending in response.completed.
func completed(n int, items ...any) string {
	output := []any{map[string]any{"type": "reasoning", "id": fmt.Sprintf("rs_%d", n), "summary": []any{}, "encrypted_content": fmt.Sprintf("enc-%d", n)}}
	var deltas strings.Builder
	for _, it := range items {
		switch v := it.(type) {
		case string:
			d, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": v})
			fmt.Fprintf(&deltas, "event: response.output_text.delta\ndata: %s\n\n", d)
			output = append(output, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": v}}})
		case call:
			output = append(output, map[string]any{"type": "function_call", "id": "fc_" + v.id, "call_id": v.id, "name": v.name, "arguments": v.args, "status": "completed"})
		}
	}
	done, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{
		"id": fmt.Sprintf("resp_%d", n), "model": "gpt-5.5", "status": "completed", "output": output,
		"usage": map[string]any{"input_tokens": 1000, "input_tokens_details": map[string]any{"cached_tokens": 800 * min(n, 1)}, "output_tokens": 50, "output_tokens_details": map[string]any{"reasoning_tokens": 30}},
	}})
	return deltas.String() + fmt.Sprintf("event: response.completed\ndata: %s\n\n", done)
}

func readRequest(t *testing.T) provider.Request {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{"a.go": "package a\n\nfunc Add() {}\n", ".env": "SECRET=1\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return provider.Request{Prompt: "ACTION: investigate\nWhere is Add?", Model: "gpt-5.5", Authority: domain.AuthorityRead,
		WorkingDir: dir, DenyRead: []string{".orchestrator/runs"}}
}

func TestLoop_ReadTools(t *testing.T) {
	f := newFake(t,
		answer{stream: completed(0, "Looking.", call{"c1", "read_file", `{"path":"a.go","offset":null,"limit":null}`}, call{"c2", "read_file", `{"path":".env","offset":null,"limit":null}`})},
		answer{stream: completed(1, call{"c3", "grep", `{"pattern":"^func","path":null,"glob":null}`}, call{"c4", "nope", `{}`})},
		answer{stream: completed(2, "Add is in a.go:3.")},
	)
	var activity []string
	req := readRequest(t)
	req.Activity = func(s string) { activity = append(activity, s) }
	res, err := client(f, "", nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "Looking.Add is in a.go:3." {
		t.Errorf("Output = %q", res.Output)
	}
	if u := res.Usage; u.Turns != 3 || u.CacheReadTokens != 1600 || u.InputTokens != 1400 || u.ReasoningTokens != 90 || u.OutputTokens != 60 {
		t.Errorf("Usage = %+v", u)
	}
	if c := res.Call.Tools; c["Read"] != 2 || c["Grep"] != 1 || c["nope"] != 1 {
		t.Errorf("Tools = %v", c)
	}
	if want := "Writing,Read a.go,Read .env,Grep ^func,nope,Writing"; strings.Join(activity, ",") != want {
		t.Errorf("activity = %q", strings.Join(activity, ","))
	}

	type body struct {
		Instructions      string            `json:"instructions"`
		Tools             json.RawMessage   `json:"tools"`
		Input             []json.RawMessage `json:"input"`
		Include           []string          `json:"include"`
		PromptCacheKey    string            `json:"prompt_cache_key"`
		ParallelToolCalls bool              `json:"parallel_tool_calls"`
		Store             bool              `json:"store"`
	}
	var bodies []body
	for _, raw := range f.bodies {
		var b body
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	for i, b := range bodies {
		if b.Instructions != bodies[0].Instructions || !bytes.Equal(b.Tools, bodies[0].Tools) || b.PromptCacheKey != bodies[0].PromptCacheKey || b.PromptCacheKey == "" {
			t.Errorf("request %d: instructions, tools or the cache key changed", i)
		}
		if b.Store || !b.ParallelToolCalls || strings.Join(b.Include, ",") != "reasoning.encrypted_content" {
			t.Errorf("request %d: store %v, parallel %v, include %v", i, b.Store, b.ParallelToolCalls, b.Include)
		}
		// Each request starts with the whole previous one: the cache prefix.
		if i > 0 {
			for j, item := range bodies[i-1].Input {
				if !bytes.Equal(item, b.Input[j]) {
					t.Errorf("request %d changed input[%d] of request %d", i, j, i-1)
				}
			}
		}
	}
	// The second request: the prompt, the first response's items unchanged, then the two outputs.
	in := bodies[1].Input
	if len(in) != 1+4+2 {
		t.Fatalf("second request has %d input items", len(in))
	}
	var reasoning map[string]any
	_ = json.Unmarshal(in[1], &reasoning)
	if reasoning["type"] != "reasoning" || reasoning["encrypted_content"] != "enc-0" {
		t.Errorf("the reasoning item did not go back: %s", in[1])
	}
	var out1, out2 functionCallOutput
	_ = json.Unmarshal(in[5], &out1)
	_ = json.Unmarshal(in[6], &out2)
	if out1.CallID != "c1" || !strings.Contains(out1.Output, "func Add() {}") || out2.CallID != "c2" || !strings.HasPrefix(out2.Output, "error: ") || strings.Contains(out2.Output, "SECRET") {
		t.Errorf("outputs = %+v, %+v", out1, out2)
	}
	if tools := string(bodies[0].Tools); !strings.Contains(tools, `"strict":true`) || !strings.Contains(tools, `"type":"function"`) {
		t.Errorf("tools = %s", tools)
	}
	golden(t, "read-turn-3", f.bodies[2])
}

func TestLoop_TurnCap(t *testing.T) {
	f := newFake(t, answer{stream: completed(0, call{"c", "glob", `{"pattern":"*.go"}`})})
	_, err := client(f, "", nil).Execute(context.Background(), readRequest(t))
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || !strings.Contains(pe.Detail, "reached 500 model turns") || f.calls.Load() != maxTurns {
		t.Fatalf("calls %d, err %v", f.calls.Load(), err)
	}
}

func TestLoop_FunctionCallWithoutTools(t *testing.T) {
	f := newFake(t, answer{stream: completed(0, call{"c", "read_file", `{}`})})
	_, err := client(f, "", nil).Execute(context.Background(), noneRequest())
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || !strings.Contains(pe.Detail, "did not offer") {
		t.Fatalf("err %v", err)
	}
}

func TestLoop_MCPServersReported(t *testing.T) {
	f := newFake(t, answer{stream: completed(0, "ok")})
	req := readRequest(t)
	req.Extensions.MCPConfig = `{"mcpServers": {"graphify": {"command": "y"}}}`
	res, err := client(f, "", nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Call.MCPFailed, ",") != "graphify" {
		t.Errorf("MCPFailed = %v", res.Call.MCPFailed)
	}
}

// A call without tools sends neither the reasoning replay nor a cache key (one turn).
func TestLoop_NoneHasNoLoopFields(t *testing.T) {
	f := newFake(t, answer{stream: completed(0, "ok")})
	if _, err := client(f, "", nil).Execute(context.Background(), noneRequest()); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"include", "prompt_cache_key", "parallel_tool_calls", "tools"} {
		if bytes.Contains(f.bodies[0], []byte(`"`+key+`"`)) {
			t.Errorf("a call without tools sends %s", key)
		}
	}
}

func TestLoop_ApplyPatch(t *testing.T) {
	patch := func(id, op, path, diff string) map[string]any {
		return map[string]any{"type": "apply_patch_call", "id": "apc_" + id, "call_id": id, "status": "completed",
			"operation": map[string]any{"type": op, "path": path, "diff": diff}}
	}
	first := map[string]any{"type": "response.completed", "response": map[string]any{
		"id": "r1", "model": "gpt-5.5", "status": "completed",
		"output": []any{
			patch("p1", "update_file", "a.go", "-func Add() {}\n+func Add() int { return 1 }\n"),
			patch("p2", "create_file", ".git/hooks/pre-commit", "+#!/bin/sh\n+exit 0"),
			patch("p3", "create_file", "b.go", "+package a"),
		},
		"usage": map[string]any{"input_tokens": 100, "output_tokens": 10},
	}}
	raw, _ := json.Marshal(first)
	f := newFake(t, answer{stream: fmt.Sprintf("event: response.completed\ndata: %s\n\n", raw)}, answer{stream: completed(1, "done")})
	req := readRequest(t)
	req.Authority = domain.AuthorityEdit
	res, err := client(f, "", nil).Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(req.WorkingDir, "a.go")); string(got) != "package a\n\nfunc Add() int { return 1 }\n" {
		t.Errorf("a.go = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(req.WorkingDir, "b.go")); string(got) != "package a\n" {
		t.Errorf("b.go = %q", got)
	}
	if _, err := os.Stat(filepath.Join(req.WorkingDir, ".git/hooks/pre-commit")); err == nil {
		t.Error("apply_patch wrote a git hook")
	}
	if c := res.Call.Tools; c["Edit"] != 1 || c["Write"] != 2 {
		t.Errorf("Tools = %v", c)
	}
	var second struct {
		Input []map[string]any `json:"input"`
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(f.bodies[1], &second); err != nil {
		t.Fatal(err)
	}
	outs := second.Input[len(second.Input)-3:]
	for i, want := range []string{"completed", "failed", "completed"} {
		if outs[i]["type"] != "apply_patch_call_output" || outs[i]["status"] != want {
			t.Errorf("output %d = %v, want %s", i, outs[i], want)
		}
	}
	if !strings.Contains(outs[1]["output"].(string), ".git is not accessible") {
		t.Errorf("the failed output = %v", outs[1])
	}
	if !bytes.Contains(f.bodies[0], []byte(`{"type":"apply_patch"}`)) || !bytes.Contains(f.bodies[0], []byte(`"read_file"`)) {
		t.Errorf("edit tools = %s", f.bodies[0])
	}
	golden(t, "edit", f.bodies[0])
}

// The skill tool is a strict function tool of a read or edit call with user_settings, and of no
// other call (SPEC §5.4).
func TestSkillToolOffered(t *testing.T) {
	skills := []tools.Skill{{Name: "graphify", Description: "a code graph", Dir: t.TempDir()}}
	for _, tc := range []struct {
		a    domain.Authority
		us   bool
		want bool
	}{{domain.AuthorityRead, true, true}, {domain.AuthorityEdit, true, true}, {domain.AuthorityRead, false, false}, {domain.AuthorityWeb, true, false}} {
		f := newFake(t, answer{stream: completed(0, "ok")})
		c := client(f, "", nil)
		c.SetSkills(skills)
		req := readRequest(t)
		req.Authority, req.Extensions.UserSettings = tc.a, tc.us
		if _, err := c.Execute(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		got := bytes.Contains(f.bodies[0], []byte(`"name":"skill"`))
		if got != tc.want {
			t.Errorf("%s user_settings %v: skill tool %v", tc.a, tc.us, got)
		}
		if got && !bytes.Contains(f.bodies[0], []byte(`"enum":["graphify"]`)) {
			t.Errorf("the skill tool has no enum: %s", f.bodies[0])
		}
	}
}

// Off the first-party endpoint apply_patch is a strict function tool (a gateway to Bedrock drops the
// built-in one, AT13), run by the same ApplyPatch and answered with a function_call_output.
func TestLoop_ApplyPatchAsFunction(t *testing.T) {
	if !New("", true, nil).nativePatch || New("https://gateway.example/", true, nil).nativePatch {
		t.Fatal("nativePatch must be on for the first-party endpoint only")
	}
	f := newFake(t,
		answer{stream: completed(0,
			call{id: "p1", name: "apply_patch", args: `{"type":"update_file","path":"a.go","diff":"-func Add() {}\n+func Add() int { return 1 }\n"}`},
			call{id: "p2", name: "apply_patch", args: `{"type":"create_file","path":".git/hooks/pre-commit","diff":"+exit 0"}`})},
		answer{stream: completed(1, "done")})
	req := readRequest(t)
	req.Authority = domain.AuthorityEdit
	c := client(f, "", nil)
	c.nativePatch = false
	res, err := c.Execute(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(req.WorkingDir, "a.go")); string(got) != "package a\n\nfunc Add() int { return 1 }\n" {
		t.Errorf("a.go = %q", got)
	}
	if _, err := os.Stat(filepath.Join(req.WorkingDir, ".git/hooks/pre-commit")); err == nil {
		t.Error("apply_patch wrote a git hook")
	}
	if c := res.Call.Tools; c["Edit"] != 1 || c["Write"] != 1 {
		t.Errorf("Tools = %v", c)
	}
	if bytes.Contains(f.bodies[0], []byte(`{"type":"apply_patch"}`)) || !bytes.Contains(f.bodies[0], []byte(`"name":"apply_patch"`)) {
		t.Errorf("edit tools = %s", f.bodies[0])
	}
	var second struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(f.bodies[1], &second); err != nil {
		t.Fatal(err)
	}
	outs := second.Input[len(second.Input)-2:]
	if outs[0]["type"] != "function_call_output" || strings.HasPrefix(outs[0]["output"].(string), "error:") {
		t.Errorf("the update's output = %v", outs[0])
	}
	if !strings.HasPrefix(outs[1]["output"].(string), "error:") || !strings.Contains(outs[1]["output"].(string), ".git is not accessible") {
		t.Errorf("the hook's output = %v", outs[1])
	}
}
