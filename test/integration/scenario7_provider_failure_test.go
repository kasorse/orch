package integration

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 7 (MVP.md §29): provider runtime failure. The run ends as failed, and the
// returned error carries provider/action/exit-code/stderr context (not just a generic
// message).
func TestScenario7_ProviderRuntimeFailure(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	writeResponse(t, responsesDir, "classify", classifyBugfixLow)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "7")
	t.Setenv("FAKE_FAIL_STDERR", "boom: simulated provider crash")

	configDir := t.TempDir()
	configPath := writeConfig(t, configDir, defaultConfigOptions())

	_, _, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err == nil {
		t.Fatal("expected the run to fail, got nil error")
	}

	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T: %v", err, err)
	}
	if provErr.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", provErr.Provider)
	}
	if provErr.Action != "implement" {
		t.Errorf("Action = %q, want implement", provErr.Action)
	}
	if provErr.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", provErr.ExitCode)
	}
	if !strings.Contains(provErr.Stderr, "boom: simulated provider crash") {
		t.Errorf("Stderr = %q, want it to contain the simulated crash message", provErr.Stderr)
	}

	want := []string{"classify", "implement"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (no further AI call after the failing one)", got, want)
	}
}
