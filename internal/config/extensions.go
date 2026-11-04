package config

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kasorse/orch/internal/domain"
)

// ExtensionsConfig opts provider calls into the operator's own provider extensions, which
// the orchestrator otherwise never loads (specs/permissions/SPEC.md §6.2): MCP servers such
// as a code knowledge graph or a memory store, and the operator's user-level plugins,
// skills and hooks. Everything here is off unless configured, and it never adds a shell or
// lifts the file tools' repository confinement.
//
//	extensions:
//	  mcp:
//	    graphify:
//	      command: graphify-mcp
//	      args: ["{source}/graphify-out/graph.json"]
//	      tools: [query_graph, get_node, get_neighbors, shortest_path]
//	      actions: [investigate, implement, review]
//	  user_settings: false
type ExtensionsConfig struct {
	// MCP lists MCP servers by name. Only the listed tools of a server are given to the
	// model, and only in the listed actions.
	MCP map[string]MCPServerConfig `yaml:"mcp"`
	// UserSettings loads ~/.claude/settings.json in the given actions (all model steps when
	// empty): plugins, skills and hooks such as memory writing. It requires dropping
	// claude's --restricted mode, which ignores settings files; the tool allowlist, the
	// permission mode and the file tools' working-directory confinement stay. Hooks run
	// with your user's permissions - they are your code, but not the orchestrator's.
	UserSettings        bool     `yaml:"user_settings"`
	UserSettingsActions []string `yaml:"user_settings_actions"`
}

// MCPServerConfig is one MCP server, in the shape claude's --mcp-config expects, plus the
// orchestrator's allowlist of its tools and the actions it is offered to.
type MCPServerConfig struct {
	// Command and Args start a stdio server; URL (with Type "http" or "sse") names a remote
	// one. "{repo}" in Command, Args or URL is replaced with the root the step works in (a
	// worktree run's worktree), "{source}" with the operator's repository, which keeps the
	// files git does not carry into a worktree, such as an ignored generated graph.
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	Type    string            `yaml:"type"`
	URL     string            `yaml:"url"`
	// Tools are the server's tool names the model may call, without the "mcp__<name>__"
	// prefix. "*" allows every tool the server offers.
	Tools []string `yaml:"tools"`
	// Actions are the workflow actions (research, investigate, implement, review) that
	// get this server. Empty means the read-only ones: investigate and review.
	Actions []string `yaml:"actions"`
}

// DefaultMCPActions are the actions an MCP server is offered to when none are listed:
// the steps that read the repository but do not change it.
var DefaultMCPActions = []string{"investigate", "review"}

var mcpNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// reservedMCPServer is the name of the orchestrator's own verify server; an operator's
// server cannot take it.
const reservedMCPServer = "orch"

var extensionActions = []string{"research", "investigate", "implement", "review"}

// actionNotAnAction is the reason an action name in extensions is refused, when there is more
// to say than "not an action": the analyze action was removed and plan was renamed investigate
// (specs/clarify).
func actionNotAnAction(a string) string {
	switch a {
	case "analyze":
		return "the analyze action was removed, a step that reads the code is investigate (specs/clarify); "
	case "plan":
		return "the plan action was renamed investigate (specs/clarify); "
	}
	return ""
}

// ActionsFor returns the actions s is offered to.
func (s MCPServerConfig) ActionsFor() []string {
	if len(s.Actions) == 0 {
		return DefaultMCPActions
	}
	return s.Actions
}

// ServersFor returns the names of the MCP servers offered to action, sorted.
func (e ExtensionsConfig) ServersFor(action domain.ActionType) []string {
	var names []string
	for name, s := range e.MCP {
		if slices.Contains(s.ActionsFor(), string(action)) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// UserSettingsFor reports whether action loads the user's Claude settings.
func (e ExtensionsConfig) UserSettingsFor(action domain.ActionType) bool {
	if !e.UserSettings {
		return false
	}
	return len(e.UserSettingsActions) == 0 || slices.Contains(e.UserSettingsActions, string(action))
}

// Enabled reports whether any extension is configured.
func (e ExtensionsConfig) Enabled() bool { return len(e.MCP) > 0 || e.UserSettings }

// usesAPI reports whether a provider runs over the API transport, where user_settings means the
// orchestrator's skill tool (specs/api-transport/SPEC.md §5.4) for codex too.
func validateExtensions(e ExtensionsConfig, providers map[string]string, usesAPI func(string) bool) []string {
	var problems []string
	names := make([]string, 0, len(e.MCP))
	for n := range e.MCP {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := e.MCP[name]
		key := "extensions.mcp." + name
		if !mcpNameRE.MatchString(name) {
			problems = append(problems, fmt.Sprintf("%s: the server name may use only letters, digits, - and _", key))
		}
		if name == reservedMCPServer {
			problems = append(problems, key+": the name orch is reserved for the orchestrator's verify server")
		}
		if strings.Contains(name, "__") {
			problems = append(problems, key+": the server name must not contain __ (it is ambiguous in mcp__<server>__<tool>)")
		}
		switch {
		case s.Command == "" && s.URL == "":
			problems = append(problems, key+": needs command (a stdio server) or url (a remote one)")
		case s.Command != "" && s.URL != "":
			problems = append(problems, key+": set command or url, not both")
		}
		if s.URL != "" && s.Type != "" && s.Type != "http" && s.Type != "sse" {
			problems = append(problems, fmt.Sprintf("%s.type must be http or sse, got %q", key, s.Type))
		}
		if len(s.Tools) == 0 {
			problems = append(problems, key+".tools: list the tools the model may call (or \"*\" for all); none are allowed by default")
		}
		for _, t := range s.Tools {
			if t != "*" && !mcpNameRE.MatchString(t) {
				problems = append(problems, fmt.Sprintf("%s.tools: %q is not a tool name", key, t))
			}
		}
		for _, a := range s.Actions {
			if !slices.Contains(extensionActions, a) {
				problems = append(problems, fmt.Sprintf("%s.actions: %s%q is not an action (want: %s)", key, actionNotAnAction(a), a, strings.Join(extensionActions, ", ")))
			}
		}
		for _, a := range s.ActionsFor() {
			if p := providers[a]; p != "" && p != "claude" && p != "codex" {
				problems = append(problems, fmt.Sprintf("%s: action %s runs on %s; extensions are only supported with provider claude or codex", key, a, p))
			}
		}
	}
	for _, a := range e.UserSettingsActions {
		if !slices.Contains(extensionActions, a) {
			problems = append(problems, fmt.Sprintf("extensions.user_settings_actions: %s%q is not an action (want: %s)", actionNotAnAction(a), a, strings.Join(extensionActions, ", ")))
		}
	}
	if len(e.UserSettingsActions) > 0 && !e.UserSettings {
		problems = append(problems, "extensions.user_settings_actions is set but extensions.user_settings is false")
	}
	if e.UserSettings {
		for _, a := range extensionActions {
			if p := providers[a]; p == "codex" && e.UserSettingsFor(domain.ActionType(a)) && !usesAPI(p) {
				problems = append(problems, fmt.Sprintf("extensions.user_settings: action %s runs on the codex CLI; user settings are supported only with provider claude or providers.codex.transport: api", a))
			}
		}
	}
	return problems
}
