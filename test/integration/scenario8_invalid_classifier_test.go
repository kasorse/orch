package integration

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 8 (MVP.md §29): invalid classifier response. The run must fail before any
// repository-changing action - i.e. classify is the only AI call, and the fixture
// repository is left exactly as committed.
func TestScenario8_InvalidClassifierResponse(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	installFakeProviders(t, "claude", "codex", "gemini")

	responsesDir := t.TempDir()
	invocationsLog := filepath.Join(t.TempDir(), "invocations.log")

	writeResponse(t, responsesDir, "classify", `this is not JSON at all`)

	t.Setenv("FAKE_RESPONSES_DIR", responsesDir)
	t.Setenv("INVOCATIONS_LOG", invocationsLog)

	configDir := t.TempDir()
	configPath := writeConfig(t, configDir, defaultConfigOptions())

	_, _, err := runOrchestrator(t, repoDir, configPath, "run", "fix Add() to add instead of subtract")
	if err == nil {
		t.Fatal("expected the run to fail, got nil error")
	}

	var classErr *apperr.ClassificationError
	if !errors.As(err, &classErr) {
		t.Fatalf("expected *apperr.ClassificationError, got %T: %v", err, err)
	}

	want := []string{"classify"}
	if got := invocations(t, invocationsLog); !equalStrings(got, want) {
		t.Errorf("invocations = %v, want %v (no action beyond classify)", got, want)
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
