package config

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Version:    1,
		Classifier: ProviderModel{Provider: "claude", Model: "sonnet"},
		Actions: ActionsConfig{
			Analyze:   ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
			Plan:      ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "sonnet"}},
			Implement: ActionConfig{ProviderModel: ProviderModel{Provider: "claude", Model: "opus"}},
			Review:    ActionConfig{ProviderModel: ProviderModel{Provider: "codex", Model: "gpt"}},
			Answer:    ActionConfig{ProviderModel: ProviderModel{Provider: "gemini", Model: "pro"}},
		},
		Test:      TestConfig{Command: "go test ./..."},
		Limits:    LimitsConfig{MaxSteps: 12, MaxFixIterations: 2},
		Execution: ExecutionConfig{Timeout: 15 * time.Minute},
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	cfg := validConfig()
	cfg.Classifier = ProviderModel{}
	cfg.Test.Command = ""

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"classifier.provider is required", "classifier.model is required", "test.command is required"} {
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
	cfg.Version = 2

	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unsupported config version 2") {
		t.Errorf("unexpected error message: %s", err.Error())
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
	for _, action := range []string{"classifier", "analyze", "plan", "implement", "review", "answer"} {
		if !strings.Contains(msg, action+".provider is required") {
			t.Errorf("expected aggregated error to mention %s.provider, got: %s", action, msg)
		}
	}
}
