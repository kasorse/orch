package integration

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 13 (SPEC-diagnostics.md §6.6): execution.timeout must wrap the decision
// phase, not just the workflow engine. A short timeout plus an LLM decision provider CLI
// that sleeps well past it must produce a *apperr.ProviderError{Kind: FailureTimeout}
// promptly, not hang until the test itself times out.
func TestScenario13_DecisionTimeout(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_SLEEP_ACTION", "decide-context")
	t.Setenv("FAKE_SLEEP_SECONDS", "5")

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	opts.timeout = "500ms"

	configDir := t.TempDir()
	configPath := writeConfig(t, configDir, opts)

	start := time.Now()
	_, _, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	elapsed := time.Since(start)

	// FAKE_SLEEP_SECONDS (5s) is deliberately far longer than execution.timeout (500ms) -
	// a broken timeout wiring would make this take ~5s; a working one returns in well
	// under that once the timeout fires and the child process is killed.
	if elapsed > 3*time.Second {
		t.Errorf("run took %s, want it to fail promptly once execution.timeout (500ms) elapses, not wait out FAKE_SLEEP_SECONDS", elapsed)
	}

	if err == nil {
		t.Fatal("expected the run to fail, got nil error")
	}

	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T: %v", err, err)
	}
	if provErr.Kind != apperr.FailureTimeout {
		t.Errorf("Kind = %q, want %q", provErr.Kind, apperr.FailureTimeout)
	}
	if provErr.Action != "decide-context" {
		t.Errorf("Action = %q, want decide-context", provErr.Action)
	}
}
