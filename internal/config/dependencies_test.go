package config

import (
	"os"
	"strings"
	"testing"
)

func TestValidateDependencies_AllInstalled(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
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
	// claude now backs analyze, plan, implement, review, and translation.

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

func TestValidateDependencies_DecisionValid(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}
	if err := ValidateDependencies(validConfig(), checker); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDependencies_DecisionAPIKeyEnvNotSet(t *testing.T) {
	// t.Setenv registers cleanup that restores whatever the variable held before this
	// test; os.Unsetenv then removes it for the duration of the test so LookupEnv
	// reports ok=false, which a value of "" would not.
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDependencies(validConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "jev decision provider: JEV_API_KEY is not configured") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_DecisionAPIKeyEnvBlank(t *testing.T) {
	t.Setenv("JEV_API_KEY", "   ")
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDependencies(validConfig(), checker)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "JEV_API_KEY is not configured") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestValidateDependencies_DecisionTranslationExecutableMissing(t *testing.T) {
	t.Setenv("JEV_API_KEY", "test-key")
	cfg := validConfig()
	cfg.Translation.Provider = "gemini"
	checker := FakeDependencyChecker{Missing: map[string]bool{"gemini": true}}

	err := ValidateDependencies(cfg, checker)
	if err == nil {
		t.Fatal("expected error")
	}
	// research and translation both use gemini, so they are reported together - the
	// translator is checked even though an English request would never call it.
	if !strings.Contains(err.Error(), `provider "gemini" is required by actions [research, translation]`) {
		t.Errorf("expected aggregated message, got: %s", err.Error())
	}
}

func TestValidateDependencies_DecisionDoesNotCheckAPIKeyWhenEnvNameIsBlank(t *testing.T) {
	// ValidateDecisionConfig (structural) is responsible for flagging a blank
	// api_key_env; ValidateDecisionConfigDependencies must not also report a
	// confusing "" is not configured message for it.
	cfg := validConfig()
	cfg.Decision.Jev.APIKeyEnv = ""
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	err := ValidateDependencies(cfg, checker)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateDependencies_DecisionMockNeedsNoAPIKey(t *testing.T) {
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	cfg := validConfig()
	cfg.Decision.Jev.Mock = true
	checker := FakeDependencyChecker{Missing: map[string]bool{}}

	if err := ValidateDependencies(cfg, checker); err != nil {
		t.Fatalf("expected mock mode to pass without an API key, got: %v", err)
	}
}

func TestValidateDependencies_DecisionLLMProvider(t *testing.T) {
	t.Setenv("JEV_API_KEY", "sentinel")
	os.Unsetenv("JEV_API_KEY")
	cfg := validConfig()
	cfg.Decision.Provider = "codex"
	cfg.Decision.Model = "gpt"

	if err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{}}); err != nil {
		t.Fatalf("an LLM provider needs no JEV_API_KEY: %v", err)
	}

	err := ValidateDependencies(cfg, FakeDependencyChecker{Missing: map[string]bool{"codex": true}})
	if err == nil || !strings.Contains(err.Error(), "decision") || !strings.Contains(err.Error(), "codex") {
		t.Errorf("a missing decision executable must fail before any AI call: %v", err)
	}
}
