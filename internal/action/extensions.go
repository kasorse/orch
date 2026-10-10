package action

import (
	"encoding/json"
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
