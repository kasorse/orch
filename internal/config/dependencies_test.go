package config

import (
	"os"
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

func TestValidateDecisionConfigDependencies_Valid(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}
	if err := ValidateDecisionConfigDependencies(validDecisionConfig(), checker); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDecisionConfigDependencies_APIKeyEnvNotSet(t *testing.T) {
	// t.Setenv registers cleanup that restores whatever the variable held before this
	// test; os.Unsetenv then removes it for the duration of the test so LookupEnv
	// reports ok=false, which a value of "" would not.
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDecisionConfigDependencies(validDecisionConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "jev decision provider: JEV_API_KEY is not configured") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDecisionConfigDependencies_APIKeyEnvBlank(t *testing.T) {
	t.Setenv("JEV_API_KEY", "   ")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDecisionConfigDependencies(validDecisionConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "JEV_API_KEY is not configured") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDecisionConfigDependencies_TranslationExecutableMissing(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	checker := FakeDependencyChecker{Missing: map[string]bool{"claude": true}}

	err := ValidateDecisionConfigDependencies(validDecisionConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	// translation and research both use claude in validDecisionConfig(), so they are
	// reported together as one aggregated "required by actions" message.
	if !strings.Contains(err.Error(), `provider "claude" is required by actions [`) {
		t.Errorf("expected aggregated message, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "translation") || !strings.Contains(err.Error(), "research") {
		t.Errorf("expected both translation and research to be named, got: %s", err.Error())
	}
}

func TestValidateDecisionConfigDependencies_DoesNotCheckAPIKeyWhenEnvNameIsBlank(t *testing.T) {
	// ValidateDecisionConfig (structural) is responsible for flagging a blank
	// api_key_env; ValidateDecisionConfigDependencies must not also report a
	// confusing "" is not configured message for it.
	cfg := validDecisionConfig()
	cfg.Decision.Jev.APIKeyEnv = ""
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDecisionConfigDependencies(cfg, checker)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
