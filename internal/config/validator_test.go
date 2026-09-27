package config

import (
	"strings"
	"testing"
	"time"
)

// validConfig returns a fully valid version 2 config, matching the example in
// specs/JEV integration/TASKS.md T02/T22.
func validConfig() Config {
	return Config{
		Version: CurrentVersion,
		Actions: ActionsConfig{
			Research:  ActionConfig{ProviderModel: ProviderModel{Provider: "gemini", Model: "pro"}},
			Analyze:   ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
			Plan:      ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
			Implement: ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "opus"}},
			Review:    ActionConfig{ProviderModel: ProviderModel{Provider: "codex", Model: "gpt"}},
		},
		Test:      TestConfig{Command: "go test ./..."},
		Limits:    LimitsConfig{MaxSteps: 12, MaxFixIterations: 2},
		Execution: ExecutionConfig{Timeout: 15 * time.Minute},
		Decision: DecisionConfig{
			Provider:   DecisionProviderJev,
			Model:      "jev-1.13.0",
			Timeout:    5 * time.Second,
			Complexity: ComplexityQuestionsConfig{Primary: "choice", Shadow: true},
			OnFailure:  OnFailureAsk,
			Jev: JevConfig{
				BaseURL:    "https://api.typesafe.ai",
				APIKeyEnv:  "JEV_API_KEY",
				MaxRetries: 1,
			},
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
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Research = ActionConfig{}
	cfg.Test.Command = ""

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"research.provider is required", "research.model is required", "test.command is required"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected error to contain %q, got: %s", want, msg)
		}
	}
}

func TestValidate_UnsupportedProvider(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Provider = "openrouter"

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `review.provider "openrouter" is not a supported provider`) {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidate_InvalidLimits(t *testing.T) {
	cfg := validConfig()
	cfg.Limits.MaxSteps = 0
	cfg.Limits.MaxFixIterations = -1

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "max_steps must be positive") || !strings.Contains(msg, "max_fix_iterations must be positive") {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestValidate_NegativeActionTimeout(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Timeout = -1 * time.Minute

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "actions.review.timeout must not be negative, got -1m0s") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidate_InvalidVersion(t *testing.T) {
	cfg := validConfig()
	cfg.Version = 3

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported config version 3 (expected 2)") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidate_Version1ExplainsTheChanges(t *testing.T) {
	cfg := validConfig()
	cfg.Version = 1

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"unsupported config version 1 (expected 2)", "classifier", "decision", "actions.answer", "orchestrator.example.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %s", want, err.Error())
		}
	}
}

func TestValidate_AggregatesAllProblems(t *testing.T) {
	cfg := Config{}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	// Every missing action's provider/model should be reported, not just the first.
	for _, action := range []string{"research", "analyze", "plan", "implement", "review", "translation"} {
		if !strings.Contains(msg, action+".provider is required") {
			t.Errorf("expected aggregated error to mention %s.provider, got: %s", action, msg)
		}
	}
}

func TestValidate_ProtectedPaths(t *testing.T) {
	cfg := validConfig()
	cfg.Security.ProtectedPaths = []string{"go.mod", "/etc/hosts", "["}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected invalid protected_paths to fail")
	}
	for _, want := range []string{`security.protected_paths[1] "/etc/hosts"`, `security.protected_paths[2] "["`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), `"go.mod"`) {
		t.Errorf("go.mod is a valid pattern:\n%v", err)
	}
}
