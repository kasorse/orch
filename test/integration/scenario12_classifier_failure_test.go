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

// Scenario 12 (SPEC-diagnostics.md): the classifier's own provider call fails. Before this
// spec's fix, execution.timeout didn't cover classification and a failing classification
// call was not logged at all (only workflow steps were). The failure must be logged under
// logs/classify.*.log, tagged with Action == "classify" (not blank), and mapped to the same
// exitcode.ProviderExecution (3) a workflow-step failure gets.
func TestScenario12_ClassifierProviderFailure(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_FAIL_ACTION", "classify")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDERR", "boom: classifier crashed")

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
	if provErr.Action != "classify" {
		t.Errorf("Action = %q, want classify", provErr.Action)
	}
	if provErr.LogPath == "" {
		t.Fatal("LogPath is empty, want it to point at the persisted classify log")
	}
	if !strings.Contains(provErr.LogPath, "classify.stderr.log") {
		t.Errorf("LogPath = %q, want it to name classify.stderr.log", provErr.LogPath)
	}

	logContent, readErr := os.ReadFile(filepath.Join(repoDir, provErr.LogPath))
	if readErr != nil {
		t.Fatalf("read LogPath %q: %v", provErr.LogPath, readErr)
	}
	if !strings.Contains(string(logContent), "boom: classifier crashed") {
		t.Errorf("log file %s = %q, want it to contain the simulated crash message", provErr.LogPath, logContent)
	}

	if code := exitcode.FromError(err); code != exitcode.ProviderExecution {
		t.Errorf("exitcode.FromError = %d, want %d (ProviderExecution)", code, exitcode.ProviderExecution)
	}

	want := []string{"classify"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (workflow never builds/runs on a classification failure)", got, want)
	}
}
