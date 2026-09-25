package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func float64Ptr(v float64) *float64 { return &v }

// validDecisionConfig returns a fully valid Jev integration config surface, matching
// the example in specs/JEV integration/TASKS.md T02. It is deliberately independent
// from validConfig() (validator_test.go): the two surfaces are validated by separate
// functions until T12 merges them.
func validDecisionConfig() Config {
	return Config{
		Decision: DecisionConfig{
			Jev: JevConfig{
				BaseURL:    "https://api.typesafe.ai",
				APIKeyEnv:  "JEV_API_KEY",
				Model:      "jev-1.13.0",
				Timeout:    5 * time.Second,
				MaxRetries: 1,
				Complexity: JevComplexityConfig{Primary: "choice", Shadow: true},
			},
			OnFailure: OnFailureAsk,
		},
		Translation: TranslationConfig{
			ProviderModel: ProviderModel{Provider: "claude", Model: "haiku"},
			Timeout:       60 * time.Second,
		},
		Policy: PolicyConfig{
			ContextDepth: ContextDepthPolicyConfig{ProjectThreshold: float64Ptr(0.5)},
			Complexity:   ComplexityPolicyConfig{Complex: float64Ptr(0.40), Medium: float64Ptr(0.50)},
			Stages: StagePolicyConfig{
				Thresholds: map[string]float64{
					"research": 0.70, "analysis": 0.60, "planning": 0.65,
					"development": 0.60, "testing": 0.60, "review": 0.70,
				},
				Force: map[string][]string{"complex": {"analysis", "planning", "review"}},
			},
		},
		Context: ContextConfig{MaxFiles: 20, MaxBytes: 24576},
		Actions: ActionsConfig{
			Research: ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
		},
	}
}

