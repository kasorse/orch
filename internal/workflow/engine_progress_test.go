package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

func implOutcome(tree string) StepOutcome {
	return StepOutcome{Outcome: OutcomeSuccess, TreeAfter: tree}
}

func failingTests(exit int, names ...string) StepOutcome {
	return StepOutcome{Outcome: OutcomeFailure, TestResult: &domain.TestResult{ExitCode: exit, FailedTests: names, Stdout: "FAIL"}}
}

func passingTests() StepOutcome {
	return StepOutcome{Outcome: OutcomeSuccess, TestResult: &domain.TestResult{Success: true}}
}

func failingReview(issues ...domain.ReviewIssue) StepOutcome {
	return StepOutcome{Outcome: OutcomeFailure, ReviewResult: &domain.ReviewResult{Status: domain.ReviewFail, Issues: issues}}
}

// persisted records the status of the run at every persist.
type persisted struct{ status []domain.RunStatus }

func (p *persisted) persist(run *domain.Run) error {
	p.status = append(p.status, run.Status)
	return nil
}

func progressLimits(maxFix int, stop bool) domain.LimitsView {
	return domain.LimitsView{MaxSteps: 40, MaxFixIterations: maxFix, StopWithoutProgress: stop}
}

func assertLimitStop(t *testing.T, err error, run *domain.Run, reason string) {
	t.Helper()
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *apperr.LimitExceededError, got %T: %v", err, err)
	}
	if run.Status != domain.RunStopped || run.FailureReason != reason {
		t.Errorf("status %s, reason %q; want stopped, %q", run.Status, run.FailureReason, reason)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("the error lacks the reason %q: %v", reason, err)
	}
}

// Equal trees stop the run right after the second implement, before a test is spent on it.
func TestEngine_NoProgress_EqualTreesStopAfterSecondImplement(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	for i := 0; i < 4; i++ {
		exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)
		exec.script(domain.ActionTest, failingTests(1, "pkg.Test"+string(rune('A'+i))), nil)
	}

	run := newRun(wf)
	p := &persisted{}
	engine := &Engine{Executor: exec, Persist: p.persist, Limits: progressLimits(5, true)}

	err := engine.Run(context.Background(), run)
	assertLimitStop(t, err, run, ReasonNoChange)
	if !strings.Contains(err.Error(), "limits.stop_without_progress: false") {
		t.Errorf("the error does not say how to turn the check off: %v", err)
	}
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionImplement})
	if got := p.status[len(p.status)-1]; got != domain.RunStopped {
		t.Errorf("last persisted status = %s, want stopped", got)
	}
	if run.Executions[0].TreeAfter != treeID('a') || run.Executions[2].TreeAfter != treeID('a') || run.Executions[1].TreeAfter != "" {
		t.Errorf("tree_after = %q, %q, %q", run.Executions[0].TreeAfter, run.Executions[1].TreeAfter, run.Executions[2].TreeAfter)
	}
	if run.FixIterations != 1 || run.TotalSteps != 3 {
		t.Errorf("fix %d, total %d; the stop must not change the counts", run.FixIterations, run.TotalSteps)
	}
}

// With the check off the same run spends every iteration, as before.
func TestEngine_NoProgress_OffUsesAllIterations(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	for i := 0; i < 4; i++ {
		exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)
		exec.script(domain.ActionTest, failingTests(1, "pkg.TestA"), nil)
	}

	run := newRun(wf)
	err := (&Engine{Executor: exec, Limits: progressLimits(2, false)}).Run(context.Background(), run)
	assertLimitStop(t, err, run, "maximum fix iterations exceeded")
	if len(exec.calls) != 6 {
		t.Errorf("calls = %v, want 3 rounds of implement and test", exec.calls)
	}
}

func TestEngine_NoProgress_SameTestsFail(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	for _, tree := range []byte{'a', 'b', 'c'} {
		exec.script(domain.ActionImplement, implOutcome(treeID(tree)), nil)
		exec.script(domain.ActionTest, failingTests(1, "pkg.TestB", "pkg.TestA"), nil)
	}

	run := newRun(wf)
	err := (&Engine{Executor: exec, Limits: progressLimits(5, true)}).Run(context.Background(), run)
	assertLimitStop(t, err, run, ReasonSameTests)
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionImplement, domain.ActionTest})
	if run.FixIterations != 2 {
		t.Errorf("fix iterations = %d, want 2", run.FixIterations)
	}
}

// max_fix_iterations is read first, so a run that exhausted it keeps its reason.
func TestEngine_NoProgress_MaxFixIterationsWinsWhenBothApply(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	for _, tree := range []byte{'a', 'b', 'c'} {
		exec.script(domain.ActionImplement, implOutcome(treeID(tree)), nil)
		exec.script(domain.ActionTest, failingTests(1, "pkg.TestA"), nil)
	}

	run := newRun(wf)
	err := (&Engine{Executor: exec, Limits: progressLimits(1, true)}).Run(context.Background(), run)
	assertLimitStop(t, err, run, "maximum fix iterations exceeded")
}

