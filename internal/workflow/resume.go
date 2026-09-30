package workflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// Entry returns the state the engine continues from, given the run's history, and a
// short human-readable reason (specs/resume/SPEC.md §7.1). An empty history returns the
// workflow's first step - the fresh-run case - so a resumed run goes through the same
// engine and the same transition table as a fresh one, not a special code path.
//
//   - last execution failed (a hard error, so no outcome was recorded) -> that action again;
//   - last execution completed -> Next(last, its outcome), with the outcome derived only
//     from trusted persisted results (see outcomeOf);
//   - ... and when Next says the run failed ("tests failed" without implement), the step
//     that failed it is run again: the operator presumably fixed something (Q3).
//
// StateDone means the history already finished the workflow.
func Entry(run *domain.Run) (state State, why string, err error) {
	if run.Workflow == nil || len(run.Workflow.Steps) == 0 {
		return "", "", errors.New("workflow has no steps")
	}
	if len(run.Executions) == 0 {
		return State(run.Workflow.Steps[0].Action), "start", nil
	}

	last := run.Executions[len(run.Executions)-1]
	current := State(last.Action)
	if last.Status == domain.StepFailed {
		return current, fmt.Sprintf("retry %s (failed: %s)", last.Action, firstLine(last.Error)), nil
	}

	outcome, err := outcomeOf(last)
	if err != nil {
		return "", "", fmt.Errorf("execution %d: %w", last.Index, err)
	}
	next, reason := Next(current, outcome, *run.Workflow)
	switch {
	case next == StateDone:
		return StateDone, "the workflow already finished", nil
	case next == StateFailed && current == StateTest:
		return current, fmt.Sprintf("re-run %s (%s)", last.Action, reason), nil
	case next == StateFailed:
		return "", "", fmt.Errorf("cannot continue after %s: %s", last.Action, reason)
	case isBackwardEntryToImplement(current, next):
		return next, reason, nil
	}
	return next, fmt.Sprintf("continue after %s", last.Action), nil
}

// outcomeOf derives a completed execution's outcome from what the orchestrator itself
// established: the test command's exit code, and the review verdict computed by
// ReviewResult.Decide - never the model's self-reported status. Every other action
// succeeds once it completes.
func outcomeOf(exec domain.StepExecution) (Outcome, error) {
	switch exec.Action {
	case domain.ActionTest:
		if exec.TestResult == nil {
			return "", errors.New("completed test has no test result")
		}
		if !exec.TestResult.Success {
			return OutcomeFailure, nil
		}
	case domain.ActionReview:
		if exec.ReviewResult == nil {
			return "", errors.New("completed review has no review result")
		}
		if exec.ReviewResult.Decide() == domain.ReviewFail {
			return OutcomeFailure, nil
		}
	}
	return OutcomeSuccess, nil
}

// Counters derives TotalSteps and FixIterations from the history rather than trusting
// the persisted counters: FixIterations is incremented after a step is persisted, so a
// crash can lose it (specs/resume/SPEC.md §8, G12). A fix iteration is a completed test
// or review that failed in a workflow with implement - exactly the transitions the
// engine counts.
func Counters(run *domain.Run) (totalSteps, fixIterations int, err error) {
	hasImplement := run.Workflow != nil && run.Workflow.Contains(domain.ActionImplement)
	for _, exec := range run.Executions {
		if exec.Status != domain.StepCompleted || !hasImplement {
			continue
		}
		if exec.Action != domain.ActionTest && exec.Action != domain.ActionReview {
			continue
		}
		outcome, err := outcomeOf(exec)
		if err != nil {
			return 0, 0, fmt.Errorf("execution %d: %w", exec.Index, err)
		}
		if outcome == OutcomeFailure {
			fixIterations++
		}
	}
	return len(run.Executions), fixIterations, nil
}

// Verify checks that a persisted run's workflow is the one its decision builds, and that
// every execution belongs to it (specs/resume/SPEC.md §6.3). Build is deterministic, so
// any difference means run.json was edited.
func Verify(run *domain.Run) error {
	if run.Decision == nil || run.Workflow == nil {
		return errors.New("no workflow was decided for this run")
	}
	want, err := Build(run.Decision.Stages)
	if err != nil {
		return fmt.Errorf("the decision builds no workflow: %w", err)
	}
	if !slices.Equal(want.Steps, run.Workflow.Steps) {
		return fmt.Errorf("the workflow %s is not the one the decision builds (%s)", describe(*run.Workflow), describe(want))
	}
	for _, exec := range run.Executions {
		if !run.Workflow.Contains(exec.Action) {
			return fmt.Errorf("execution %d ran %s, which is not part of the workflow", exec.Index, exec.Action)
		}
	}
	return nil
}

// CheckLimits refuses to continue a run whose history already exhausts a limit: limits
// count the whole history, across resumes (specs/resume/SPEC.md §8, Q1). entry is the
// state Entry returned. The counters are derived from the history (Counters), not read
// from the run. The error says which config key to raise.
func CheckLimits(run *domain.Run, limits domain.LimitsView, entry State) error {
	reason, key, used, limit, err := limitReached(run, limits, entry)
	if err != nil {
		return &apperr.WorkflowError{Msg: "cannot count the run's history", Err: err}
	}
	if reason == "" {
		return nil
	}
	return &apperr.LimitExceededError{Reason: fmt.Sprintf(
		"Run status: stopped\nReason: %s\n\nThe run has used %d, and %s is %d. Raise %s to continue it.",
		reason, used, key, limit, key)}
}

// limitReached is CheckLimits' verdict without the rendering: the engine records reason as
// the run's failure reason, exactly as when a limit is hit mid-run.
func limitReached(run *domain.Run, limits domain.LimitsView, entry State) (reason, key string, used, limit int, err error) {
	if entry == StateDone || len(run.Executions) == 0 {
		return "", "", 0, 0, nil
	}
	total, fix, err := Counters(run)
	if err != nil {
		return "", "", 0, 0, err
	}
	if total >= limits.MaxSteps {
		return "maximum steps exceeded", "limits.max_steps", total, limits.MaxSteps, nil
	}
	last := run.Executions[len(run.Executions)-1]
	if entry == StateImplement && fix > limits.MaxFixIterations &&
		last.Status == domain.StepCompleted && isBackwardEntryToImplement(State(last.Action), entry) {
		return "maximum fix iterations exceeded", "limits.max_fix_iterations", fix, limits.MaxFixIterations, nil
	}
	return "", "", 0, 0, nil
}

func describe(wf domain.Workflow) string {
	names := make([]string, len(wf.Steps))
	for i, s := range wf.Steps {
		names[i] = string(s.Action)
	}
	return strings.Join(names, " -> ")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
