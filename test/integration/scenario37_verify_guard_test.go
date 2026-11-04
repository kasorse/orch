package integration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 37, the guard before every verify call (specs/quality/SPEC.md §6.6): the second
// implement plants a git hook and then calls run_tests. The server refuses the call without
// running anything, and the step fails on the protected path. The planted hook writes a marker
// file when it runs; it must never run, so the marker must not exist.

func TestScenario37_PlantedHookIsNeverExecuted(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	v := newVerifyRun(t, repoDir, "run_tests")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	t.Setenv("FAKE_HOOK_MARKER", marker)
	// The first implement leaves the tests red, so implement runs a second time: that is the
	// call that plants the hook.
	neverFix(t)
	t.Setenv("FAKE_PLANT_HOOK_BEFORE_CALL", "2")

	_, _, err := runOrchestrator(t, repoDir, v.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a WorkflowError (exit 4), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(), "implement changed protected paths", ".git (config or hooks)", "Restore point:")

	// The hook is there, planted, and nothing ran it.
	if _, statErr := os.Stat(filepath.Join(repoDir, ".git", "hooks", "pre-commit")); statErr != nil {
		t.Fatalf("the fake did not plant the hook, so this proves nothing: %v", statErr)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the planted hook ran")
	}

	if got := invocations(t, v.invocationsLog); !equalStrings(got, []string{"implement", "implement"}) {
		t.Errorf("invocations = %v", got)
	}
	run := loadQL(t, repoDir)
	if run.Status != "failed" || !equalStrings(run.actions(), []string{"implement", "test", "implement"}) {
		t.Fatalf("status = %s, executions = %v", run.Status, run.actions())
	}

	// The first implement's call ran the red tests; the second's was refused, and is recorded
	// although the step failed.
	if got := run.Executions[0].VerifyCalls; len(got) != 1 || got[0].Tool != "run_tests" || got[0].Refused != "" || got[0].Success {
		t.Errorf("the first implement's verify_calls = %+v", got)
	}
	second := run.Executions[2]
	if second.Status != "failed" || len(second.VerifyCalls) != 1 || second.VerifyCalls[0].Refused != "protected files changed" {
		t.Fatalf("the second implement = %+v", second)
	}
	if second.VerifyCalls[0].Success || second.VerifyCalls[0].ExitCode != 0 {
		t.Errorf("a refused call ran nothing, but it recorded a result: %+v", second.VerifyCalls[0])
	}
}