func TestEngine_NoProgress_ReviewRepeatsTheSameIssues(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionReview}}}
	issue := domain.ReviewIssue{Severity: domain.SeverityHigh, File: "a.go", Description: "The pointer is dereferenced without a nil check"}
	exec := newScriptedExecutor()
	for _, tree := range []byte{'a', 'b', 'c'} {
		exec.script(domain.ActionImplement, implOutcome(treeID(tree)), nil)
		exec.script(domain.ActionReview, failingReview(issue), nil)
	}

	run := newRun(wf)
	err := (&Engine{Executor: exec, Limits: progressLimits(5, true)}).Run(context.Background(), run)
	assertLimitStop(t, err, run, ReasonSameReviewHits)
	assertActions(t, exec.calls, []domain.ActionType{domain.ActionImplement, domain.ActionReview, domain.ActionImplement, domain.ActionReview})
}

// A loop that does move - another tree, another failure - runs on to its fix.
func TestEngine_NoProgress_ProgressIsNotStopped(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)
	exec.script(domain.ActionTest, failingTests(1, "pkg.TestA"), nil)
	exec.script(domain.ActionImplement, implOutcome(treeID('b')), nil)
	exec.script(domain.ActionTest, failingTests(1, "pkg.TestB"), nil)
	exec.script(domain.ActionImplement, implOutcome(treeID('c')), nil)
	exec.script(domain.ActionTest, passingTests(), nil)

	run := newRun(wf)
	if err := (&Engine{Executor: exec, Limits: progressLimits(5, true)}).Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunCompleted || len(exec.calls) != 6 {
		t.Errorf("status %s after %v", run.Status, exec.calls)
	}
}

// Git could not give a tree: two missing values are not equal ones.
func TestEngine_NoProgress_MissingTreesAreNotEqual(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, implOutcome(""), nil)
	exec.script(domain.ActionTest, failingTests(1, "pkg.TestA"), nil)
	exec.script(domain.ActionImplement, implOutcome(""), nil)
	exec.script(domain.ActionTest, passingTests(), nil)

	run := newRun(wf)
	if err := (&Engine{Executor: exec, Limits: progressLimits(5, true)}).Run(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunCompleted {
		t.Errorf("status %s", run.Status)
	}
}

// Resume of a run stopped for no progress enters implement, and the engine neither re-checks
// the entry nor treats the implement it runs as exempt: that step's own result is judged.
func TestEngine_NoProgress_ResumeEntersImplement(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)
	exec.script(domain.ActionTest, failingTests(1, "pkg.TestA"), nil)
	exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)

	run := newRun(wf)
	limits := progressLimits(5, true)
	err := (&Engine{Executor: exec, Limits: limits}).Run(context.Background(), run)
	assertLimitStop(t, err, run, ReasonNoChange)

	// What the app does once the operator approves a resume.
	approve := func() {
		run.Resumes = append(run.Resumes, domain.Resume{FromStatus: run.Status, FromReason: run.FailureReason})
		run.Status = domain.RunRunning
		run.FailureReason = ""
	}

	// The resumed implement changes the tree: the run goes on to the test step.
	approve()
	resumed := newScriptedExecutor()
	resumed.script(domain.ActionImplement, implOutcome(treeID('b')), nil)
	resumed.script(domain.ActionTest, passingTests(), nil)
	if err := (&Engine{Executor: resumed, Limits: limits}).Run(context.Background(), run); err != nil {
		t.Fatalf("resumed: %v", err)
	}
	assertActions(t, resumed.calls, []domain.ActionType{domain.ActionImplement, domain.ActionTest})
	if run.Status != domain.RunCompleted {
		t.Errorf("status %s", run.Status)
	}
}

func TestEngine_NoProgress_ResumedImplementThatChangesNothingStopsAgain(t *testing.T) {
	wf := domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}}}
	exec := newScriptedExecutor()
	exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)
	exec.script(domain.ActionTest, failingTests(1, "pkg.TestA"), nil)
	exec.script(domain.ActionImplement, implOutcome(treeID('a')), nil)

	run := newRun(wf)
	limits := progressLimits(5, true)
	assertLimitStop(t, (&Engine{Executor: exec, Limits: limits}).Run(context.Background(), run), run, ReasonNoChange)

	run.Resumes = append(run.Resumes, domain.Resume{FromStatus: run.Status, FromReason: run.FailureReason})
	run.Status, run.FailureReason = domain.RunRunning, ""
	again := newScriptedExecutor()
	again.script(domain.ActionImplement, implOutcome(treeID('a')), nil)
	err := (&Engine{Executor: again, Limits: limits}).Run(context.Background(), run)
	assertLimitStop(t, err, run, ReasonNoChange)
	assertActions(t, again.calls, []domain.ActionType{domain.ActionImplement})
	if got := run.Executions[len(run.Executions)-1].Attempt; got != 1 {
		t.Errorf("attempt of the resumed implement = %d, want 1", got)
	}
}
