package integration

import (
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 39, review.fix_medium (specs/quality/SPEC.md §7.3): a review that passes but reports a
// medium issue sends the run back to implement once, spending a fix iteration; the second
// review decides as usual. With the option off the same review completes the run.

func TestScenario39_FixMediumSendsTheRunBackOnce(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir, func(o *configOptions) { o.fixMedium = true })
	writeResponse(t, r.responses, "review_1", reviewMediumResponse)
	// The second review reports a medium issue again: the option is used up, so it is a warning.
	writeResponse(t, r.responses, "review_2", reviewMediumResponse)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review", "implement", "review"}) {
		t.Fatalf("invocations = %v", got)
	}
	assertContains(t, "the console", stdout, "review.fix_medium is on: fixing the medium issues.")

	assertNotContains(t, "the first implement prompt", r.prompt(t, "implement", 1), "noted issues worth fixing")
	assertContains(t, "the second implement prompt", r.prompt(t, "implement", 2),
		"A code review passed but noted issues worth fixing.",
		"- [medium] fixture.go: no test covers the negative case")

	run := loadQL(t, repoDir)
	if want := []string{"implement", "test", "review", "implement", "test", "review"}; !equalStrings(run.actions(), want) {
		t.Fatalf("executions = %v, want %v", run.actions(), want)
	}
	if run.Status != "completed" || run.FixIterations != 1 {
		t.Errorf("status = %s, fix_iterations = %d", run.Status, run.FixIterations)
	}
	if !run.Options.ReviewFixMedium {
		t.Error("run.json does not record options.review_fix_medium")
	}
	if run.Outcome == nil || run.Outcome.FixIterations != 1 {
		t.Errorf("outcome = %+v", run.Outcome)
	}
}

func TestScenario39_OffByDefault(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	r := newQualityRun(t, repoDir)
	writeResponse(t, r.responses, "review", reviewMediumResponse)

	stdout, stderr, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	mustSucceed(t, stdout, stderr, err)

	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review"}) {
		t.Errorf("invocations = %v", got)
	}
	run := loadQL(t, repoDir)
	if run.Status != "completed" || run.FixIterations != 0 || run.Options.ReviewFixMedium {
		t.Errorf("status = %s, fix_iterations = %d, options = %+v", run.Status, run.FixIterations, run.Options)
	}
}
