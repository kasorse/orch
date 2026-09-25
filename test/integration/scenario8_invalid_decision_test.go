package integration

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 8 (MVP.md §29): the decision provider answers with something that is not a
// decision. With an LLM decision provider (TASKS.md T22) and on_failure: fail, that is
// an InvalidResponse decision error (exit 3) - the pre-check is the only AI call, and
// the fixture repository is left untouched.
func TestScenario8_InvalidDecisionResponse(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	writeResponse(t, responsesDir, "decide-context", `this is not JSON at all`)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	configPath := writeConfig(t, t.TempDir(), opts)

	_, _, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err == nil {
		t.Fatal("expected the run to fail, got nil error")
	}

	var decErr *apperr.DecisionError
	if !errors.As(err, &decErr) || decErr.Kind != apperr.DecisionInvalidResponse || decErr.Provider != "claude" {
		t.Fatalf("expected a claude invalid_response DecisionError, got %T: %v", err, err)
	}
	if !strings.Contains(decErr.Body, "this is not JSON at all") {
		t.Errorf("Body = %q, want the model's output kept for diagnosis", decErr.Body)
	}
	if code := exitcode.FromError(err); code != exitcode.ProviderExecution {
		t.Errorf("exit code = %d, want %d", code, exitcode.ProviderExecution)
	}

	want := []string{"decide-context"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (nothing beyond the pre-check)", got, want)
	}

	assertRepoUnchanged(t, repoDir)
}

// assertRepoUnchanged fails the test if the git fixture repository's tracked source
// files have any uncommitted changes, confirming no repository-changing action ran.
// The orchestrator's own ".orchestrator/" run-state directory is expected to appear as
// untracked (it is app bookkeeping, not a change to the repository under test) and is
// ignored here.
func assertRepoUnchanged(t *testing.T, repoDir string) {
	t.Helper()
	out, err := runGitStatus(repoDir)
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasSuffix(line, ".orchestrator/") {
			continue
		}
		t.Errorf("expected a clean fixture repository, got git status --porcelain output:\n%s", out)
		return
	}
}

func runGitStatus(dir string) (string, error) {
	return gitOutput(dir, "status", "--porcelain")
}
