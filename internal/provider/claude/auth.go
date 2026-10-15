package claude

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
)

// The credential of the CLI, per mode (specs/claude-auth/SPEC.md §7–§9). The mode is the
// process's, set once by app after it has resolved it; the adapter builds the child's
// environment from it, checks what the CLI says it used, and keeps the key out of every
// error and result.

const (
	apiKeyVar = "ANTHROPIC_API_KEY"
	// redacted is what the key becomes, as for the Jev key (decision/jev).
	redacted = "[REDACTED]"
)

// apiModeRemoved are the variables that outrank or replace ANTHROPIC_API_KEY in the CLI's
// precedence (SPEC §2). In api mode none of them reaches the child; in login mode they are
// the operator's own setup and stay.
var apiModeRemoved = []string{
	"ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_PROFILE",
	"ANTHROPIC_FEDERATION_RULE_ID",
}

// credentialAuth is the mode and key a Client was given; set is false until SetAuth.
type credentialAuth struct {
	mu   sync.RWMutex
	set  bool
	mode string
	key  string
}

// SetAuth fixes the credential mode (domain.ClaudeAuthAPI or ClaudeAuthLogin) every later
// call and version probe runs under. key is the API key and is only used in api mode. A
// Client whose auth was never set refuses to run.
func (c *Client) SetAuth(mode, key string) {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	c.auth.set, c.auth.mode, c.auth.key = true, mode, key
}

func (c *Client) snapshot() (mode, key string, err error) {
	c.auth.mu.RLock()
	defer c.auth.mu.RUnlock()
	if !c.auth.set {
		return "", "", apperr.NewConfigError("the claude credential mode was not set before the first call (specs/claude-auth/SPEC.md §3)", nil)
	}
	return c.auth.mode, c.auth.key, nil
}

// env is the child's environment for the mode: the scrubbed environment of the orchestrator
// with the table of SPEC §7 applied. removed names the variables api mode dropped that were
// set - names only, never values.
func (c *Client) env() (env, removed []string, err error) {
	mode, key, err := c.snapshot()
	if err != nil {
		return nil, nil, err
	}
	return buildEnv(execution.ScrubbedEnv(), mode, key)
}

// IgnoredEnv names the variables api mode removes from the child's environment that are
// set now, sorted; none in login mode.
func (c *Client) IgnoredEnv() []string {
	_, removed, err := c.env()
	if err != nil {
		return nil
	}
	return removed
}

func buildEnv(base []string, mode, key string) (env, removed []string, err error) {
	switch mode {
	case domain.ClaudeAuthAPI:
		if key == "" {
			return nil, nil, apperr.NewConfigError("claude auth mode api has no API key", nil)
		}
	case domain.ClaudeAuthLogin:
	default:
		return nil, nil, apperr.NewConfigError(fmt.Sprintf("unknown claude auth mode %q", mode), nil)
	}
	env = make([]string, 0, len(base)+1)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == apiKeyVar:
			// Login never passes a key on; api replaces it with its own below.
		case mode == domain.ClaudeAuthAPI && slices.Contains(apiModeRemoved, name):
			if !slices.Contains(removed, name) {
				removed = append(removed, name)
			}
		default:
			env = append(env, kv)
		}
	}
	if mode == domain.ClaudeAuthAPI {
		env = append(env, apiKeyVar+"="+key)
	}
	slices.Sort(removed)
	return env, removed, nil
}

// authFailure compares what the CLI used with the mode (SPEC §8) and what its retries said
// (§9). It returns what to tell the operator, or "" when the call is fine. source is ""
// without an init event (not stream-json): not checked.
func authFailure(mode, source, retryErr string) string {
	switch {
	case retryErr != "" && mode == domain.ClaudeAuthAPI:
		return fmt.Sprintf("claude auth (api): %s. Check the Console account, or run with --claude-auth login", retryErr)
	case retryErr != "":
		return fmt.Sprintf("claude auth (login): %s. Check the CLI login (claude /login), or run with --claude-auth api", retryErr)
	case mode == domain.ClaudeAuthAPI && source != "" && source != apiKeyVar:
		return fmt.Sprintf("claude auth (api): claude did not use the API key (it reported %q); a gateway sign-in or managed settings override it", source)
	case mode == domain.ClaudeAuthLogin && source == apiKeyVar:
		return fmt.Sprintf("claude auth (login): claude used an API key (it reported %q) although the orchestrator passed none", source)
	}
	return ""
}

// redactor returns a function that replaces the key in a string; without a key it is the
// identity.
func redactor(key string) func(string) string {
	if key == "" {
		return func(s string) string { return s }
	}
	return func(s string) string { return strings.ReplaceAll(s, key, redacted) }
}

// redactedError renders its cause with the key redacted and still unwraps to it, so
// errors.Is on a timeout or cancellation keeps working.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

func redactError(err error, redact func(string) string) error {
	if err == nil {
		return nil
	}
	if msg := err.Error(); redact(msg) != msg {
		return &redactedError{msg: redact(msg), cause: err}
	}
	return err
}

// redactWriter passes whole lines to w with the key redacted, so a live stream never shows
// it, even if the CLI echoes it. Flush writes a trailing partial line.
type redactWriter struct {
	mu     sync.Mutex
	w      io.Writer
	redact func(string) string
	buf    []byte
}

func (r *redactWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		line := string(r.buf[:i+1])
		r.buf = r.buf[i+1:]
		_, _ = r.w.Write([]byte(r.redact(line)))
	}
	return len(p), nil
}

func (r *redactWriter) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) > 0 {
		_, _ = r.w.Write([]byte(r.redact(string(r.buf))))
		r.buf = nil
	}
}
