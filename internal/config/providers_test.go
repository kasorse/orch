package config

import (
	"strings"
	"testing"
)

func TestClaudeProviderConfig_Defaults(t *testing.T) {
	var c ClaudeProviderConfig
	if got := c.EffectiveAuth(); got != ClaudeAuthAuto {
		t.Errorf("EffectiveAuth() = %q, want auto", got)
	}
	if got := c.KeyEnv(); got != "ANTHROPIC_API_KEY" {
		t.Errorf("KeyEnv() = %q, want ANTHROPIC_API_KEY", got)
	}
	c = ClaudeProviderConfig{Auth: ClaudeAuthLogin, APIKeyEnv: "ORCH_KEY"}
	if c.EffectiveAuth() != ClaudeAuthLogin || c.KeyEnv() != "ORCH_KEY" {
		t.Errorf("explicit values not kept: %q %q", c.EffectiveAuth(), c.KeyEnv())
	}
}

func TestValidate_ProvidersClaude(t *testing.T) {
	for name, tc := range map[string]struct {
		claude ClaudeProviderConfig
		want   string // "" = valid
	}{
		"absent":             {ClaudeProviderConfig{}, ""},
		"auth auto":          {ClaudeProviderConfig{Auth: "auto"}, ""},
		"auth api":           {ClaudeProviderConfig{Auth: "api"}, ""},
		"auth login":         {ClaudeProviderConfig{Auth: "login"}, ""},
		"auth unknown":       {ClaudeProviderConfig{Auth: "subscription"}, `providers.claude.auth must be auto (the default), api or login, got "subscription"`},
		"auth case":          {ClaudeProviderConfig{Auth: "API"}, "providers.claude.auth must be"},
		"custom key env":     {ClaudeProviderConfig{APIKeyEnv: "ORCH_CLAUDE_KEY"}, ""},
		"key env with dash":  {ClaudeProviderConfig{APIKeyEnv: "ORCH-KEY"}, "providers.claude.api_key_env"},
		"key env blank":      {ClaudeProviderConfig{APIKeyEnv: "  "}, "providers.claude.api_key_env"},
		"key env a literal":  {ClaudeProviderConfig{APIKeyEnv: "sk ant"}, "providers.claude.api_key_env"},
		"key env is session": {ClaudeProviderConfig{APIKeyEnv: "CLAUDECODE"}, "belongs to the agent session"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Providers.Claude = tc.claude
			err := Validate(cfg)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("want valid, got %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// The default key variable is a credential even without the section, so test.env_pass refuses it.
func TestValidate_EnvPassRefusesClaudeKey(t *testing.T) {
	for name, env := range map[string]string{"": "ANTHROPIC_API_KEY", "configured": "ORCH_CLAUDE_KEY"} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			if name != "" {
				cfg.Providers.Claude.APIKeyEnv = env
			}
			cfg.Test.EnvPass = []string{env}
			err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), "is a credential this config names") {
				t.Fatalf("want the credential refusal, got %v", err)
			}
		})
	}
}

func TestLoad_ProvidersClaude(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2("providers:\n  claude:\n    auth: api\n    api_key_env: ORCH_CLAUDE_KEY\n")))
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.Providers.Claude; c.Auth != "api" || c.APIKeyEnv != "ORCH_CLAUDE_KEY" {
		t.Errorf("Providers.Claude = %+v", c)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("want valid, got %v", err)
	}
}

func TestLoad_ProvidersAbsent(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2("")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers.Claude.EffectiveAuth() != ClaudeAuthAuto || cfg.Providers.Claude.KeyEnv() != "ANTHROPIC_API_KEY" {
		t.Errorf("defaults wrong: %+v", cfg.Providers)
	}
}

// A literal key is declared so it can be rejected with its own message, and its value never
// reaches the message.
func TestLoad_ProvidersClaudeLiteralKeyRejected(t *testing.T) {
	secret := "literal-" + "value-123"
	cfg, err := Load(writeTempConfig(t, validV2("providers:\n  claude:\n    api_key: "+secret+"\n")))
	if err != nil {
		t.Fatal(err)
	}
	err = Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "name an environment variable via providers.claude.api_key_env instead") {
		t.Fatalf("expected literal-key error, got %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the key's value appears in the error:\n%v", err)
	}
}

func TestLoad_ProvidersStrictKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		want  string
	}{
		"other provider": {"providers:\n  codex:\n    auth: api\n", "line 26: codex"},
		"unknown claude": {"providers:\n  claude:\n    autho: api\n", "line 27: autho"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeTempConfig(t, validV2(tc.extra)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an unknown-key error containing %q, got %v", tc.want, err)
			}
		})
	}
}
