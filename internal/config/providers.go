// This file defines and validates the providers section: how the Claude and Codex CLIs are
// authenticated. Validate includes these checks.
package config

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/secretscan"
)

// providers.*.auth values. The empty string means auto.
const (
	ClaudeAuthAuto  = "auto"
	ClaudeAuthAPI   = "api"
	ClaudeAuthLogin = "login"
	CodexAuthAuto   = "auto"
	CodexAuthAPI    = "api"
	CodexAuthLogin  = "login"
)

// DefaultClaudeAPIKeyEnv is the variable that holds the key when api_key_env is not set.
const DefaultClaudeAPIKeyEnv = "ANTHROPIC_API_KEY"

// DefaultCodexAPIKeyEnv is the variable operators normally use for an OpenAI API key.
// The Codex adapter maps it to CODEX_API_KEY for non-interactive codex exec calls.
const DefaultCodexAPIKeyEnv = "OPENAI_API_KEY"

// ProvidersConfig is the providers section. Gemini has no credential-specific settings yet,
// so a providers.gemini key is still rejected by strict parsing.
type ProvidersConfig struct {
	Claude ClaudeProviderConfig `yaml:"claude"`
	Codex  CodexProviderConfig  `yaml:"codex"`
}

// ClaudeProviderConfig is providers.claude. Both keys are optional; read them through
// EffectiveAuth and KeyEnv.
type ClaudeProviderConfig struct {
	// Auth is auto (or empty, the default), api or login.
	Auth string `yaml:"auth"`
	// APIKeyEnv names the variable that holds the Console API key; never the key itself.
	APIKeyEnv string `yaml:"api_key_env"`
	// APIEnv is set for the claude child in api mode and removed from its environment in login
	// mode (specs/claude-auth/SPEC.md §17); never a secret, since the file is committed.
	APIEnv map[string]string `yaml:"api_env"`
	TransportConfig

	// apiKeyLiteralSet records that a literal "api_key" was written, which Validate rejects:
	// the key comes from the environment, never from a file that might end up committed.
	apiKeyLiteralSet bool
}

// CodexProviderConfig is providers.codex. The key itself is always read from the
// environment; Codex's own login state is used in login mode.
type CodexProviderConfig struct {
	Auth      string `yaml:"auth"`
	APIKeyEnv string `yaml:"api_key_env"`
	TransportConfig

	apiKeyLiteralSet bool
}

