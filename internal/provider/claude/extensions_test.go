package claude

import (
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

func flag(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// No extensions: the argv is exactly the restricted one it always was.
func TestArgs_NoExtensionsIsUnchanged(t *testing.T) {
	got := buildArgs(domain.AuthorityRead, nil, "opus", "", provider.Extensions{})
	if !slices.Contains(got, "--restricted") || slices.Contains(got, "--mcp-config") {
		t.Errorf("unexpected argv %q", got)
	}
	if v, _ := flag(got, "--setting-sources"); v != "" {
		t.Errorf("--setting-sources = %q, want empty", v)
	}
}

// MCP servers stay under --restricted; their allowlisted tools join the authority's own.
func TestArgs_MCPKeepsRestrictedAndAllowlists(t *testing.T) {
	ext := provider.Extensions{MCPConfig: `{"mcpServers":{"graphify":{}}}`, MCPTools: []string{"mcp__graphify__query_graph"}}
	got := buildArgs(domain.AuthorityEdit, nil, "opus", "", ext)
	if !slices.Contains(got, "--restricted") || !slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("must stay restricted and strict: %q", got)
	}
	if v, _ := flag(got, "--mcp-config"); v != ext.MCPConfig {
		t.Errorf("--mcp-config = %q", v)
	}
	if v, _ := flag(got, "--allowed-tools"); v != "Edit,Write,mcp__graphify__query_graph" {
		t.Errorf("--allowed-tools = %q", v)
	}
	if v, _ := flag(got, "--tools"); v != "Read,Grep,Glob,Edit,Write" {
		t.Errorf("built-in tools must not change: %q", v)
	}
	if n := len(got); got[n-2] != "--model" {
		t.Errorf("--model must stay last (the prompt is on stdin): %q", got)
	}
}

// The verify server of an implement step (specs/quality/SPEC.md §6.3): its tools join Edit
// and Write in --allowed-tools, and the built-in tool list is untouched.
func TestArgs_VerifyServer(t *testing.T) {
	const doc = `{"mcpServers":{"orch":{"args":["mcp-verify","--dir","/tmp/orch-verify-1"],"command":"/usr/bin/orchestrator"}}}`
	for _, tc := range []struct {
		name  string
		tools []string
		want  string
	}{
		{"tests only", []string{"mcp__orch__run_tests"}, "Edit,Write,mcp__orch__run_tests"},
		{"with checks", []string{"mcp__orch__run_tests", "mcp__orch__run_check"}, "Edit,Write,mcp__orch__run_tests,mcp__orch__run_check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgs(domain.AuthorityEdit, nil, "opus", "", provider.Extensions{MCPConfig: doc, MCPTools: tc.tools})
			if v, _ := flag(got, "--mcp-config"); v != doc {
				t.Errorf("--mcp-config = %q", v)
			}
			if v, _ := flag(got, "--allowed-tools"); v != tc.want {
				t.Errorf("--allowed-tools = %q, want %q", v, tc.want)
			}
			if v, _ := flag(got, "--tools"); v != "Read,Grep,Glob,Edit,Write" {
				t.Errorf("built-in tools must not change: %q", v)
			}
			if !slices.Contains(got, "--restricted") || !slices.Contains(got, "--strict-mcp-config") {
				t.Errorf("must stay restricted and strict: %q", got)
			}
		})
	}
}

// User settings drop --restricted (it ignores settings files) and load user settings
// only; the tool list, dontAsk and strict MCP stay.
func TestArgs_UserSettings(t *testing.T) {
	got := buildArgs(domain.AuthorityRead, nil, "opus", "", provider.Extensions{UserSettings: true})
	if slices.Contains(got, "--restricted") {
		t.Errorf("--restricted ignores settings files and must be dropped: %q", got)
	}
	if v, _ := flag(got, "--setting-sources"); v != "user" {
		t.Errorf("--setting-sources = %q, want user (never project/local)", v)
	}
	for _, want := range []string{"--strict-mcp-config", "dontAsk"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s: %q", want, got)
		}
	}
	if v, _ := flag(got, "--tools"); strings.Contains(v, "Bash") || v != "Read,Grep,Glob" {
		t.Errorf("--tools = %q", v)
	}
}
