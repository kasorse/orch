package integration

import (
	"errors"
	"path/filepath"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 9 (MVP.md §29): max fix iterations reached. The implement fake never fixes
// the fixture, so the workflow loops implement->test(fail) until max_fix_iterations is
// hit, and stops before ever invoking review.
func TestScenario9_MaxFixIterationsReached(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	stateDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	writeResponse(t, responsesDir, "classify", classifyBugfixLow)
	writeResponse(t, responsesDir, "implement", implementResponse)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("FAKE_STATE_DIR", stateDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_IMPLEMENT_FIX_ON_CALL", "999")

	opts := defaultConfigOptions()
	opts.maxFixIterations = 1

	configDir := t.TempDir()
	configPath := writeConfig(t, configDir, opts)

	_, _, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err == nil {
		t.Fatal("expected the run to fail, got nil error")
	}

	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}

	want := []string{"classify", "implement", "implement"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (review must never be invoked)", got, want)
	}
}
