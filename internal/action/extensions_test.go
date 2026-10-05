package action

import (
	"encoding/json"
	"strings"
	"testing"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
)

func TestExtensionsFor(t *testing.T) {
	cfg := config.ExtensionsConfig{MCP: map[string]config.MCPServerConfig{
		"graphify": {Command: "graphify-mcp", Args: []string{"{repo}/graphify-out/graph.json"}, Tools: []string{"query_graph", "get_node"}},
		"memory":   {URL: "https://mem.example/mcp", Tools: []string{"*"}, Actions: []string{"implement"}},
	}, UserSettings: true, UserSettingsActions: []string{"implement"}}

	analyze := extensionsFor(cfg, domain.ActionAnalyze, "/work/repo")
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

	impl := extensionsFor(cfg, domain.ActionImplement, "/work/repo")
	if !impl.UserSettings || strings.Join(impl.MCPTools, ",") != "mcp__memory" || !strings.Contains(impl.MCPConfig, `"type":"http"`) {
		t.Errorf("implement = %+v", impl)
	}
	if research := extensionsFor(cfg, domain.ActionResearch, "/r"); research.MCPConfig != "" || len(research.MCPTools) != 0 || research.UserSettings {
		t.Errorf("research must get nothing: %+v", research)
	}
	if extensionsHint(extensionsFor(config.ExtensionsConfig{}, domain.ActionAnalyze, "/r")) != "" {
		t.Error("no extensions, no hint")
	}
}
