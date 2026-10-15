// This file defines and validates the providers section (specs/claude-auth/SPEC.md §5): how the
// claude CLI is authenticated. Validate includes these checks.
package config

import (
	"fmt"
	"strings"
	"unicode"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
)

// providers.claude.auth values. The empty string means auto.
const (
	ClaudeAuthAuto  = "auto"
	ClaudeAuthAPI   = "api"
	ClaudeAuthLogin = "login"
)

// DefaultClaudeAPIKeyEnv is the variable that holds the key when api_key_env is not set.
const DefaultClaudeAPIKeyEnv = "ANTHROPIC_API_KEY"

// ProvidersConfig is the providers section. Only claude exists: the other providers support no
// authority, so any other key is refused by strict parsing.
type ProvidersConfig struct {
	Claude ClaudeProviderConfig `yaml:"claude"`
}

// ClaudeProviderConfig is providers.claude. Both keys are optional; read them through
// EffectiveAuth and KeyEnv.
type ClaudeProviderConfig struct {
	// Auth is auto (or empty, the default), api or login.
	Auth string `yaml:"auth"`
	// APIKeyEnv names the variable that holds the Console API key; never the key itself.
	APIKeyEnv string `yaml:"api_key_env"`

	// apiKeyLiteralSet records that a literal "api_key" was written, which Validate rejects:
	// the key comes from the environment, never from a file that might end up committed.
	apiKeyLiteralSet bool
}

// UnmarshalYAML captures whether a literal api_key field was present, without ever exposing
// its value as a Go field.
func (c *ClaudeProviderConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Auth      string `yaml:"auth"`
		APIKeyEnv string `yaml:"api_key_env"`
		APIKey    string `yaml:"api_key"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	c.Auth = raw.Auth
	c.APIKeyEnv = raw.APIKeyEnv
	c.apiKeyLiteralSet = raw.APIKey != ""
	return nil
}

// EffectiveAuth is providers.claude.auth, with the default auto for an absent value.
func (c ClaudeProviderConfig) EffectiveAuth() string {
	if c.Auth == "" {
		return ClaudeAuthAuto
	}
	return c.Auth
}

// KeyEnv is the name of the variable that holds the API key: api_key_env, or its default.
func (c ClaudeProviderConfig) KeyEnv() string {
	if env := strings.TrimSpace(c.APIKeyEnv); env != "" {
		return env
	}
	return DefaultClaudeAPIKeyEnv
}

// ParseClaudeAuth validates a --claude-auth value: auto, api or login.
func ParseClaudeAuth(v string) (string, error) {
	switch v {
	case ClaudeAuthAuto, ClaudeAuthAPI, ClaudeAuthLogin:
		return v, nil
	}
	return "", fmt.Errorf("--claude-auth must be auto, api or login, got %q", v)
}

// ResolveClaudeAuth turns the --claude-auth flag ("" if not given), providers.claude and the
// environment into the effective credential mode (specs/claude-auth/SPEC.md §4). lookup reads
// the environment. The notices are warnings and remarks for the console; no message ever holds
// the key's value. The key itself is read again by app, only when the mode is api.
func ResolveClaudeAuth(cfg Config, flag string, lookup func(string) (string, bool)) (domain.ClaudeAuth, []string, error) {
	c := cfg.Providers.Claude
	res := domain.ClaudeAuth{Requested: c.EffectiveAuth(), DecidedBy: domain.ClaudeAuthByDefault, KeyEnv: c.KeyEnv()}
	switch {
	case flag != "":
		res.Requested, res.DecidedBy = flag, domain.ClaudeAuthByFlag
	case c.Auth != "":
		res.DecidedBy = domain.ClaudeAuthByConfig
	}
	env := res.KeyEnv
	raw, set := lookup(env)
	key := strings.TrimSpace(raw)
	var notices []string

	if res.Requested == ClaudeAuthLogin {
		res.Mode = domain.ClaudeAuthLogin
		if set {
			notices = append(notices, fmt.Sprintf("%s is set but not used (--claude-auth login)", env))
		}
		return res, notices, nil
	}

	switch {
	case !set || key == "":
		if res.Requested == ClaudeAuthAPI {
			if !set {
				return domain.ClaudeAuth{}, nil, &apperr.ConfigError{Msg: fmt.Sprintf(
					"claude auth is api but %s is not set; export the key or use --claude-auth login", env)}
			}
			return domain.ClaudeAuth{}, nil, &apperr.ConfigError{Msg: fmt.Sprintf(
				"claude auth is api but %s is empty; export the key or use --claude-auth login", env)}
		}
		res.Mode = domain.ClaudeAuthLogin
		if set {
			res.KeyBlank = true
			notices = append(notices, fmt.Sprintf("warning: %s is set but empty; using the CLI login", env))
		}
		if _, other := lookup(DefaultClaudeAPIKeyEnv); env != DefaultClaudeAPIKeyEnv && other {
			notices = append(notices, fmt.Sprintf("%s is set but the configured key variable is %s, which has no key; "+
				"%s is not used", DefaultClaudeAPIKeyEnv, env, DefaultClaudeAPIKeyEnv))
		}
		return res, notices, nil
	}

	if problem := claudeKeyProblem(key); problem != "" {
		return domain.ClaudeAuth{}, nil, &apperr.ConfigError{Msg: fmt.Sprintf("%s %s", env, problem)}
	}
	res.Mode = domain.ClaudeAuthAPI
	return res, notices, nil
}

// claudeKeyProblem reports why a trimmed, non-empty key is unusable, without quoting it.
func claudeKeyProblem(key string) string {
	if strings.HasPrefix(key, "sk-ant-oat") {
		return "holds a subscription token; set it as CLAUDE_CODE_OAUTH_TOKEN and use --claude-auth login"
	}
	for _, r := range key {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "contains whitespace or a control character; it is not a valid API key"
		}
	}
	return ""
}

// providersProblems validates providers.claude.
func providersProblems(cfg Config) []string {
	var problems []string
	c := cfg.Providers.Claude

	switch c.Auth {
	case "", ClaudeAuthAuto, ClaudeAuthAPI, ClaudeAuthLogin:
	default:
		problems = append(problems, fmt.Sprintf("providers.claude.auth must be auto (the default), api or login, got %q", c.Auth))
	}

	switch env := c.KeyEnv(); {
	case c.APIKeyEnv != "" && !envNameRE.MatchString(c.APIKeyEnv):
		problems = append(problems, fmt.Sprintf("providers.claude.api_key_env %q must be an environment variable name (letters, digits, _)", c.APIKeyEnv))
	case execution.IsParentSessionVar(env):
		problems = append(problems, fmt.Sprintf("providers.claude.api_key_env %q belongs to the agent session that launched the orchestrator; no child process gets it", env))
	}

	if c.apiKeyLiteralSet {
		problems = append(problems, "providers.claude.api_key must not be set in the configuration file; "+
			"name an environment variable via providers.claude.api_key_env instead")
	}
	return problems
}
