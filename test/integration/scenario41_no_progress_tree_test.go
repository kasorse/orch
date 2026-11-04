package integration

import (
	"errors"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/exitcode"
	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 41, no progress: the tree (specs/quality/SPEC.md §8.1). An implement that leaves the
// working tree as the previous implement left it ends the run, before the test step is spent on
// it, with status stopped and the reason. A resume goes into implement again.

// stoppedForNoProgress asserts err is the no-progress stop with reason (exit 4, the way out
// named) and that run.json says the same.
func stoppedForNoProgress(t *testing.T, err error, repoDir, reason string) qlRun {
	t.Helper()
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || exitcode.FromError(err) != exitcode.WorkflowOrLimit {
		t.Fatalf("expected a LimitExceededError (exit 4), got %T: %v", err, err)
	}
	assertContains(t, "the error", err.Error(), "Run status: stopped", reason, "limits.stop_without_progress: false")
	run := loadQL(t, repoDir)
	if run.Status != "stopped" || run.FailureReason != reason {
		t.Errorf("status = %s, failure_reason = %q, want stopped / %q", run.Status, run.FailureReason, reason)
	}
	return run
}

func TestScenario41_ImplementChangesNothingTwice(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.stopWithoutProgress = true })
	neverFix(t)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	run := stoppedForNoProgress(t, err, repoDir, "no progress: implement changed nothing")

	// Stopped right after the second implement: no test step was spent on it, and no third
	// attempt began.
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement"}) {
		t.Errorf("invocations = %v", got)
	}
	if want := []string{"implement", "test", "implement"}; !equalStrings(run.actions(), want) {
		t.Fatalf("executions = %v, want %v", run.actions(), want)
	}
	first, second := run.Executions[0], run.Executions[2]
	if first.TreeAfter == "" || first.TreeAfter != second.TreeAfter {
		t.Errorf("tree_after = %q then %q, want the same git tree", first.TreeAfter, second.TreeAfter)
	}
	if run.FixIterations != 1 {
		t.Errorf("fix_iterations = %d, want 1: the stop is not a spent iteration", run.FixIterations)
	}

	// The operator resuming means "try again": the run goes into implement, which now fixes it.
	fixOnCall(t, 1)
	stdout, stderr, err := resume(t, repoDir, r.cfg, run.ID)
	mustSucceed(t, stdout, stderr, err)
	resumed := loadQL(t, repoDir)
	if resumed.Status != "completed" || len(resumed.Resumes) != 1 || resumed.Resumes[0].Entry != "implement" ||
		!strings.HasPrefix(resumed.Resumes[0].FromReason, "no progress:") {
		t.Errorf("status = %s, resumes = %+v", resumed.Status, resumed.Resumes)
	}
	if want := []string{"implement", "test", "implement", "implement", "test"}; !equalStrings(resumed.actions(), want) {
		t.Errorf("executions after the resume = %v, want %v", resumed.actions(), want)
	}
}

func TestScenario41_TurnedOffTheLoopRunsToItsLimit(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	// The helpers' default is limits.stop_without_progress: false.
	r := newQualityRun(t, repoDir)
	neverFix(t)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementAndTest, "fix Add in fixture.go")...)
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || !strings.Contains(err.Error(), "maximum fix iterations exceeded") {
		t.Fatalf("expected the fix-iteration limit, got %T: %v", err, err)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "implement", "implement"}) {
		t.Errorf("invocations = %v", got)
	}
}