func TestValidateDecisionConfig_Valid(t *testing.T) {
	if err := ValidateDecisionConfig(validDecisionConfig()); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidateDecisionConfig_Jev(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr string
	}{
		{"empty base_url", func(c *Config) { c.Decision.Jev.BaseURL = "" }, "decision.jev.base_url is required"},
		{"base_url without scheme", func(c *Config) { c.Decision.Jev.BaseURL = "api.typesafe.ai" }, "must be a valid http(s) URL"},
		{"base_url with unsupported scheme", func(c *Config) { c.Decision.Jev.BaseURL = "ftp://api.typesafe.ai" }, "must be a valid http(s) URL"},
		{"base_url without host", func(c *Config) { c.Decision.Jev.BaseURL = "https://" }, "must be a valid http(s) URL"},
		{"empty api_key_env", func(c *Config) { c.Decision.Jev.APIKeyEnv = "" }, "decision.jev.api_key_env is required"},
		{"empty model", func(c *Config) { c.Decision.Jev.Model = "" }, "decision.jev.model is required"},
		{"zero timeout", func(c *Config) { c.Decision.Jev.Timeout = 0 }, "decision.jev.timeout must be a positive duration"},
		{"negative timeout", func(c *Config) { c.Decision.Jev.Timeout = -time.Second }, "decision.jev.timeout must be a positive duration"},
		{"max_retries too high", func(c *Config) { c.Decision.Jev.MaxRetries = 4 }, "decision.jev.max_retries must be between 0 and 3"},
		{"max_retries negative", func(c *Config) { c.Decision.Jev.MaxRetries = -1 }, "decision.jev.max_retries must be between 0 and 3"},
		{"unknown complexity.primary", func(c *Config) { c.Decision.Jev.Complexity.Primary = "vote" }, "decision.jev.complexity.primary"},
		{"empty complexity.primary", func(c *Config) { c.Decision.Jev.Complexity.Primary = "" }, "decision.jev.complexity.primary"},
		{"unknown on_failure", func(c *Config) { c.Decision.OnFailure = "retry" }, `decision.on_failure must be "ask" or "fail"`},
		{"empty on_failure", func(c *Config) { c.Decision.OnFailure = "" }, `decision.on_failure must be "ask" or "fail"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDecisionConfig()
			tt.mutate(&cfg)
			err := ValidateDecisionConfig(cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}

	// max_retries at the edges of [0, 3] must both be accepted.
	for _, n := range []int{0, 3} {
		cfg := validDecisionConfig()
		cfg.Decision.Jev.MaxRetries = n
		if err := ValidateDecisionConfig(cfg); err != nil {
			t.Errorf("max_retries=%d rejected: %v", n, err)
		}
	}
}

func TestValidateDecisionConfig_APIKeyMustNotBeLiteral(t *testing.T) {
	cfg := validDecisionConfig()
	cfg.Decision.Jev.apiKeyLiteralSet = true

	err := ValidateDecisionConfig(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "decision.jev.api_key must not be set") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "api_key_env") {
		t.Errorf("expected error to point at api_key_env, got: %s", err.Error())
	}
}

// A literal api_key: field in YAML must be detected even though JevConfig has no
// exported field for it - this is the actual parsing path, not just the flag set
// directly in TestValidateDecisionConfig_APIKeyMustNotBeLiteral.
func TestJevConfig_UnmarshalYAML_DetectsLiteralAPIKey(t *testing.T) {
	var j JevConfig
	yamlSrc := "base_url: https://api.typesafe.ai\napi_key: sk-should-not-be-here\n"
	if err := yaml.Unmarshal([]byte(yamlSrc), &j); err != nil {
		t.Fatal(err)
	}
	if !j.apiKeyLiteralSet {
		t.Fatal("expected apiKeyLiteralSet to be true after parsing a literal api_key field")
	}

	var clean JevConfig
	if err := yaml.Unmarshal([]byte("base_url: https://api.typesafe.ai\n"), &clean); err != nil {
		t.Fatal(err)
	}
	if clean.apiKeyLiteralSet {
		t.Fatal("expected apiKeyLiteralSet to be false when no api_key field is present")
	}
}

func TestJevConfig_UnmarshalYAML_Timeout(t *testing.T) {
	var j JevConfig
	src := "base_url: https://api.typesafe.ai\ntimeout: 5s\ncomplexity:\n  primary: choice\n  shadow: true\n"
	if err := yaml.Unmarshal([]byte(src), &j); err != nil {
		t.Fatal(err)
	}
	if j.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", j.Timeout)
	}
	if j.Complexity.Primary != "choice" || !j.Complexity.Shadow {
		t.Errorf("Complexity = %+v, want {choice true}", j.Complexity)
	}

	var bad JevConfig
	if err := yaml.Unmarshal([]byte("timeout: not-a-duration\n"), &bad); err == nil {
		t.Fatal("expected an error parsing an invalid duration")
	}
}

func TestJevConfig_UnmarshalYAML_Mock(t *testing.T) {
	var j JevConfig
	if err := yaml.Unmarshal([]byte("mock: true\n"), &j); err != nil {
		t.Fatal(err)
	}
	if !j.Mock {
		t.Error("Mock = false, want true")
	}
}

func TestTranslationConfig_UnmarshalYAML(t *testing.T) {
	var tr TranslationConfig
	src := "provider: claude\nmodel: haiku\ntimeout: 60s\n"
	if err := yaml.Unmarshal([]byte(src), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Provider != "claude" || tr.Model != "haiku" || tr.Timeout != 60*time.Second {
		t.Errorf("got %+v", tr)
	}

	var bad TranslationConfig
	if err := yaml.Unmarshal([]byte("timeout: not-a-duration\n"), &bad); err == nil {
		t.Fatal("expected an error parsing an invalid duration")
	}
}

func TestValidateDecisionConfig_Translation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr string
	}{
		{"missing provider", func(c *Config) { c.Translation.Provider = "" }, "translation.provider is required"},
		{"unsupported provider", func(c *Config) { c.Translation.Provider = "openrouter" }, `translation.provider "openrouter" is not a supported provider`},
		{"missing model", func(c *Config) { c.Translation.Model = "" }, "translation.model is required"},
		{"zero timeout", func(c *Config) { c.Translation.Timeout = 0 }, "translation.timeout must be a positive duration"},
		{"negative timeout", func(c *Config) { c.Translation.Timeout = -time.Second }, "translation.timeout must be a positive duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDecisionConfig()
			tt.mutate(&cfg)
			if err := ValidateDecisionConfig(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateDecisionConfig() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDecisionConfig_Policy(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr string
	}{
		{"missing project_threshold", func(c *Config) { c.Policy.ContextDepth.ProjectThreshold = nil }, "policy.context_depth.project_threshold is required"},
		{"project_threshold above 1", func(c *Config) { c.Policy.ContextDepth.ProjectThreshold = float64Ptr(1.5) }, "project_threshold must be in [0, 1]"},
		{"project_threshold negative", func(c *Config) { c.Policy.ContextDepth.ProjectThreshold = float64Ptr(-0.1) }, "project_threshold must be in [0, 1]"},
		{"missing complexity.complex", func(c *Config) { c.Policy.Complexity.Complex = nil }, "policy.complexity.complex is required"},
		{"missing complexity.medium", func(c *Config) { c.Policy.Complexity.Medium = nil }, "policy.complexity.medium is required"},
		{"complexity.complex out of range", func(c *Config) { c.Policy.Complexity.Complex = float64Ptr(1.1) }, "policy.complexity.complex must be in [0, 1]"},
		{"missing stage threshold", func(c *Config) {
			delete(c.Policy.Stages.Thresholds, "review")
		}, "policy.stages.thresholds.review is required"},
		{"stage threshold out of range", func(c *Config) {
			c.Policy.Stages.Thresholds["review"] = 1.5
		}, "policy.stages.thresholds.review must be in [0, 1]"},
		{"unknown stage threshold key", func(c *Config) {
			c.Policy.Stages.Thresholds["deploy"] = 0.5
		}, `policy.stages.thresholds has unknown stage "deploy"`},
		{"force references unknown complexity", func(c *Config) {
			c.Policy.Stages.Force = map[string][]string{"critical": {"review"}}
		}, `policy.stages.force has unknown complexity "critical"`},
		{"force references unknown stage", func(c *Config) {
			c.Policy.Stages.Force = map[string][]string{"complex": {"deploy"}}
		}, `policy.stages.force.complex has unknown stage "deploy"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDecisionConfig()
			tt.mutate(&cfg)
			if err := ValidateDecisionConfig(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateDecisionConfig() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}

	// A threshold of exactly 0 must be accepted as a deliberate (if extreme) choice,
	// distinct from an omitted one.
	cfg := validDecisionConfig()
	cfg.Policy.ContextDepth.ProjectThreshold = float64Ptr(0)
	cfg.Policy.Stages.Thresholds["review"] = 0
	if err := ValidateDecisionConfig(cfg); err != nil {
		t.Errorf("threshold of exactly 0 rejected: %v", err)
	}
}

func TestValidateDecisionConfig_Context(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr string
	}{
		{"zero max_files", func(c *Config) { c.Context.MaxFiles = 0 }, "context.max_files must be positive"},
		{"negative max_files", func(c *Config) { c.Context.MaxFiles = -1 }, "context.max_files must be positive"},
		{"zero max_bytes", func(c *Config) { c.Context.MaxBytes = 0 }, "context.max_bytes must be positive"},
		{"blank extra_deny entry", func(c *Config) { c.Context.ExtraDeny = []string{" "} }, "context.extra_deny[0] must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validDecisionConfig()
			tt.mutate(&cfg)
			if err := ValidateDecisionConfig(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateDecisionConfig() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDecisionConfig_Research(t *testing.T) {
	cfg := validDecisionConfig()
	cfg.Actions.Research.Provider = ""
	err := ValidateDecisionConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "research.provider is required") {
		t.Errorf("ValidateDecisionConfig() = %v, want error containing research.provider is required", err)
	}
}

func TestValidateDecisionConfig_AggregatesAllProblems(t *testing.T) {
	cfg := validDecisionConfig()
	cfg.Decision.Jev.BaseURL = ""
	cfg.Translation.Provider = ""
	cfg.Context.MaxFiles = 0

	err := ValidateDecisionConfig(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"decision.jev.base_url is required", "translation.provider is required", "context.max_files must be positive"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected aggregated error to contain %q, got: %s", want, msg)
		}
	}
}

func TestConfig_DecisionPolicy(t *testing.T) {
	cfg := validDecisionConfig()
	p := cfg.DecisionPolicy()

	if p.ProjectContextThreshold != 0.5 {
		t.Errorf("ProjectContextThreshold = %v, want 0.5", p.ProjectContextThreshold)
	}
	if got := len(p.StageThreshold); got != 6 {
		t.Fatalf("StageThreshold has %d entries, want 6", got)
	}
	if p.StageThreshold["review"] != 0.70 {
		t.Errorf(`StageThreshold["review"] = %v, want 0.70`, p.StageThreshold["review"])
	}
	if p.ComplexityThreshold["complex"] != 0.40 || p.ComplexityThreshold["medium"] != 0.50 {
		t.Errorf("ComplexityThreshold = %+v", p.ComplexityThreshold)
	}
	force := p.ForceStages["complex"]
	if len(force) != 3 {
		t.Fatalf("ForceStages[complex] = %v, want 3 stages", force)
	}
}
