package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The test binary is its own fake server: run with FAKE_MCP_SERVER set, it speaks MCP on stdio.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_MCP_SERVER"); mode != "" {
		fakeServer(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeServer(mode string) {
	if mode == "exit" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	cwd, _ := os.Getwd()
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Cursor    string          `json:"cursor"`
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue // a notification
		}
		answer := func(result any) { _ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}) }
		switch req.Method {
		case "initialize":
			if mode == "silent" {
				continue
			}
			answer(map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake"}})
		case "tools/list":
			if req.Params.Cursor == "" {
				answer(map[string]any{"tools": []any{
					map[string]any{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}},
					map[string]any{"name": "bad.name"},
				}, "nextCursor": "2"})
			} else {
				answer(map[string]any{"tools": []any{map[string]any{"name": "fail"}, map[string]any{"name": "where"}, map[string]any{"name": "hidden"}}})
			}
		case "tools/call":
			// A server may ask the client something first; the client must answer, not hang.
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "id": "srv-1", "method": "roots/list"})
			_ = out.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"level": "info"}})
			sc.Scan() // the client's answer to roots/list
			switch req.Params.Name {
			case "echo":
				answer(map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo " + string(req.Params.Arguments)}, map[string]any{"type": "image", "data": "x"}}})
			case "fail":
				answer(map[string]any{"content": []any{map[string]any{"type": "text", "text": "it failed"}}, "isError": true})
			case "where":
				answer(map[string]any{"content": []any{map[string]any{"type": "text", "text": cwd + " " + os.Getenv("FAKE_SERVER_ENV") + " " + os.Getenv("SECRET_PARENT")}}})
			}
		}
	}
}

func doc(t *testing.T, servers map[string]map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func self(mode string, extra map[string]string) map[string]any {
	env := map[string]string{"FAKE_MCP_SERVER": mode}
	for k, v := range extra {
		env[k] = v
	}
	return map[string]any{"command": os.Args[0], "args": []string{"-test.run=^$"}, "env": env}
}

func TestSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SECRET_PARENT", "leaked")
	cfg := doc(t, map[string]map[string]any{
		"fake":   self("ok", map[string]string{"FAKE_SERVER_ENV": "own"}),
		"gone":   self("exit", nil),
		"remote": {"url": "https://mcp.example/x", "type": "http"},
		"nobin":  {"command": "/nonexistent/mcp-server"},
	})
	s := Start(context.Background(), cfg, []string{"mcp__fake__echo", "mcp__fake__fail", "mcp__fake__where", "mcp__gone"}, dir, []string{"PATH=" + os.Getenv("PATH")})
	defer s.Close()
	if got := strings.Join(s.Failed, ","); got != "gone,nobin,remote" {
		t.Errorf("Failed = %s", got)
	}
	var names []string
	for _, tool := range s.Tools {
		names = append(names, tool.Name)
	}
	// Both pages listed; bad.name is not a valid tool name, hidden is not allowlisted.
	if got := strings.Join(names, ","); got != "mcp__fake__echo,mcp__fake__fail,mcp__fake__where" {
		t.Errorf("Tools = %s", got)
	}
	ctx := context.Background()
	if out, isErr := s.Call(ctx, "mcp__fake__echo", json.RawMessage(`{"a":1}`)); isErr || out != "echo {\"a\":1}\n[image content omitted]" {
		t.Errorf("echo: %q, %v", out, isErr)
	}
	if out, isErr := s.Call(ctx, "mcp__fake__fail", nil); !isErr || out != "it failed" {
		t.Errorf("fail: %q, %v", out, isErr)
	}
	// The server runs in the working directory with the caller's environment and its own, and
	// nothing the caller did not pass.
	if out, _ := s.Call(ctx, "mcp__fake__where", nil); out != dir+" own " {
		t.Errorf("where: %q", out)
	}
	if out, isErr := s.Call(ctx, "mcp__fake__hidden", nil); !isErr || !strings.Contains(out, "there is no tool") {
		t.Errorf("a tool not allowlisted was called: %q", out)
	}
}

func TestAllowWholeServer(t *testing.T) {
	s := Start(context.Background(), doc(t, map[string]map[string]any{"fake": self("ok", nil)}), []string{"mcp__fake"}, t.TempDir(), nil)
	defer s.Close()
	if len(s.Tools) != 4 || !s.Has("mcp__fake__hidden") {
		t.Errorf("Tools = %v", s.Tools)
	}
	other := Start(context.Background(), doc(t, map[string]map[string]any{"fake": self("ok", nil)}), []string{"mcp__fak"}, t.TempDir(), nil)
	defer other.Close()
	if len(other.Tools) != 0 {
		t.Errorf("a prefix of a server name allowed its tools: %v", other.Tools)
	}
}

func TestSilentServerFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	s := Start(ctx, doc(t, map[string]map[string]any{"mute": self("silent", nil)}), []string{"mcp__mute"}, t.TempDir(), nil)
	defer s.Close()
	if fmt.Sprint(s.Failed) != "[mute]" {
		t.Errorf("Failed = %v", s.Failed)
	}
}

func TestInvalidDocument(t *testing.T) {
	if s := Start(context.Background(), "{", nil, t.TempDir(), nil); fmt.Sprint(s.Failed) != "[mcp-config]" {
		t.Errorf("Failed = %v", s.Failed)
	}
	if s := Start(context.Background(), "", nil, t.TempDir(), nil); s.Failed != nil || s.Tools != nil {
		t.Errorf("an empty document: %+v", s)
	}
}
