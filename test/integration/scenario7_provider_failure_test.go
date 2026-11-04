package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 7 (MVP.md §29): provider runtime failure. The run ends as failed, and the
// returned error carries provider/action/exit-code/stderr context (not just a generic
// message).
func TestScenario7_ProviderRuntimeFailure(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "7")
	t.Setenv("FAKE_FAIL_STDERR", "boom: simulated provider crash")

	configDir := t.TempDir()
	opts := defaultConfigOptions()
	opts.jevURL = startJev(t, scriptBugfixLow()).URL()
	configPath := writeConfig(t, configDir, opts)

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
	if provErr.LogPath == "" {
		t.Fatal("LogPath is empty, want it to point at the persisted stderr log")
	}
	logContent, readErr := os.ReadFile(filepath.Join(repoDir, provErr.LogPath))
	if readErr != nil {
		t.Fatalf("read LogPath %q: %v", provErr.LogPath, readErr)
	}
	if !strings.Contains(string(logContent), "boom: simulated provider crash") {
		t.Errorf("log file %s = %q, want it to contain the simulated crash message", provErr.LogPath, logContent)
	}
	if !strings.Contains(err.Error(), "boom: simulated provider crash") {
		t.Errorf("rendered error message does not contain the simulated crash message: %s", err.Error())
	}

	want := []string{"implement"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (no further AI call after the failing one)", got, want)
	}
}
