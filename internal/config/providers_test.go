package config

import (
	"maps"
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

func envLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// Every row of SPEC §4 and corner cases 3-5, 13, 16.
func TestResolveClaudeAuth(t *testing.T) {
	key := "test-" + "key-123"
	oauth := "sk-ant-" + "oat01-abc"
	for name, tc := range map[string]struct {
		auth, keyEnv, flag string
		env                map[string]string
		wantMode, wantBy   string
		wantLine           string
		wantNotice         string
		wantErr            string
	}{
		"api with key":         {auth: "api", env: map[string]string{"ANTHROPIC_API_KEY": key}, wantMode: "api", wantBy: "config", wantLine: "claude auth: api key ($ANTHROPIC_API_KEY)"},
		"api unset":            {auth: "api", wantErr: "ANTHROPIC_API_KEY is not set"},
		"api blank":            {auth: "api", env: map[string]string{"ANTHROPIC_API_KEY": "  "}, wantErr: "is empty"},
		"api invalid":          {auth: "api", env: map[string]string{"ANTHROPIC_API_KEY": "a b"}, wantErr: "whitespace"},
		"auto with key":        {env: map[string]string{"ANTHROPIC_API_KEY": " " + key + "\n"}, wantMode: "api", wantBy: "default", wantLine: "claude auth: api key ($ANTHROPIC_API_KEY, auto)"},
		"auto unset":           {wantMode: "login", wantBy: "default", wantLine: "claude auth: CLI login (auto: $ANTHROPIC_API_KEY not set)"},
		"auto blank":           {env: map[string]string{"ANTHROPIC_API_KEY": ""}, wantMode: "login", wantBy: "default", wantNotice: "ANTHROPIC_API_KEY is set but empty; using the CLI login"},
		"auto invalid":         {env: map[string]string{"ANTHROPIC_API_KEY": "a\tb"}, wantErr: "control character"},
		"login key set":        {auth: "login", env: map[string]string{"ANTHROPIC_API_KEY": key}, wantMode: "login", wantBy: "config", wantNotice: "ANTHROPIC_API_KEY is set but not used (--claude-auth login)"},
		"login key invalid":    {auth: "login", env: map[string]string{"ANTHROPIC_API_KEY": "a b"}, wantMode: "login", wantBy: "config", wantNotice: "is set but not used"},
		"flag over config":     {auth: "api", flag: "login", wantMode: "login", wantBy: "flag"},
		"configured env unset": {keyEnv: "ORCH_CLAUDE_KEY", env: map[string]string{"ANTHROPIC_API_KEY": key}, wantMode: "login", wantBy: "default", wantNotice: "ORCH_CLAUDE_KEY"},
		"configured env used":  {keyEnv: "ORCH_CLAUDE_KEY", env: map[string]string{"ORCH_CLAUDE_KEY": key, "ANTHROPIC_API_KEY": "other"}, wantMode: "api", wantBy: "default", wantLine: "claude auth: api key ($ORCH_CLAUDE_KEY, auto)"},
		"oauth token":          {env: map[string]string{"ANTHROPIC_API_KEY": oauth}, wantErr: "CLAUDE_CODE_OAUTH_TOKEN"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Providers.Claude = ClaudeProviderConfig{Auth: tc.auth, APIKeyEnv: tc.keyEnv}
			got, notices, err := ResolveClaudeAuth(cfg, tc.flag, envLookup(tc.env))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want an error containing %q, got %v", tc.wantErr, err)
				}
				for _, v := range tc.env {
					if v = strings.TrimSpace(v); v != "" && strings.Contains(err.Error(), v) {
						t.Errorf("the key appears in the error: %v", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != tc.wantMode || got.DecidedBy != tc.wantBy {
				t.Errorf("got mode %q decided by %q, want %q by %q", got.Mode, got.DecidedBy, tc.wantMode, tc.wantBy)
			}
			if tc.wantLine != "" && got.Line() != tc.wantLine {
				t.Errorf("Line() = %q, want %q", got.Line(), tc.wantLine)
			}
			if tc.wantNotice == "" && len(notices) != 0 {
				t.Errorf("unexpected notices %q", notices)
			}
			if tc.wantNotice != "" && !strings.Contains(strings.Join(notices, "\n"), tc.wantNotice) {
				t.Errorf("notices %q lack %q", notices, tc.wantNotice)
			}
		})
	}
}

func TestParseClaudeAuth(t *testing.T) {
	for _, v := range []string{"auto", "api", "login"} {
		if got, err := ParseClaudeAuth(v); err != nil || got != v {
			t.Errorf("ParseClaudeAuth(%q) = %q, %v", v, got, err)
		}
	}
	if _, err := ParseClaudeAuth("subscription"); err == nil {
		t.Error("an unknown value must be refused")
	}
}

// specs/claude-auth/SPEC.md §17.2: api_env is part of the strict schema and parses into the map.
func TestLoad_ProvidersClaudeAPIEnv(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2("providers:\n  claude:\n    auth: api\n    api_env:\n"+
		"      ANTHROPIC_BASE_URL: https://ai-gateway.example.internal\n"+
		"      AWS_PROFILE: claude-bedrock\n"+
		"      ANTHROPIC_DEFAULT_HAIKU_MODEL: bedrock/eu.anthropic.claude-haiku-4-5-20251001-v1:0\n")))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":            "https://ai-gateway.example.internal",
		"AWS_PROFILE":                   "claude-bedrock",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "bedrock/eu.anthropic.claude-haiku-4-5-20251001-v1:0",
	}
	if got := cfg.Providers.Claude.APIEnv; !maps.Equal(got, want) {
		t.Errorf("APIEnv = %v, want %v", got, want)
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("want valid, got %v", err)
	}
}