func (c *CodexProviderConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Auth            string `yaml:"auth"`
		APIKeyEnv       string `yaml:"api_key_env"`
		APIKey          string `yaml:"api_key"`
		TransportConfig `yaml:",inline"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	c.Auth = raw.Auth
	c.APIKeyEnv = raw.APIKeyEnv
	c.TransportConfig = raw.TransportConfig
	c.apiKeyLiteralSet = raw.APIKey != ""
	return nil
}

func (c CodexProviderConfig) EffectiveAuth() string {
	if c.Auth == "" {
		return CodexAuthAuto
	}
	return c.Auth
}

func (c CodexProviderConfig) KeyEnv() string {
	if env := strings.TrimSpace(c.APIKeyEnv); env != "" {
		return env
	}
	return DefaultCodexAPIKeyEnv
}

// UnmarshalYAML captures whether a literal api_key field was present, without ever exposing
// its value as a Go field.
func (c *ClaudeProviderConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Auth            string            `yaml:"auth"`
		APIKeyEnv       string            `yaml:"api_key_env"`
		APIKey          string            `yaml:"api_key"`
		APIEnv          map[string]string `yaml:"api_env"`
		TransportConfig `yaml:",inline"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	c.Auth = raw.Auth
	c.APIKeyEnv = raw.APIKeyEnv
	c.APIEnv = raw.APIEnv
	c.TransportConfig = raw.TransportConfig
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

// ParseCodexAuth validates a --codex-auth value.
func ParseCodexAuth(v string) (string, error) {
	switch v {
	case CodexAuthAuto, CodexAuthAPI, CodexAuthLogin:
		return v, nil
	}
	return "", fmt.Errorf("--codex-auth must be auto, api or login, got %q", v)
}

// ResolveClaudeAuth turns the --claude-auth flag ("" if not given), providers.claude and the
// environment into the effective credential mode (specs/claude-auth/SPEC.md §4). lookup reads
// the environment. The notices are warnings and remarks for the console; no message ever holds
// the key's value. The key itself is read again by app, only when the mode is api.
func ResolveClaudeAuth(cfg Config, flag string, lookup func(string) (string, bool)) (domain.ClaudeAuth, []string, error) {
	res, notices, err := resolveClaudeAuth(cfg, flag, lookup)
	if err == nil && cfg.Providers.Claude.UsesAPI() && res.Mode != domain.ClaudeAuthAPI {
		return domain.ClaudeAuth{}, nil, transportAuthError("claude", res.DecidedBy, res.KeyEnv)
	}
	return res, notices, err
}

func resolveClaudeAuth(cfg Config, flag string, lookup func(string) (string, bool)) (domain.ClaudeAuth, []string, error) {
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

// ResolveCodexAuth applies the same precedence as Claude: command-line flag, config, auto.
// Auto uses the API key when the configured variable is non-empty and otherwise uses the
// Codex CLI's own login state.
func ResolveCodexAuth(cfg Config, flag string, lookup func(string) (string, bool)) (domain.CodexAuth, []string, error) {
	res, notices, err := resolveCodexAuth(cfg, flag, lookup)
	if err == nil && cfg.Providers.Codex.UsesAPI() && res.Mode != CodexAuthAPI {
		return domain.CodexAuth{}, nil, transportAuthError("codex", res.DecidedBy, res.KeyEnv)
	}
	return res, notices, err
}

func resolveCodexAuth(cfg Config, flag string, lookup func(string) (string, bool)) (domain.CodexAuth, []string, error) {
	c := cfg.Providers.Codex
	res := domain.CodexAuth{Requested: c.EffectiveAuth(), DecidedBy: domain.CodexAuthByDefault, KeyEnv: c.KeyEnv()}
	if flag != "" {
		res.Requested, res.DecidedBy = flag, domain.CodexAuthByFlag
	} else if c.Auth != "" {
		res.DecidedBy = domain.CodexAuthByConfig
	}

	raw, set := lookup(res.KeyEnv)
	key := strings.TrimSpace(raw)
	var notices []string
	if res.Requested == CodexAuthLogin {
		res.Mode = CodexAuthLogin
		if set {
			notices = append(notices, fmt.Sprintf("%s is set but not used (--codex-auth login)", res.KeyEnv))
		}
		return res, notices, nil
	}
	if !set || key == "" {
		if res.Requested == CodexAuthAPI {
			if !set {
				return domain.CodexAuth{}, nil, &apperr.ConfigError{Msg: fmt.Sprintf(
					"codex auth is api but %s is not set; export the key or use --codex-auth login", res.KeyEnv)}
			}
			return domain.CodexAuth{}, nil, &apperr.ConfigError{Msg: fmt.Sprintf(
				"codex auth is api but %s is empty; export the key or use --codex-auth login", res.KeyEnv)}
		}
		res.Mode = CodexAuthLogin
		if set {
			res.KeyBlank = true
			notices = append(notices, fmt.Sprintf("warning: %s is set but empty; using the Codex CLI login", res.KeyEnv))
		}
		return res, notices, nil
	}
	if problem := domain.CodexKeyProblem(key); problem != "" {
		return domain.CodexAuth{}, nil, &apperr.ConfigError{Msg: fmt.Sprintf("%s %s", res.KeyEnv, problem)}
	}
	res.Mode = CodexAuthAPI
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

// providersProblems validates providers.claude and providers.codex.
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

	problems = append(problems, transportProblems(cfg, "claude", c.TransportConfig)...)
	if c.UsesAPI() && len(c.APIEnv) > 0 {
		problems = append(problems, "providers.claude.api_env only configures the claude CLI; with transport: api "+
			"name the endpoint in providers.claude.base_url and the models by their full IDs in the routes")
	}
	if c.apiKeyLiteralSet {
		problems = append(problems, "providers.claude.api_key must not be set in the configuration file; "+
			"name an environment variable via providers.claude.api_key_env instead")
	}
	problems = append(problems, apiEnvProblems(cfg)...)
	codex := cfg.Providers.Codex
	switch env := codex.KeyEnv(); {
	case codex.Auth != "" && codex.Auth != CodexAuthAuto && codex.Auth != CodexAuthAPI && codex.Auth != CodexAuthLogin:
		problems = append(problems, fmt.Sprintf("providers.codex.auth must be auto (the default), api or login, got %q", codex.Auth))
	case codex.APIKeyEnv != "" && !envNameRE.MatchString(codex.APIKeyEnv):
		problems = append(problems, fmt.Sprintf("providers.codex.api_key_env %q must be an environment variable name (letters, digits, _)", codex.APIKeyEnv))
	case execution.IsParentSessionVar(env):
		problems = append(problems, fmt.Sprintf("providers.codex.api_key_env %q belongs to the agent session that launched the orchestrator; no child process gets it", env))
	}
	problems = append(problems, transportProblems(cfg, "codex", codex.TransportConfig)...)
	if codex.apiKeyLiteralSet {
		problems = append(problems, "providers.codex.api_key must not be set in the configuration file; name an environment variable via providers.codex.api_key_env instead")
	}
	return problems
}

// redactedRuleRE finds the rule secretscan.Redact names in its replacement.
var redactedRuleRE = regexp.MustCompile(`\[REDACTED:([a-z0-9-]+)\]`)

// apiEnvProblems validates providers.claude.api_env (specs/claude-auth/SPEC.md §17.3), in the
// order of the names. A message names the variable and never its value: the file is
// committed, and a value that looks like a secret must not reach a console or a log either.
func apiEnvProblems(cfg Config) []string {
	var problems []string
	c := cfg.Providers.Claude
	credentials := cfg.CredentialEnvNames()
	removed := domain.ClaudeAPIModeRemoved()
	for _, name := range slices.Sorted(maps.Keys(c.APIEnv)) {
		if !envNameRE.MatchString(name) {
			problems = append(problems, fmt.Sprintf("providers.claude.api_env: %q must be an environment variable name (letters, digits, _)", name))
			continue
		}
		key := "providers.claude.api_env." + name
		switch {
		case name == DefaultClaudeAPIKeyEnv || name == c.KeyEnv():
			problems = append(problems, key+": the API key comes only from the environment (providers.claude.api_key_env), never from the configuration file")
		case slices.Contains(removed, name) || strings.HasPrefix(name, "CLAUDE_CODE_USE_"):
			problems = append(problems, key+": api mode removes this variable because it outranks the API key; setting it would undo the mode")
		case execution.IsParentSessionVar(name):
			problems = append(problems, key+": belongs to the agent session that launched the orchestrator; no child process gets it")
		case slices.Contains(credentials, name):
			problems = append(problems, key+": is a credential this config names; it comes only from the environment")
		default:
			if why := credentialLike(name); why != "" {
				problems = append(problems, key+": looks like a credential ("+why+"); a credential belongs in the environment "+
					"or in the profile a variable names, not in the committed configuration")
			}
		}
		if problem := apiEnvValueProblem(c.APIEnv[name]); problem != "" {
			problems = append(problems, key+": "+problem)
		}
	}
	return problems
}

// apiEnvValueProblem reports why an api_env value is refused, without quoting it.
func apiEnvValueProblem(v string) string {
	if v == "" {
		return "the value must not be empty"
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return "the value contains a control character"
		}
	}
	if u, err := url.Parse(v); err == nil && u.User != nil {
		return "the value is a URL with a user or password; the configuration file is committed and holds no secret"
	}
	if redacted := secretscan.Redact(v); redacted != v {
		rule := "secret scan"
		if m := redactedRuleRE.FindStringSubmatch(redacted); m != nil {
			rule = "secret scan rule " + m[1]
		}
		return "the value looks like a secret (" + rule + "); the configuration file is committed and holds no secret"
	}
	return ""
}
