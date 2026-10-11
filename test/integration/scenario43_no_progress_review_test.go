package integration

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 43, no progress: the review (specs/quality/SPEC.md §8.1, §7.4). The tests pass each
// time, but the review reports the same blocking issue again after implement tried to fix it,
// so the run stops after the second review. A different issue is progress.

func TestScenario43_TheSameIssueTwice(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.stopWithoutProgress = true })
	// Each implement fixes Add and adds a line, so the tree differs and the tests pass.
	appendOnEveryImplement(t, repoDir)
	writeResponse(t, r.responses, "review", reviewFailResponse)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	run := stoppedForNoProgress(t, err, repoDir, "no progress: review repeats the same issues")

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review", "implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	if want := []string{"implement", "test", "review", "implement", "test", "review"}; !equalStrings(run.actions(), want) {
		t.Fatalf("executions = %v, want %v", run.actions(), want)
	}
	if a, b := run.Executions[0].TreeAfter, run.Executions[3].TreeAfter; a == "" || a == b {
		t.Errorf("tree_after = %q then %q, want two different trees", a, b)
	}
	if run.FixIterations != 2 {
		t.Errorf("fix_iterations = %d, want 2", run.FixIterations)
	}
}

func TestScenario43_ADifferentIssueIsProgress(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.stopWithoutProgress = true })
	appendOnEveryImplement(t, repoDir)
	writeResponse(t, r.responses, "review_1", reviewFailResponse)
	writeResponse(t, r.responses, "review_2", reviewJSON("fail",
		`[{"severity": "high", "file": "fixture_test.go", "description": "negative operands are not tested"}]`, ""))
	// The third review repeats the second's issue, but the fix-iteration limit is read first.
	writeResponse(t, r.responses, "review_3", reviewJSON("fail",
		`[{"severity": "high", "file": "fixture_test.go", "description": "negative operands are not tested"}]`, ""))

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || !strings.Contains(err.Error(), "maximum fix iterations exceeded") {
		t.Fatalf("expected the fix-iteration limit, got %T: %v", err, err)
	}
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review", "implement", "review", "implement", "review"}) {
		t.Errorf("invocations = %v: the changed issue after the first review is progress", got)
	}
	if run := loadQL(t, repoDir); run.Status != "stopped" || run.FailureReason != "maximum fix iterations exceeded" {
		t.Errorf("status = %s, failure_reason = %q", run.Status, run.FailureReason)
	}
}