// §17.3: every class of refused name and value. Each message names the variable and never
// holds the value.
func TestValidate_ProvidersClaudeAPIEnv(t *testing.T) {
	// Assembled at run time: no token-shaped literal in the repository (CLAUDE.md).
	secret := "sk-" + "ant-" + "api03-" + strings.Repeat("Xy7Kq2Lm9P", 3)
	gateway := map[string]string{
		"ANTHROPIC_BASE_URL":           "https://ai-gateway.example.internal",
		"AWS_PROFILE":                  "claude-bedrock",
		"AWS_REGION":                   "eu-west-1",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "bedrock/eu.anthropic.claude-opus-5-5",
	}
	for name, tc := range map[string]struct {
		keyEnv string
		env    map[string]string
		want   string // "" = valid
	}{
		"gateway setup":       {env: gateway},
		"empty map":           {env: map[string]string{}},
		"bad name":            {env: map[string]string{"AWS-PROFILE": "p"}, want: `providers.claude.api_env: "AWS-PROFILE" must be an environment variable name`},
		"the default key":     {env: map[string]string{"ANTHROPIC_API_KEY": "v"}, want: "providers.claude.api_env.ANTHROPIC_API_KEY: the API key comes only from the environment"},
		"the configured key":  {keyEnv: "ORCH_CLAUDE_KEY", env: map[string]string{"ORCH_CLAUDE_KEY": "v"}, want: "providers.claude.api_env.ORCH_CLAUDE_KEY: the API key comes only from the environment"},
		"auth token":          {env: map[string]string{"ANTHROPIC_AUTH_TOKEN": "v"}, want: "providers.claude.api_env.ANTHROPIC_AUTH_TOKEN: api mode removes this variable"},
		"use bedrock":         {env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, want: "providers.claude.api_env.CLAUDE_CODE_USE_BEDROCK: api mode removes this variable"},
		"a future use_*":      {env: map[string]string{"CLAUDE_CODE_USE_ANYCLOUD": "1"}, want: "providers.claude.api_env.CLAUDE_CODE_USE_ANYCLOUD: api mode removes this variable"},
		"oauth token":         {env: map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "v"}, want: "providers.claude.api_env.CLAUDE_CODE_OAUTH_TOKEN: api mode removes this variable"},
		"profile":             {env: map[string]string{"ANTHROPIC_PROFILE": "v"}, want: "providers.claude.api_env.ANTHROPIC_PROFILE: api mode removes this variable"},
		"federation rule":     {env: map[string]string{"ANTHROPIC_FEDERATION_RULE_ID": "v"}, want: "providers.claude.api_env.ANTHROPIC_FEDERATION_RULE_ID: api mode removes this variable"},
		"parent session":      {env: map[string]string{"CLAUDECODE": "1"}, want: "providers.claude.api_env.CLAUDECODE: belongs to the agent session"},
		"another credential":  {env: map[string]string{"JEV_API_KEY": "v"}, want: "providers.claude.api_env.JEV_API_KEY: is a credential this config names"},
		"aws secret":          {env: map[string]string{"AWS_SECRET_ACCESS_KEY": "v"}, want: "providers.claude.api_env.AWS_SECRET_ACCESS_KEY: looks like a credential (contains SECRET)"},
		"aws session token":   {env: map[string]string{"AWS_SESSION_TOKEN": "v"}, want: "providers.claude.api_env.AWS_SESSION_TOKEN: looks like a credential (contains TOKEN)"},
		"empty value":         {env: map[string]string{"AWS_PROFILE": ""}, want: "providers.claude.api_env.AWS_PROFILE: the value must not be empty"},
		"newline":             {env: map[string]string{"AWS_PROFILE": "claude\nbedrock"}, want: "providers.claude.api_env.AWS_PROFILE: the value contains a control character"},
		"tab":                 {env: map[string]string{"AWS_PROFILE": "claude\tbedrock"}, want: "providers.claude.api_env.AWS_PROFILE: the value contains a control character"},
		"url with userinfo":   {env: map[string]string{"ANTHROPIC_BASE_URL": "https://gwuser:gwpass@gw.example"}, want: "providers.claude.api_env.ANTHROPIC_BASE_URL: the value is a URL with a user or password"},
		"url with a user":     {env: map[string]string{"ANTHROPIC_BASE_URL": "https://gwuser@gw.example"}, want: "providers.claude.api_env.ANTHROPIC_BASE_URL: the value is a URL with a user or password"},
		"secret scan finding": {env: map[string]string{"ANTHROPIC_CUSTOM_HEADERS": secret}, want: "providers.claude.api_env.ANTHROPIC_CUSTOM_HEADERS: the value looks like a secret (secret scan"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			cfg.Providers.Claude = ClaudeProviderConfig{APIKeyEnv: tc.keyEnv, APIEnv: tc.env}
			err := Validate(cfg)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
			for _, v := range tc.env {
				if len(v) > 1 && strings.Contains(err.Error(), v) {
					t.Errorf("the value %q appears in the error:\n%v", v, err)
				}
			}
			for _, part := range []string{"gwpass", "gwuser", "Xy7Kq2Lm9P"} {
				if strings.Contains(err.Error(), part) {
					t.Errorf("a part of the value (%q) appears in the error:\n%v", part, err)
				}
			}
		})
	}
}

func TestLoad_ProvidersStrictKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		extra string
		want  string
	}{
		"other provider": {"providers:\n  gemini:\n    auth: api\n", "line 26: gemini"},
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
