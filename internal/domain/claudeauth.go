package domain

import (
	"fmt"
	"slices"
	"strings"
)

// How the claude CLI is authenticated (specs/claude-auth/SPEC.md §4). The values are those of
// providers.claude.auth and --claude-auth, except that a resolved Mode is only api or login.
const (
	ClaudeAuthAuto  = "auto"
	ClaudeAuthAPI   = "api"
	ClaudeAuthLogin = "login"
)

// Who decided the requested mode.
const (
	ClaudeAuthByFlag    = "flag"
	ClaudeAuthByConfig  = "config"
	ClaudeAuthByDefault = "default"
)

// claudeAPIModeRemoved are the variables that outrank or replace ANTHROPIC_API_KEY in the
// claude CLI's precedence (specs/claude-auth/SPEC.md §2, §7). Api mode removes them from the
// child's environment, and providers.claude.api_env may not set them (§17.3).
var claudeAPIModeRemoved = []string{
	"ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_PROFILE",
	"ANTHROPIC_FEDERATION_RULE_ID",
}

// ClaudeAPIModeRemoved returns the variables api mode removes from claude's environment, in
// a fixed order.
func ClaudeAPIModeRemoved() []string { return slices.Clone(claudeAPIModeRemoved) }

// ClaudeAuth is the resolved credential mode of one process. It never holds the key, its length
// or a hash of it: KeyEnv is the variable's name.
type ClaudeAuth struct {
	// Requested is auto, api or login: the flag, else the config, else auto.
	Requested string `json:"requested"`
	// Mode is the effective mode: api or login.
	Mode string `json:"mode"`
	// DecidedBy is flag, config or default.
	DecidedBy string `json:"decided_by"`
	// KeyEnv is the variable that holds (or would hold) the API key.
	KeyEnv string `json:"key_env"`
	// APIEnv names the providers.claude.api_env variables (SPEC §17.5), sorted: those set for
	// claude in api mode, or those removed from its environment in login mode because they
	// were set. Names only, never the values.
	APIEnv []string `json:"api_env,omitempty"`

	// KeyBlank is set when auto fell back to login because the variable was set but empty.
	KeyBlank bool `json:"-"`
}

// Validate checks a recorded ClaudeAuth: requested in auto, api, login; mode api or login;
// decided_by one of the three; key_env free of whitespace and control characters; api_env
// only variable names, so no value can be recorded there.
func (a ClaudeAuth) Validate() error {
	switch a.Requested {
	case ClaudeAuthAuto, ClaudeAuthAPI, ClaudeAuthLogin:
	default:
		return fmt.Errorf("requested %q is not auto, api or login", a.Requested)
	}
	if a.Mode != ClaudeAuthAPI && a.Mode != ClaudeAuthLogin {
		return fmt.Errorf("mode %q is not api or login", a.Mode)
	}
	switch a.DecidedBy {
	case ClaudeAuthByFlag, ClaudeAuthByConfig, ClaudeAuthByDefault:
	default:
		return fmt.Errorf("decided_by %q is not flag, config or default", a.DecidedBy)
	}
	for _, r := range a.KeyEnv {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("key_env %q has a control or space character", a.KeyEnv)
		}
	}
	for i, name := range a.APIEnv {
		if !IsEnvName(name) {
			return fmt.Errorf("api_env[%d] is not an environment variable name", i)
		}
	}
	return nil
}

// IsEnvName reports whether name is a portable environment variable name: a letter or _,
// then letters, digits and _.
func IsEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// ClaudeModelVar reports whether the claude CLI reads name as a model or a model alias
// (ANTHROPIC_MODEL, ANTHROPIC_SMALL_FAST_MODEL, ANTHROPIC_CUSTOM_MODEL_OPTION,
// ANTHROPIC_DEFAULT_*_MODEL): set in providers.claude.api_env, it changes the model a route
// reaches (specs/claude-auth/SPEC.md §17.3).
func ClaudeModelVar(name string) bool {
	switch name {
	case "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_CUSTOM_MODEL_OPTION":
		return true
	}
	return len(name) > len("ANTHROPIC_DEFAULT__MODEL") &&
		strings.HasPrefix(name, "ANTHROPIC_DEFAULT_") && strings.HasSuffix(name, "_MODEL")
}

// Line is the console line that says which credential the run uses.
func (a ClaudeAuth) Line() string {
	auto := a.Requested == ClaudeAuthAuto
	switch {
	case a.Mode == ClaudeAuthAPI && auto:
		return fmt.Sprintf("claude auth: api key ($%s, auto)", a.KeyEnv)
	case a.Mode == ClaudeAuthAPI:
		return fmt.Sprintf("claude auth: api key ($%s)", a.KeyEnv)
	case auto && a.KeyBlank:
		return fmt.Sprintf("claude auth: CLI login (auto: $%s is empty)", a.KeyEnv)
	case auto:
		return fmt.Sprintf("claude auth: CLI login (auto: $%s not set)", a.KeyEnv)
	default:
		return "claude auth: CLI login"
	}
}
