package config

import (
	"strings"
	"testing"
)

func TestValidateDependencies_AllInstalled(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{}}
	if err := ValidateDependencies(validConfig(), checker); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDependencies_ClaudeMissing(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{"claude": true}}
	err := ValidateDependencies(validConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"claude"`) || !strings.Contains(msg, "was not found in PATH") {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestValidateDependencies_CodexMissing(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{"codex": true}}
	err := ValidateDependencies(validConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `provider "codex" is configured for action "review"`) {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_GeminiMissing(t *testing.T) {
	checker := FakeDependencyChecker{Missing: map[string]bool{"gemini": true}}
	err := ValidateDependencies(validConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"gemini"`) {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_ProviderUsedByMultipleActions(t *testing.T) {
	cfg := validConfig()
	cfg.Actions.Review.Provider = "claude"
	cfg.Actions.Review.Model = "sonnet"
	// claude now backs classifier, analyze, plan, implement, and review.

	checker := FakeDependencyChecker{Missing: map[string]bool{"claude": true}}
	err := ValidateDependencies(cfg, checker)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `provider "claude" is required by actions [`) {
		t.Errorf("expected aggregated multi-action message, got: %s", msg)
	}
	if !strings.Contains(msg, "review") || !strings.Contains(msg, "analyze") {
		t.Errorf("expected aggregated actions to include review and analyze, got: %s", msg)
	}
}

func TestValidateDependencies_ClassifierProviderMissing(t *testing.T) {
	cfg := validConfig()
	cfg.Classifier.Provider = "gemini"
	cfg.Classifier.Model = "pro"
	// gemini now backs classifier and answer; make it missing.

	checker := FakeDependencyChecker{Missing: map[string]bool{"gemini": true}}
	err := ValidateDependencies(cfg, checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "classifier") {
		t.Errorf("expected classifier to be flagged as a using action, got: %s", err.Error())
	}
}
