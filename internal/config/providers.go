// This file defines and validates the providers section (specs/claude-auth/SPEC.md §5): how the
// claude CLI is authenticated. Validate includes these checks.
package config

import (
	"fmt"
	"strings"

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
