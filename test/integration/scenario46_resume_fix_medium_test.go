package integration

import (
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 46, resume and review.fix_medium (specs/quality/SPEC.md §7.3, §10): a run that is
// interrupted right after a review that passed with a medium issue resumes into implement,
// because the option is recorded in the run at its start, and the config the resume is given
// no longer has it.

func TestScenario46_ResumeFollowsTheRecordedFixMediumOption(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	// max_steps 3 ends the run after implement, test and review: the review has passed with a
	// medium issue and the run has not yet taken the step back to implement.
	r := newQualityRun(t, repoDir, func(o *configOptions) {
		o.fixMedium = true
		o.maxSteps = 3
	})
	writeResponse(t, r.responses, "review_1", reviewMediumResponse)
	writeResponse(t, r.responses, "review_2", reviewPassResponse)

	_, _, err := runOrchestrator(t, repoDir, r.cfg, append(implementTestReview, "fix Add in fixture.go")...)
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) || !strings.Contains(err.Error(), "maximum steps exceeded") {
		t.Fatalf("expected the step limit, got %T: %v", err, err)
	}
	stopped := loadQL(t, repoDir)
	if stopped.Status != "stopped" || !equalStrings(stopped.actions(), []string{"implement", "test", "review"}) {
		t.Fatalf("status = %s, executions = %v", stopped.Status, stopped.actions())
	}
	if !stopped.Options.ReviewFixMedium || stopped.FixIterations != 1 {
		t.Fatalf("options = %+v, fix_iterations = %d: the pass with a medium issue is the first fix iteration",
			stopped.Options, stopped.FixIterations)
	}

	// The operator raises the step limit and, in the same edit, drops fix_medium from the config.
	changed := jevConfig(t, r.jev, func(o *configOptions) { o.maxSteps = 12 })
	assertNotContains(t, "the new config", readFile(t, changed), "fix_medium")

	stdout, stderr, err := resume(t, repoDir, changed, stopped.ID)
	mustSucceed(t, stdout, stderr, err)

	// Without the recorded option the passing review would have ended the run; with it, the
	// resume goes back into implement, with the medium issue in the prompt.
	if got := invocations(t, r.invocationsLog); !equalStrings(got, []string{"implement", "review", "implement", "review"}) {
		t.Fatalf("invocations = %v", got)
	}
	assertContains(t, "the resumed implement prompt", r.prompt(t, "implement", 2),
		"A code review passed but noted issues worth fixing.",
		"- [medium] fixture.go: no test covers the negative case")

	run := loadQL(t, repoDir)
	if run.Status != "completed" || run.FixIterations != 1 {
		t.Errorf("status = %s, fix_iterations = %d", run.Status, run.FixIterations)
	}
	if !run.Options.ReviewFixMedium {
		t.Error("the resume changed the recorded option")
	}
	if len(run.Resumes) != 1 || run.Resumes[0].Entry != "implement" || run.Resumes[0].FromStatus != "stopped" {
		t.Errorf("resumes = %+v", run.Resumes)
	}
	if want := []string{"implement", "test", "review", "implement", "test", "review"}; !equalStrings(run.actions(), want) {
		t.Errorf("executions = %v, want %v", run.actions(), want)
	}
}
