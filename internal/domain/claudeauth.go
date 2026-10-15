package domain

import "fmt"

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

	// KeyBlank is set when auto fell back to login because the variable was set but empty.
	KeyBlank bool `json:"-"`
}

// Validate checks a recorded ClaudeAuth: requested in auto, api, login; mode api or login;
// decided_by one of the three; key_env free of whitespace and control characters.
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
	return nil
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
