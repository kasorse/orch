package execution

import (
	"os"
	"slices"
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

// testEnvBase and testEnvToolchain are the variables the test command - which runs code a
// model wrote - inherits by default (specs/hardening/SPEC.md §6.1); everything else is
// dropped unless the operator names it in test.env_pass. Unlike a provider CLI, the test
// command is a shell: whatever reaches it, the code under test can read, print or send.
// So it gets an allowlist, not ScrubbedEnv's denylist (DECISIONS.md D7 covers providers
// only). Proxies (GOPROXY, HTTP_PROXY, ...) are absent on purpose: a proxy URL can carry
// credentials. Exact names, as everywhere in this file.
var (
	testEnvBase = []string{
		"PATH", "HOME", "USER", "LOGNAME", "SHELL",
		"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "LC_COLLATE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME",
		"TERM", "TZ", "TMPDIR", "CI",
	}
	testEnvToolchain = []string{
		"GOPATH", "GOROOT", "GOFLAGS", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN", "CGO_ENABLED",
		"CARGO_HOME", "RUSTUP_HOME", "JAVA_HOME", "VIRTUAL_ENV", "PYTHONPATH", "NODE_OPTIONS", "XDG_CACHE_HOME",
	}
)

// testEnvPass is test.env_pass. Process-wide and add-only for the same reason as
// credentialVars: the test runner exists before the config is loaded.
var (
	testEnvMu   sync.RWMutex
	testEnvPass = map[string]bool{}
)

// AllowTestEnv adds exact variable names to those TestEnv passes (test.env_pass). A
// credential or parent-session variable stays removed even if named here. Empty names are
// ignored.
func AllowTestEnv(names ...string) {
	testEnvMu.Lock()
	defer testEnvMu.Unlock()
	for _, n := range names {
		if n != "" {
			testEnvPass[n] = true
		}
	}
}

// IsParentSessionVar reports whether name is one of the variables through which a parent
// agent session reaches its children, which no child process may inherit.
func IsParentSessionVar(name string) bool { return parentSessionVars[name] }

// TestEnv returns the environment of every process that runs the test command: the
// variables of the default allowlist and test.env_pass that are set, in the orchestrator's
// order, without any credential or parent-session variable.
func TestEnv() []string {
	return testEnv(os.Environ())
}

func testEnv(environ []string) []string {
	credentialMu.RLock()
	defer credentialMu.RUnlock()
	testEnvMu.RLock()
	defer testEnvMu.RUnlock()
	out := make([]string, 0, len(testEnvBase)+len(testEnvToolchain)+len(testEnvPass))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		allowed := testEnvPass[name] || slices.Contains(testEnvBase, name) || slices.Contains(testEnvToolchain, name)
		if allowed && !parentSessionVars[name] && !credentialVars[name] {
			out = append(out, kv)
		}
	}
	return out
}
