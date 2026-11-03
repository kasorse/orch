package config

import (
	"strings"
	"testing"
)

func TestValidateExtensions(t *testing.T) {
	cli := func(string) bool { return false }
	claude := map[string]string{"research": "claude", "investigate": "claude", "implement": "claude", "review": "claude"}
	ok := ExtensionsConfig{MCP: map[string]MCPServerConfig{"graphify": {Command: "graphify-mcp", Tools: []string{"query_graph"}}}}
	if p := validateExtensions(ok, claude, cli); len(p) != 0 {
		t.Errorf("valid config rejected: %v", p)
	}
	bad := ExtensionsConfig{MCP: map[string]MCPServerConfig{
		"no tools":  {Command: "x"},
		"both":      {Command: "x", URL: "http://y", Tools: []string{"t"}},
		"badaction": {Command: "x", Tools: []string{"t"}, Actions: []string{"deploy"}},
	}, UserSettingsActions: []string{"analyze", "plan"}}
	got := strings.Join(validateExtensions(bad, claude, cli), "\n")
	for _, want := range []string{"letters, digits", "tools: list the tools", "not both", `"deploy" is not an action`, "user_settings is false",
		`the analyze action was removed`, `the plan action was renamed investigate`, "specs/clarify"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}
	gemini := map[string]string{"investigate": "gemini", "review": "claude"}
	if p := strings.Join(validateExtensions(ok, gemini, cli), "\n"); !strings.Contains(p, "only supported with provider claude or codex") {
		t.Errorf("a gemini action must be refused: %s", p)
	}
}

// The codex CLI loads no user settings; over the API transport user_settings is the skill tool
// (specs/api-transport/SPEC.md §5.4), and an action outside user_settings_actions is not asked.
func TestValidateExtensions_UserSettingsOnCodex(t *testing.T) {
	codex := map[string]string{"investigate": "codex", "implement": "claude"}
	on := ExtensionsConfig{UserSettings: true}
	if p := strings.Join(validateExtensions(on, codex, func(string) bool { return false }), "\n"); !strings.Contains(p, "action investigate runs on the codex CLI") {
		t.Errorf("user_settings on the codex CLI must be refused: %q", p)
	}
	if p := validateExtensions(on, codex, func(id string) bool { return id == "codex" }); len(p) != 0 {
		t.Errorf("user_settings on the codex API refused: %v", p)
	}
	only := ExtensionsConfig{UserSettings: true, UserSettingsActions: []string{"implement"}}
	if p := validateExtensions(only, codex, func(string) bool { return false }); len(p) != 0 {
		t.Errorf("a codex action without user_settings refused: %v", p)
	}
}
