package domain

import (
	"fmt"
)

// Codex credential modes. A resolved mode is always api or login; auto is only the
// operator's selection rule.
const (
	CodexAuthAuto  = "auto"
	CodexAuthAPI   = "api"
	CodexAuthLogin = "login"
)

const (
	CodexAuthByFlag    = "flag"
	CodexAuthByConfig  = "config"
	CodexAuthByDefault = "default"
)

// CodexAuth records how the codex CLI was authenticated. It never contains the key.
type CodexAuth struct {
	Requested string `json:"requested"`
	Mode      string `json:"mode"`
	DecidedBy string `json:"decided_by"`
	KeyEnv    string `json:"key_env"`
	KeyBlank  bool   `json:"-"`
}

func (a CodexAuth) Validate() error {
	switch a.Requested {
	case CodexAuthAuto, CodexAuthAPI, CodexAuthLogin:
	default:
		return fmt.Errorf("requested %q is not auto, api or login", a.Requested)
	}
	if a.Mode != CodexAuthAPI && a.Mode != CodexAuthLogin {
		return fmt.Errorf("mode %q is not api or login", a.Mode)
	}
	switch a.DecidedBy {
	case CodexAuthByFlag, CodexAuthByConfig, CodexAuthByDefault:
	default:
		return fmt.Errorf("decided_by %q is not flag, config or default", a.DecidedBy)
	}
	if !IsEnvName(a.KeyEnv) {
		return fmt.Errorf("key_env %q is not an environment variable name", a.KeyEnv)
	}
	return nil
}

func (a CodexAuth) Line() string {
	auto := a.Requested == CodexAuthAuto
	switch {
	case a.Mode == CodexAuthAPI && auto:
		return fmt.Sprintf("codex auth: api key ($%s, auto)", a.KeyEnv)
	case a.Mode == CodexAuthAPI:
		return fmt.Sprintf("codex auth: api key ($%s)", a.KeyEnv)
	case auto && a.KeyBlank:
		return fmt.Sprintf("codex auth: CLI login (auto: $%s is empty)", a.KeyEnv)
	case auto:
		return fmt.Sprintf("codex auth: CLI login (auto: $%s not set)", a.KeyEnv)
	default:
		return "codex auth: CLI login"
	}
}

// CodexKeyProblem validates the content of a configured API key without returning it.
func CodexKeyProblem(key string) string {
	for _, r := range key {
		if r <= ' ' || r == 0x7f {
			return "contains whitespace or a control character; it is not a valid API key"
		}
	}
	return ""
}

// CodexAPIEnvRemoved are credential variables that would override the selected mode.
func CodexAPIEnvRemoved() []string {
	return []string{"CODEX_API_KEY", "OPENAI_API_KEY", "CODEX_ACCESS_TOKEN"}
}
