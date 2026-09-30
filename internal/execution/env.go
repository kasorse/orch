package execution

import (
	"os"
	"strings"
	"sync"
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

// credentialVars are the variables the configuration names as the orchestrator's own
// credentials - the Jev API key, the Jira email and token (specs/jira/SPEC.md F1, §12).
// The orchestrator reads them itself; no child process needs them, and the test command,
// which runs code a model wrote and may have network access, must not be able to read
// them. Exact names, as for parentSessionVars.
//
// This is process-wide state because the names come from the configuration, but the
// provider adapters and the test runner are constructed before it is loaded (newRegistry)
// and each of them calls ScrubbedEnv at call time. The set is only ever added to, so a
// name registered once stays scrubbed for the rest of the process.
var (
	credentialMu   sync.RWMutex
	credentialVars = map[string]bool{}
)

// ScrubCredentials adds exact variable names to those ScrubbedEnv removes. Empty names
// are ignored.
func ScrubCredentials(names ...string) {
	credentialMu.Lock()
	defer credentialMu.Unlock()
	for _, n := range names {
		if n != "" {
			credentialVars[n] = true
		}
	}
}

// ScrubbedEnv returns the orchestrator's environment without the parent-session
// variables and the credential variables registered through ScrubCredentials. Everything
// else - PATH, HOME, the provider CLIs' own auth variables - is inherited: no provider
// authority includes a shell, so a model cannot read the environment, and an allowlist
// would mostly be a way to break auth (specs/permissions/DECISIONS.md D7).
func ScrubbedEnv() []string {
	return scrub(os.Environ())
}

func scrub(environ []string) []string {
	credentialMu.RLock()
	defer credentialMu.RUnlock()
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !parentSessionVars[name] && !credentialVars[name] {
			out = append(out, kv)
		}
	}
	return out
}
