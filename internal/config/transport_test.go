package config

import (
	"errors"
	"math"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// apiClaudeConfig is validConfig with providers.claude on the API transport and every claude
// route on a full model ID.
func apiClaudeConfig() Config {
	cfg := validConfig()
	cfg.Providers.Claude.Transport = TransportAPI
	cfg.Actions.Investigate.Model = "claude-sonnet-5-5"
	cfg.Actions.Implement.Model = "claude-opus-5-5"
	cfg.Translation.Model = "claude-haiku-5-5"
	return cfg
}

func TestValidate_Transport(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	on := true
	for name, tc := range map[string]struct {
		edit func(*Config)
		want string // "" = valid
	}{
		"cli explicit":       {func(c *Config) { c.Providers.Claude.Transport = TransportCLI }, ""},
		"unknown transport":  {func(c *Config) { c.Providers.Codex.Transport = "http" }, `providers.codex.transport must be cli (the default) or api, got "http"`},
		"base_url on cli":    {func(c *Config) { c.Providers.Codex.BaseURL = "https://gw.example" }, "providers.codex.base_url needs providers.codex.transport: api"},
		"web_tools on cli":   {func(c *Config) { c.Providers.Claude.WebTools = &on }, "providers.claude.web_tools needs providers.claude.transport: api"},
		"prices on cli":      {func(c *Config) { c.Providers.Claude.Prices = map[string]ModelPrice{} }, "providers.claude.prices needs"},
		"api, full IDs":      {func(c *Config) { *c = apiClaudeConfig() }, ""},
		"api, alias action":  {func(c *Config) { *c = apiClaudeConfig(); c.Actions.Implement.Model = "opus" }, "actions.implement: opus is a claude CLI alias"},
		"api, alias 1m":      {func(c *Config) { *c = apiClaudeConfig(); c.Actions.Implement.Model = "Sonnet[1m]" }, "actions.implement: Sonnet[1m] is a claude CLI alias"},
		"api, alias transl.": {func(c *Config) { *c = apiClaudeConfig(); c.Translation.Model = "haiku" }, "translation: haiku is a claude CLI alias"},
		"api, alias by_complexity": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Actions.Implement.ByComplexity = map[domain.Complexity]ModelEffort{domain.ComplexitySimple: {Model: "haiku"}}
		}, "actions.implement.by_complexity.simple: haiku is a claude CLI alias"},
		"codex api keeps its names": {func(c *Config) { c.Providers.Codex.Transport = TransportAPI }, ""},
		"alias on another provider": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.Transport = ""
			c.Providers.Codex.Transport = TransportAPI
			c.Actions.Review.Model = "sonnet"
		}, ""},
		"base_url https": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.BaseURL = "https://gw.example.internal/litellm"
		}, ""},
		"base_url localhost":    {func(c *Config) { *c = apiClaudeConfig(); c.Providers.Claude.BaseURL = "http://127.0.0.1:4000" }, ""},
		"base_url plain http":   {func(c *Config) { *c = apiClaudeConfig(); c.Providers.Claude.BaseURL = "http://gw.example" }, "providers.claude.base_url: must use https (plain http only for localhost)"},
		"base_url relative":     {func(c *Config) { *c = apiClaudeConfig(); c.Providers.Claude.BaseURL = "gw.example" }, "must be an absolute URL"},
		"base_url user":         {func(c *Config) { *c = apiClaudeConfig(); c.Providers.Claude.BaseURL = "https://u:p@gw.example" }, "must not hold a user or password"},
		"base_url query":        {func(c *Config) { *c = apiClaudeConfig(); c.Providers.Claude.BaseURL = "https://gw.example/?key=x" }, "must not have a query or a fragment"},
		"base_url other scheme": {func(c *Config) { *c = apiClaudeConfig(); c.Providers.Claude.BaseURL = "ftp://gw.example" }, "must use https"},
		"api_env with api": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.APIEnv = map[string]string{"AWS_REGION": "eu-west-1"}
		}, "providers.claude.api_env only configures the claude CLI"},
		"prices valid": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.Prices = map[string]ModelPrice{"bedrock/eu.anthropic.claude-opus-5-5": {Input: f(4.4), Output: f(22), CacheRead: f(0)}}
		}, ""},
		"price missing output": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.Prices = map[string]ModelPrice{"m": {Input: f(1)}}
		}, `providers.claude.prices["m"].output is required`},
		"price negative": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.Prices = map[string]ModelPrice{"m": {Input: f(-1), Output: f(1)}}
		}, `providers.claude.prices["m"].input must be a finite number >= 0`},
		"price NaN": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.Prices = map[string]ModelPrice{"m": {Input: f(1), Output: f(1), CacheWrite: f(math.NaN())}}
		}, "cache_write must be a finite number"},
		"price padded ID": {func(c *Config) {
			*c = apiClaudeConfig()
			c.Providers.Claude.Prices = map[string]ModelPrice{" m": {Input: f(1), Output: f(1)}}
		}, "the model ID must not be empty or padded"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			tc.edit(&cfg)
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

func TestWebToolsOn(t *testing.T) {
	off := false
	for _, tc := range []struct {
		t    TransportConfig
		want bool
	}{
		{TransportConfig{}, true},
		{TransportConfig{BaseURL: "https://gw.example"}, false},
		{TransportConfig{BaseURL: "https://gw.example", WebTools: new(bool)}, false},
		{TransportConfig{WebTools: &off}, false},
	} {
		if got := tc.t.WebToolsOn(); got != tc.want {
			t.Errorf("%+v: WebToolsOn() = %v, want %v", tc.t, got, tc.want)
		}
	}
	on := true
	if !(TransportConfig{BaseURL: "https://gw.example", WebTools: &on}).WebToolsOn() {
		t.Error("an explicit web_tools: true is not kept")
	}
}

func TestLoad_Transport(t *testing.T) {
	cfg, err := Load(writeTempConfig(t, validV2(`providers:
  claude:
    transport: api
    base_url: https://gw.example.internal
    web_tools: false
    prices:
      bedrock/eu.anthropic.claude-opus-5-5: {input: 4.4, output: 22, cache_read: 0.44, cache_write: 5.5}
  codex:
    transport: api
    base_url: https://api.openai.com
`)))
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Providers.Claude
	if !c.UsesAPI() || c.BaseURL != "https://gw.example.internal" || c.WebToolsOn() {
		t.Errorf("Providers.Claude = %+v", c.TransportConfig)
	}
	p, ok := c.Prices["bedrock/eu.anthropic.claude-opus-5-5"]
	if !ok || *p.Input != 4.4 || *p.Output != 22 || *p.CacheRead != 0.44 || *p.CacheWrite != 5.5 {
		t.Errorf("Prices = %+v", c.Prices)
	}
	if x := cfg.Providers.Codex; !x.UsesAPI() || x.BaseURL != "https://api.openai.com" {
		t.Errorf("Providers.Codex = %+v", x.TransportConfig)
	}

	// The strict schema covers the new keys too.
	_, err = Load(writeTempConfig(t, validV2("providers:\n  claude:\n    transport: api\n    base_ulr: https://x\n")))
	if err == nil || !strings.Contains(err.Error(), "base_ulr") {
		t.Fatalf("want an unknown-key error for base_ulr, got %v", err)
	}
}

// SPEC §2 item 2: transport: api refuses any resolution that ends in the login mode.
func TestResolveAuth_TransportAPINeedsKey(t *testing.T) {
	claude := apiClaudeConfig()
	codex := validConfig()
	codex.Providers.Codex.Transport = TransportAPI
	for name, tc := range map[string]struct {
		run  func() error
		want string
	}{
		"claude auto, no key": {func() error {
			_, _, err := ResolveClaudeAuth(claude, "", envLookup(nil))
			return err
		}, "providers.claude.transport is api, which needs the api auth mode, but the mode resolved to login (decided by default)"},
		"claude login flag": {func() error {
			_, _, err := ResolveClaudeAuth(claude, ClaudeAuthLogin, envLookup(map[string]string{"ANTHROPIC_API_KEY": "test-key"}))
			return err
		}, "(decided by flag)"},
		"claude api with key": {func() error {
			_, _, err := ResolveClaudeAuth(claude, "", envLookup(map[string]string{"ANTHROPIC_API_KEY": "test-key"}))
			return err
		}, ""},
		"codex auto, no key": {func() error {
			_, _, err := ResolveCodexAuth(codex, "", envLookup(nil))
			return err
		}, "providers.codex.transport is api"},
		"codex api with key": {func() error {
			_, _, err := ResolveCodexAuth(codex, "", envLookup(map[string]string{"OPENAI_API_KEY": "test-key"}))
			return err
		}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.run()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want no error, got %v", err)
				}
				return
			}
			var ce *apperr.ConfigError
			if !errors.As(err, &ce) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a config error containing %q, got %v", tc.want, err)
			}
		})
	}
}
