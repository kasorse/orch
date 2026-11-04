package integration

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 11 (MVP.md §23): reproduces the originally reported bug directly -
// `claude --print` writes backend/API errors to stdout, not stderr. Before the
// fix, ProviderError only carried Stderr, so this exact failure rendered as an
// information-free "provider execution failed" with a blank stderr. It must now surface
// the stdout text, classify as a plain exit failure, and render stderr as "(empty)".
func TestScenario11_ProviderFailureWritesToStdoutNotStderr(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDOUT", "API Error: overloaded_error - the backend is temporarily overloaded")

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
	if provErr.Kind != apperr.FailureExit {
		t.Errorf("Kind = %q, want %q", provErr.Kind, apperr.FailureExit)
	}
	if !strings.Contains(provErr.Stdout, "overloaded_error") {
		t.Errorf("Stdout = %q, want it to contain the API error text", provErr.Stdout)
	}
	if provErr.Stderr != "" {
		t.Errorf("Stderr = %q, want empty (nothing was written to stderr)", provErr.Stderr)
	}

	msg := err.Error()
	if !strings.Contains(msg, "overloaded_error") {
		t.Errorf("rendered error message does not contain the stdout failure text: %s", msg)
	}
	if !strings.Contains(msg, "stderr:\n(empty)") {
		t.Errorf("rendered error message does not render stderr as (empty): %s", msg)
	}
}
