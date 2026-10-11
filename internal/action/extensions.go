package action

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
)

// extensionsFor resolves the operator's opt-in extensions (config.ExtensionsConfig) for
// one action: the --mcp-config document with only the servers offered to it, "{repo}"
// replaced with the repository root, the allowlisted tool names, and whether the user's
// Claude settings load. An action with nothing configured gets the zero value, so its
// argv is exactly what it was before extensions existed.
func extensionsFor(cfg config.ExtensionsConfig, action domain.ActionType, repoDir string) provider.Extensions {
	var ext provider.Extensions
	ext.UserSettings = cfg.UserSettingsFor(action)

	servers := map[string]map[string]any{}
	for _, name := range cfg.ServersFor(action) {
		s := cfg.MCP[name]
		sub := func(v string) string { return strings.ReplaceAll(v, "{repo}", repoDir) }
		entry := map[string]any{}
		if s.Command != "" {
			entry["command"] = sub(s.Command)
			args := make([]string, len(s.Args))
			for i, a := range s.Args {
				args[i] = sub(a)
			}
			entry["args"] = args
		} else {
			entry["url"] = sub(s.URL)
			entry["type"] = firstNonEmpty(s.Type, "http")
		}
		if len(s.Env) > 0 {
			entry["env"] = s.Env
		}
		servers[name] = entry
		for _, t := range s.Tools {
			if t == "*" {
				ext.MCPTools = append(ext.MCPTools, "mcp__"+name)
			} else {
				ext.MCPTools = append(ext.MCPTools, "mcp__"+name+"__"+t)
			}
		}
	}
	if len(servers) > 0 {
		data, _ := json.Marshal(map[string]any{"mcpServers": servers})
		ext.MCPConfig = string(data)
	}
	sort.Strings(ext.MCPTools)
	return ext
}

// verifyServer is the name of the orchestrator's own MCP server in the --mcp-config document,
// and so the middle part of its tool names. config rejects an operator server of this name.
const verifyServer = "orch"

// verifyExtensions is the built-in extension of an implement step with verify on: the stdio
// server `<exe> mcp-verify --dir <dir>` and the tools the model may call on it
// (specs/quality/SPEC.md §6.3). run_check is offered only when there are checks.
func verifyExtensions(dir, exe string, hasChecks bool) provider.Extensions {
	doc, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		verifyServer: map[string]any{"command": exe, "args": []string{"mcp-verify", "--dir", dir}},
	}})
	ext := provider.Extensions{
		MCPConfig: string(doc),
		MCPTools:  []string{"mcp__" + verifyServer + "__" + domain.VerifyToolRunTests},
	}
	if hasChecks {
		ext.MCPTools = append(ext.MCPTools, "mcp__"+verifyServer+"__"+domain.VerifyToolRunCheck)
	}
	return ext
}

// mergeExtensions unions two extension sets into one: the servers of both mcpServers
// documents, the tools of both (a's first), and UserSettings if either asks for it. A server
// name in both is an error, so neither can shadow the other.
func mergeExtensions(a, b provider.Extensions) (provider.Extensions, error) {
	out := provider.Extensions{UserSettings: a.UserSettings || b.UserSettings}
	servers := map[string]json.RawMessage{}
	// With one side empty the other document is passed on as it is.
	if a.MCPConfig == "" || b.MCPConfig == "" {
		out.MCPConfig = a.MCPConfig + b.MCPConfig
	} else {
		for _, cfg := range []string{a.MCPConfig, b.MCPConfig} {
			if err := addMCPServers(servers, cfg); err != nil {
				return provider.Extensions{}, err
			}
		}
	}
	if len(servers) > 0 {
		data, err := json.Marshal(map[string]any{"mcpServers": servers})
		if err != nil {
			return provider.Extensions{}, err
		}
		out.MCPConfig = string(data)
	}
	// a's tools come first, each side in its own order: extensionsFor sorts the operator's, and
	// the verify tools stay run_tests, then run_check.
	if tools := append(append([]string(nil), a.MCPTools...), b.MCPTools...); len(tools) > 0 {
		out.MCPTools = tools
	}
	return out, nil
}

// addMCPServers adds the servers of one --mcp-config document to servers; a name already
// there is an error.
func addMCPServers(servers map[string]json.RawMessage, cfg string) error {
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(cfg), &doc); err != nil {
		return fmt.Errorf("the mcp config is not valid JSON: %w", err)
	}
	for name, s := range doc.MCPServers {
		if _, dup := servers[name]; dup {
			return fmt.Errorf("the MCP server %q is configured twice", name)
		}
		servers[name] = s
	}
	return nil
}

// withExtensionsHint tells the model which extra tools this step has, so it reaches for them
// instead of defaulting to grep. It goes right after the "ACTION: <name>" first line: at the
// end of a long prompt (a review diff) models ignored it. prompt is unchanged without tools.
func withExtensionsHint(prompt string, ext provider.Extensions) string {
	if len(ext.MCPTools) == 0 {
		return prompt
	}
	hint := "Extra tools are available in this step: " + strings.Join(ext.MCPTools, ", ") + ". " +
		"Start with them: before searching with Grep or Glob, ask them about the code this step concerns " +
		"(for example a knowledge graph for structure, callers and relationships), then read the files they point to. " +
		"Use Grep and Glob for what they do not answer."
	first, rest, _ := strings.Cut(prompt, "\n")
	return first + "\n\n" + hint + "\n" + rest
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
