package config

import (
	"strings"
	"testing"
)

func TestValidateExtensions(t *testing.T) {
	claude := map[string]string{"research": "claude", "plan": "claude", "implement": "claude", "review": "claude"}
	ok := ExtensionsConfig{MCP: map[string]MCPServerConfig{"graphify": {Command: "graphify-mcp", Tools: []string{"query_graph"}}}}
	if p := validateExtensions(ok, claude); len(p) != 0 {
		t.Errorf("valid config rejected: %v", p)
	}
	bad := ExtensionsConfig{MCP: map[string]MCPServerConfig{
		"no tools":  {Command: "x"},
		"both":      {Command: "x", URL: "http://y", Tools: []string{"t"}},
		"badaction": {Command: "x", Tools: []string{"t"}, Actions: []string{"deploy"}},
	}, UserSettingsActions: []string{"analyze"}}
	got := strings.Join(validateExtensions(bad, claude), "\n")
	for _, want := range []string{"letters, digits", "tools: list the tools", "not both", `"deploy" is not an action`, "user_settings is false",
		`the analyze action was removed`, "specs/clarify"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}
	gemini := map[string]string{"plan": "gemini", "review": "claude"}
	if p := strings.Join(validateExtensions(ok, gemini), "\n"); !strings.Contains(p, "only supported with provider claude or codex") {
		t.Errorf("a gemini action must be refused: %s", p)
	}
}
