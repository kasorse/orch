package integration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/exitcode"
	"orchestrator/internal/persistence"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 18, continuing (specs/resume/SPEC.md §6, §6.2, §11): a completed run is
// reported, not re-run; run IDs resolve by full ID or a unique prefix of at least 8
// characters; and a run another process holds is refused.

const analyzeAnswerResponse = `{
  "summary": "Add() subtracts instead of adding.",
  "root_cause": "wrong operator",
  "affected_files": ["fixture.go"],
  "recommendation": "use + instead of -",
  "risks": [],
  "answer": "Add returns a - b; it should return a + b."
}`

// completedReadOnlyRun runs an analysis-only task to completion and returns it.
func completedReadOnlyRun(t *testing.T) (repoDir, cfg, invocationsLog string, run resumedRun) {
	t.Helper()
	repoDir = fakeexec.InitGitFixture(t)
	invocationsLog, _ = decisionEnv(t, repoDir)
	writeResponse(t, os.Getenv("FAKE_RESPONSES_DIR"), "analyze", analyzeAnswerResponse)
	cfg = jevConfig(t, startJev(t, scriptAnalysisOnly()))

	stdout, stderr, err := runOrchestrator(t, repoDir, cfg, "run", "explain Add in fixture.go")
	mustSucceed(t, stdout, stderr, err)
	run = loadResumedRun(t, repoDir)
	if run.Status != "completed" {
		t.Fatalf("status = %s, want completed", run.Status)
	}
	return repoDir, cfg, invocationsLog, run
}

func TestScenario18_ResumeCompletedRun(t *testing.T) {
	repoDir, cfg, invocationsLog, run := completedReadOnlyRun(t)
	calls := len(invocations(t, invocationsLog))
	before, _ := os.ReadFile(runJSONPath(t, repoDir))

	stdout, stderr, err := resume(t, repoDir, cfg, run.ID)
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "already completed") || !strings.Contains(stdout, "Answer:\nAdd returns a - b; it should return a + b.") {
		t.Errorf("expected the completed report and the answer again:\n%s", stdout)
	}
	assertRefusedUnchanged(t, repoDir, invocationsLog, calls, before)
}

func TestScenario18_ResumeRunIDs(t *testing.T) {
	repoDir, cfg, invocationsLog, run := completedReadOnlyRun(t)
	calls := len(invocations(t, invocationsLog))

	stdout, stderr, err := resume(t, repoDir, cfg, run.ID[:8])
	mustSucceed(t, stdout, stderr, err)
	if !strings.Contains(stdout, "Run "+run.ID+" already completed") {
		t.Errorf("a unique 8-character prefix must resolve to the run:\n%s", stdout)
	}

	notFound := func(t *testing.T, arg string) {
		t.Helper()
		_, _, err := resume(t, repoDir, cfg, arg)
		var stateErr *apperr.RunStateError
		if !errors.As(err, &stateErr) || stateErr.Kind != apperr.RunNotFound || exitcode.FromError(err) != exitcode.GenericError {
			t.Errorf("resume %q: expected RunStateError{not_found} (exit 1), got %T: %v", arg, err, err)
		}
	}
	t.Run("prefix too short", func(t *testing.T) { notFound(t, run.ID[:7]) })
	t.Run("unknown ID", func(t *testing.T) { notFound(t, "00000000-0000-4000-8000-000000000000") })
	t.Run("not an ID", func(t *testing.T) { notFound(t, "../../etc") })

	t.Run("ambiguous prefix", func(t *testing.T) {
		// A second run directory sharing the first 8 characters.
		other := run.ID[:8] + "-ffff-4fff-8fff-ffffffffffff"
		if other == run.ID {
			t.Skip("the random run ID collides with the fixture's")
		}
		if err := os.MkdirAll(filepath.Join(repoDir, ".orchestrator", "runs", other), 0o755); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(filepath.Join(repoDir, ".orchestrator", "runs", other))

		_, _, err := resume(t, repoDir, cfg, run.ID[:8])
		var stateErr *apperr.RunStateError
		if !errors.As(err, &stateErr) || stateErr.Kind != apperr.RunAmbiguous || exitcode.FromError(err) != exitcode.GenericError {
			t.Fatalf("expected RunStateError{ambiguous} (exit 1), got %T: %v", err, err)
		}
		if !strings.Contains(err.Error(), run.ID) || !strings.Contains(err.Error(), other) {
			t.Errorf("the refusal must list both runs:\n%v", err)
		}
	})

	if got := len(invocations(t, invocationsLog)); got != calls {
		t.Errorf("resolving run IDs called a provider (%d calls, was %d)", got, calls)
	}
}

func TestScenario18_ResumeRunInUse(t *testing.T) {
	repoDir, cfg, invocationsLog, run := completedReadOnlyRun(t)
	calls := len(invocations(t, invocationsLog))

	// Another orchestrator process holding the run: its lock is on the run directory.
	unlock, err := persistence.NewStore(filepath.Join(repoDir, ".orchestrator", "runs")).Lock(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	_, _, err = resume(t, repoDir, cfg, run.ID)
	var stateErr *apperr.RunStateError
	if !errors.As(err, &stateErr) || stateErr.Kind != apperr.RunInUse || exitcode.FromError(err) != exitcode.GenericError {
		t.Fatalf("expected RunStateError{in_use} (exit 1), got %T: %v", err, err)
	}
	if got := len(invocations(t, invocationsLog)); got != calls {
		t.Errorf("a refused resume called a provider")
	}

	unlock()
	stdout, stderr, err := resume(t, repoDir, cfg, run.ID)
	mustSucceed(t, stdout, stderr, err)
}
