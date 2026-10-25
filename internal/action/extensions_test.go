package action

import (
	"encoding/json"
	"strings"
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

func TestExtensionsFor(t *testing.T) {
	cfg := config.ExtensionsConfig{MCP: map[string]config.MCPServerConfig{
		"graphify": {Command: "graphify-mcp", Args: []string{"{repo}/graphify-out/graph.json"}, Tools: []string{"query_graph", "get_node"}},
		"memory":   {URL: "https://mem.example/mcp", Tools: []string{"*"}, Actions: []string{"implement"}},
	}, UserSettings: true, UserSettingsActions: []string{"implement"}}

	analyze := extensionsFor(cfg, domain.ActionAnalyze, "/work/repo", "/work/repo")
	if analyze.UserSettings {
		t.Error("user settings were opted in for implement only")
	}
	if got := strings.Join(analyze.MCPTools, ","); got != "mcp__graphify__get_node,mcp__graphify__query_graph" {
		t.Errorf("analyze tools = %s", got)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(analyze.MCPConfig), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.MCPServers) != 1 || doc.MCPServers["graphify"].Args[0] != "/work/repo/graphify-out/graph.json" {
		t.Errorf("analyze mcp config = %s", analyze.MCPConfig)
	}

	impl := extensionsFor(cfg, domain.ActionImplement, "/work/repo", "/work/repo")
	if !impl.UserSettings || strings.Join(impl.MCPTools, ",") != "mcp__memory" || !strings.Contains(impl.MCPConfig, `"type":"http"`) {
		t.Errorf("implement = %+v", impl)
	}
	// In a worktree run, {source} is the operator's repository, where an ignored graph lies.
	cfg.MCP["graphify"] = config.MCPServerConfig{Command: "graphify-mcp", Args: []string{"{source}/graphify-out/graph.json", "{repo}"}, Tools: []string{"query_graph"}}
	wt := extensionsFor(cfg, domain.ActionAnalyze, "/state/worktrees/x/run", "/work/repo")
	if !strings.Contains(wt.MCPConfig, `"args":["/work/repo/graphify-out/graph.json","/state/worktrees/x/run"]`) {
		t.Errorf("worktree mcp config = %s", wt.MCPConfig)
	}
	if research := extensionsFor(cfg, domain.ActionResearch, "/r", "/r"); research.MCPConfig != "" || len(research.MCPTools) != 0 || research.UserSettings {
		t.Errorf("research must get nothing: %+v", research)
	}
	if got := withExtensionsHint("ACTION: analyze\nbody", extensionsFor(config.ExtensionsConfig{}, domain.ActionAnalyze, "/r", "/r")); got != "ACTION: analyze\nbody" {
		t.Errorf("no extensions, no hint: %q", got)
	}
	// The hint comes right after the first line, which stays "ACTION: <name>".
	got := withExtensionsHint("ACTION: analyze\nbody", analyze)
	if !strings.HasPrefix(got, "ACTION: analyze\n\nExtra tools are available in this step: mcp__graphify__get_node, mcp__graphify__query_graph. ") || !strings.HasSuffix(got, "\nbody") {
		t.Errorf("hint = %q", got)
	}
}

type mcpDoc struct {
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
}

func parseMCPDoc(t *testing.T, s string) mcpDoc {
	t.Helper()
	var doc mcpDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("not an mcp config: %v\n%s", err, s)
	}
	return doc
}

func TestVerifyExtensions(t *testing.T) {
	ext := verifyExtensions("/tmp/orch-verify-1", "/usr/bin/orchestrator", []string{domain.VerifyToolRunTests})
	if want := `{"mcpServers":{"orch":{"args":["mcp-verify","--dir","/tmp/orch-verify-1"],"command":"/usr/bin/orchestrator"}}}`; ext.MCPConfig != want {
		t.Errorf("MCPConfig = %s, want %s", ext.MCPConfig, want)
	}
	if got := strings.Join(ext.MCPTools, ","); got != "mcp__orch__run_tests" {
		t.Errorf("tools = %s", got)
	}
	if ext.UserSettings {
		t.Error("the verify server does not load user settings")
	}
	ext = verifyExtensions("/d", "/x", []string{domain.VerifyToolRunTests, domain.VerifyToolRunCheck, domain.VerifyToolReplace})
	if got := strings.Join(ext.MCPTools, ","); got != "mcp__orch__run_tests,mcp__orch__run_check,mcp__orch__replace" {
		t.Errorf("tools with checks and replace = %s", got)
	}
}

func TestMergeExtensions(t *testing.T) {
	cfg := config.ExtensionsConfig{MCP: map[string]config.MCPServerConfig{
		"graphify": {Command: "graphify-mcp", Args: []string{"g.json"}, Tools: []string{"query_graph"}, Actions: []string{"implement"}},
	}, UserSettings: true}
	operator := extensionsFor(cfg, domain.ActionImplement, "/r", "/r")
	builtin := verifyExtensions("/tmp/d", "/usr/bin/orchestrator", []string{domain.VerifyToolRunTests, domain.VerifyToolRunCheck})

	t.Run("empty sides", func(t *testing.T) {
		got, err := mergeExtensions(provider.Extensions{}, provider.Extensions{})
		if err != nil || got.MCPConfig != "" || got.MCPTools != nil || got.UserSettings {
			t.Errorf("merge of nothing = %+v, %v", got, err)
		}
		got, err = mergeExtensions(operator, provider.Extensions{})
		if err != nil || len(parseMCPDoc(t, got.MCPConfig).MCPServers) != 1 || !got.UserSettings {
			t.Errorf("operator only = %+v, %v", got, err)
		}
		got, err = mergeExtensions(provider.Extensions{}, builtin)
		if err != nil || got.MCPConfig != builtin.MCPConfig || strings.Join(got.MCPTools, ",") != strings.Join(builtin.MCPTools, ",") {
			t.Errorf("builtin only = %+v, %v", got, err)
		}
	})

	t.Run("union", func(t *testing.T) {
		got, err := mergeExtensions(operator, builtin)
		if err != nil {
			t.Fatal(err)
		}
		doc := parseMCPDoc(t, got.MCPConfig)
		if len(doc.MCPServers) != 2 || doc.MCPServers["graphify"].Command != "graphify-mcp" ||
			doc.MCPServers["orch"].Args[2] != "/tmp/d" {
			t.Errorf("merged config = %s", got.MCPConfig)
		}
		if want := "mcp__graphify__query_graph,mcp__orch__run_tests,mcp__orch__run_check"; strings.Join(got.MCPTools, ",") != want {
			t.Errorf("tools = %v, want %s", got.MCPTools, want)
		}
		if !got.UserSettings {
			t.Error("UserSettings must be kept")
		}
	})

	t.Run("collision", func(t *testing.T) {
		if _, err := mergeExtensions(builtin, builtin); err == nil || !strings.Contains(err.Error(), `"orch"`) {
			t.Errorf("a server in both sides must be an error, got %v", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := mergeExtensions(provider.Extensions{MCPConfig: "{"}, builtin); err == nil {
			t.Error("an invalid document must be an error")
		}
	})
}
