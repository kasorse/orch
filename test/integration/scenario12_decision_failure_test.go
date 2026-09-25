package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 12 (SPEC-diagnostics.md): the decision provider's own CLI call fails (an
// LLM decision provider, TASKS.md T22). The failure must be logged under
// logs/decide-context.*.log, tagged with Action == "decide-context", wrapped in a
// DecisionError, and mapped to the same exitcode.ProviderExecution (3) a workflow-step
// failure gets.
func TestScenario12_DecisionProviderFailure(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_FAIL_ACTION", "decide-context")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDERR", "boom: decision provider crashed")

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	configPath := writeConfig(t, t.TempDir(), opts)

	_, _, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err == nil {
		t.Fatal("expected the run to fail, got nil error")
	}

	var decErr *apperr.DecisionError
	if !errors.As(err, &decErr) || decErr.Kind != apperr.DecisionUnavailable {
		t.Fatalf("expected an unavailable DecisionError, got %T: %v", err, err)
	}
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected the *apperr.ProviderError inside, got %v", err)
	}
	if provErr.Action != "decide-context" {
		t.Errorf("Action = %q, want decide-context", provErr.Action)
	}
	if !strings.Contains(provErr.LogPath, "decide-context.stderr.log") {
		t.Fatalf("LogPath = %q, want it to name decide-context.stderr.log", provErr.LogPath)
	}

	logContent, readErr := os.ReadFile(filepath.Join(repoDir, provErr.LogPath))
	if readErr != nil {
		t.Fatalf("read LogPath %q: %v", provErr.LogPath, readErr)
	}
	if !strings.Contains(string(logContent), "boom: decision provider crashed") {
		t.Errorf("log file %s = %q, want it to contain the simulated crash message", provErr.LogPath, logContent)
	}

	if code := exitcode.FromError(err); code != exitcode.ProviderExecution {
		t.Errorf("exitcode.FromError = %d, want %d (ProviderExecution)", code, exitcode.ProviderExecution)
	}

	want := []string{"decide-context"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (the workflow never builds/runs on a decision failure)", got, want)
	}
}
