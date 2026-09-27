package execution

import (
	"os"
	"strings"
)

// parentSessionVars are the variables through which an agent session that launched the
// orchestrator (for example a Claude Code session) reaches its own child processes: a
// live, authenticated IPC channel and the markers that identify the session. No provider
// CLI and no test command - which runs code a model wrote - may inherit them
// (specs/permissions/SPEC.md §7.1). The names are exact, never prefixes: what a prefix
// admits depends on the environment it meets, so a prefix list cannot be audited by
// reading it.
var parentSessionVars = map[string]bool{
	"CLAUDECODE":                   true,
	"CLAUDE_CODE_MESSAGING_SOCKET": true,
	"CLAUDE_CODE_MESSAGING_TOKEN":  true,
	"CLAUDE_CODE_SESSION_ID":       true,
	"CLAUDE_CODE_CHILD_SESSION":    true,
	"CLAUDE_PID":                   true,
}

// ScrubbedEnv returns the orchestrator's environment without the parent-session
// variables. Everything else - PATH, HOME, auth variables - is inherited: no provider
// authority includes a shell, so a model cannot read the environment, and an allowlist
// would mostly be a way to break auth (specs/permissions/DECISIONS.md D7).
func ScrubbedEnv() []string {
	return scrub(os.Environ())
}

func scrub(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !parentSessionVars[name] {
			out = append(out, kv)
		}
	}
	return out
}
