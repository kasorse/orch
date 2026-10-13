package config

import (
	"math"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"orchestrator/internal/domain"
)

func float64Ptr(v float64) *float64 { return &v }

func TestValidateDecision_Valid(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidateDecision_Jev(t *testing.T) {
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
		{"empty provider", func(c *Config) { c.Decision.Provider = "" }, "decision.provider is required"},
		{"unknown provider", func(c *Config) { c.Decision.Provider = "openai" }, `decision.provider "openai" is not supported (supported: jev, claude, codex, gemini)`},
		{"empty model", func(c *Config) { c.Decision.Model = "" }, "decision.model is required"},
		{"zero timeout", func(c *Config) { c.Decision.Timeout = 0 }, "decision.timeout must be a positive duration"},
		{"negative timeout", func(c *Config) { c.Decision.Timeout = -time.Second }, "decision.timeout must be a positive duration"},
		{"max_retries too high", func(c *Config) { c.Decision.Jev.MaxRetries = 4 }, "decision.jev.max_retries must be between 0 and 3"},
		{"max_retries negative", func(c *Config) { c.Decision.Jev.MaxRetries = -1 }, "decision.jev.max_retries must be between 0 and 3"},
		{"unknown complexity.primary", func(c *Config) { c.Decision.Complexity.Primary = "vote" }, "decision.complexity.primary"},
		{"empty complexity.primary", func(c *Config) { c.Decision.Complexity.Primary = "" }, "decision.complexity.primary"},
		{"unknown on_failure", func(c *Config) { c.Decision.OnFailure = "retry" }, `decision.on_failure must be "ask" or "fail"`},
		{"empty on_failure", func(c *Config) { c.Decision.OnFailure = "" }, `decision.on_failure must be "ask" or "fail"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			err := Validate(cfg)
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
		cfg := validConfig()
		cfg.Decision.Jev.MaxRetries = n
		if err := Validate(cfg); err != nil {
			t.Errorf("max_retries=%d rejected: %v", n, err)
		}
	}
}

func TestValidateDecision_APIKeyMustNotBeLiteral(t *testing.T) {
	cfg := validConfig()
	cfg.Decision.Jev.apiKeyLiteralSet = true

	err := Validate(cfg)
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

func TestDecisionConfig_UnmarshalYAML(t *testing.T) {
	var d DecisionConfig
	src := "provider: claude\nmodel: sonnet\ntimeout: 60s\ncomplexity:\n  primary: score\n  shadow: true\non_failure: fail\njev:\n  base_url: https://api.typesafe.ai\n  mock: true\n"
	if err := yaml.Unmarshal([]byte(src), &d); err != nil {
		t.Fatal(err)
	}
	if d.Provider != "claude" || d.Model != "sonnet" || d.Timeout != 60*time.Second || d.OnFailure != OnFailureFail {
		t.Errorf("decision = %+v", d)
	}
	if d.Complexity.Primary != "score" || !d.Complexity.Shadow || d.Jev.BaseURL == "" || !d.Jev.Mock || d.UsesJev() {
		t.Errorf("decision = %+v", d)
	}

	var bad DecisionConfig
	if err := yaml.Unmarshal([]byte("timeout: not-a-duration\n"), &bad); err == nil {
		t.Fatal("expected an error parsing an invalid duration")
	}
}

func TestComplexityQuestionsConfig_Sources(t *testing.T) {
	cases := []struct {
		cfg  ComplexityQuestionsConfig
		want string
	}{
		{ComplexityQuestionsConfig{Primary: "choice"}, "choice"},
		{ComplexityQuestionsConfig{Primary: "choice", Shadow: true}, "choice,score"},
		{ComplexityQuestionsConfig{Primary: "score", Shadow: true}, "score,choice"},
	}
	for _, tc := range cases {
		var got []string
		for _, s := range tc.cfg.Sources() {
			got = append(got, string(s))
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("%+v.Sources() = %v, want %s", tc.cfg, got, tc.want)
		}
	}
}

func TestValidateDecision_LLMProviderIgnoresJevBlock(t *testing.T) {
	cfg := validConfig()
	cfg.Decision.Provider = "claude"
	cfg.Decision.Model = "sonnet"
	cfg.Decision.Jev = JevConfig{} // absent entirely
	if err := Validate(cfg); err != nil {
		t.Fatalf("an LLM provider needs no jev block: %v", err)
	}

	cfg.Decision.Jev = JevConfig{BaseURL: "not a url", MaxRetries: 9} // present but invalid
	if err := Validate(cfg); err != nil {
		t.Fatalf("the jev block is not validated for an LLM provider: %v", err)
	}

	cfg.Decision.Jev.apiKeyLiteralSet = true
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "api_key must not be set") {
		t.Errorf("a literal api_key is rejected whichever provider is used: %v", err)
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

func TestValidateDecision_Translation(t *testing.T) {
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
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDecision_Policy(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *Config)
		wantErr string
	}{
		{"missing project_threshold", func(c *Config) { c.Policy.ContextDepth.ProjectThreshold = nil }, "policy.context_depth.project_threshold is required"},
		{"project_threshold above 1", func(c *Config) { c.Policy.ContextDepth.ProjectThreshold = float64Ptr(1.5) }, "project_threshold must be in [0, 1]"},
		{"project_threshold negative", func(c *Config) { c.Policy.ContextDepth.ProjectThreshold = float64Ptr(-0.1) }, "project_threshold must be in [0, 1]"},
		{"missing complexity.cuts", func(c *Config) { c.Policy.Complexity.Cuts = nil }, "policy.complexity.cuts is required"},
		{"missing cuts.complex", func(c *Config) { c.Policy.Complexity.Cuts.Complex = nil }, "policy.complexity.cuts.complex is required"},
		{"missing cuts.medium", func(c *Config) { c.Policy.Complexity.Cuts.Medium = nil }, "policy.complexity.cuts.medium is required"},
		{"cuts.complex above 2", func(c *Config) { c.Policy.Complexity.Cuts.Complex = float64Ptr(2.1) }, "0 < medium < complex <= 2"},
		{"cuts.medium at 0", func(c *Config) { c.Policy.Complexity.Cuts.Medium = float64Ptr(0) }, "0 < medium < complex <= 2"},
		{"cuts out of order", func(c *Config) { c.Policy.Complexity.Cuts.Medium = float64Ptr(1.5) }, "0 < medium < complex <= 2"},
		{"old complexity.medium", func(c *Config) { c.Policy.Complexity.Medium = 0.7 }, "policy.complexity.medium was replaced by policy.complexity.cuts"},
		{"old complexity.complex", func(c *Config) { c.Policy.Complexity.Complex = 0.55 }, "policy.complexity.complex was replaced by policy.complexity.cuts"},
		{"missing stage threshold", func(c *Config) {
			delete(c.Policy.Stages.Thresholds, "analysis")
		}, "policy.stages.thresholds.analysis is required"},
		{"stage threshold out of range", func(c *Config) {
			c.Policy.Stages.Thresholds["analysis"] = 1.5
		}, "policy.stages.thresholds.analysis must be in [0, 1]"},
		{"threshold of a stage no longer asked", func(c *Config) {
			c.Policy.Stages.Thresholds["review"] = 0.7
		}, "policy.stages.thresholds.review: review is not asked any more"},
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
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}

	// A threshold of exactly 0 must be accepted as a deliberate (if extreme) choice,
	// distinct from an omitted one.
	cfg := validConfig()
	cfg.Policy.ContextDepth.ProjectThreshold = float64Ptr(0)
	cfg.Policy.Stages.Thresholds["analysis"] = 0
	if err := Validate(cfg); err != nil {
		t.Errorf("threshold of exactly 0 rejected: %v", err)
	}
}

func TestValidateDecision_Explore(t *testing.T) {
	bad := []struct {
		name    string
		explore ExploreConfig
		wantErr string
	}{
		{"zero band", ExploreConfig{Band: 0, Rate: 0.1}, "policy.explore.band must be in (0, 0.2]"},
		{"negative band", ExploreConfig{Band: -0.05, Rate: 0.1}, "policy.explore.band must be in (0, 0.2]"},
		{"band above 0.2", ExploreConfig{Band: 0.25, Rate: 0.1}, "policy.explore.band must be in (0, 0.2]"},
		{"NaN band", ExploreConfig{Band: math.NaN(), Rate: 0.1}, "policy.explore.band must be in (0, 0.2]"},
		{"zero rate", ExploreConfig{Band: 0.05, Rate: 0}, "policy.explore.rate must be in (0, 0.5]"},
		{"rate above 0.5", ExploreConfig{Band: 0.05, Rate: 0.6}, "policy.explore.rate must be in (0, 0.5]"},
		{"NaN rate", ExploreConfig{Band: 0.05, Rate: math.NaN()}, "policy.explore.rate must be in (0, 0.5]"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			explore := tt.explore
			cfg.Policy.Explore = &explore
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}

	cfg := validConfig()
	if got := cfg.DecisionPolicy().Explore; got != nil {
		t.Errorf("DecisionPolicy().Explore = %+v without policy.explore, want nil", got)
	}
	for _, e := range []ExploreConfig{{Band: 0.2, Rate: 0.5}, {Band: 0.05, Rate: 0.1}} {
		cfg.Policy.Explore = &ExploreConfig{Band: e.Band, Rate: e.Rate}
		if err := Validate(cfg); err != nil {
			t.Errorf("explore %+v rejected: %v", e, err)
		}
		got := cfg.DecisionPolicy().Explore
		if got == nil || got.Band != e.Band || got.Rate != e.Rate {
			t.Errorf("DecisionPolicy().Explore = %+v, want %+v", got, e)
		}
	}
}

func TestValidateDecision_Context(t *testing.T) {
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
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDecision_Research(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Research.Provider = ""
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "research.provider is required") {
		t.Errorf("Validate() = %v, want error containing research.provider is required", err)
	}
}

func TestValidateDecision_AggregatesAllProblems(t *testing.T) {
	cfg := validConfig()
	cfg.Decision.Jev.BaseURL = ""
	cfg.Translation.Provider = ""
	cfg.Context.MaxFiles = 0

	err := Validate(cfg)
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
	cfg := validConfig()
	p := cfg.DecisionPolicy()

	if p.ProjectContextThreshold != 0.5 {
		t.Errorf("ProjectContextThreshold = %v, want 0.5", p.ProjectContextThreshold)
	}
	if got := len(p.StageThreshold); got != 3 {
		t.Fatalf("StageThreshold has %d entries, want 3", got)
	}
	if p.StageThreshold["research"] != 0.70 {
		t.Errorf(`StageThreshold["research"] = %v, want 0.70`, p.StageThreshold["research"])
	}
	if p.ComplexityCuts != (domain.ComplexityCuts{Medium: 0.50, Complex: 1.40}) {
		t.Errorf("ComplexityCuts = %+v", p.ComplexityCuts)
	}
	force := p.ForceStages["complex"]
	if len(force) != 3 {
		t.Fatalf("ForceStages[complex] = %v, want 3 stages", force)
	}
}
