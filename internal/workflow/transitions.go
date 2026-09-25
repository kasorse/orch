package workflow

import (
	"fmt"

	"orchestrator/internal/domain"
)

type State string

const (
	StateResearch  State = State(domain.ActionResearch)
	StateAnalyze   State = State(domain.ActionAnalyze)
	StatePlan      State = State(domain.ActionPlan)
	StateImplement State = State(domain.ActionImplement)
	StateTest      State = State(domain.ActionTest)
	StateReview    State = State(domain.ActionReview)
	// StateAnswer exists only for the pre-Jev classifier flow (workflow.BuildLegacy) -
	// specs/JEV integration/TASKS.md T12 removes it along with that flow.
	StateAnswer State = State(domain.ActionAnswer)
	StateDone   State = "done"
	StateFailed State = "failed"
)

type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Next implements the workflow state machine's transition rule (specs/JEV
// integration/SPEC.md §13, TASKS.md T05). Forward progress follows wf.Steps' own
// fixed order - current's successor in wf.Steps, or StateDone once current is the
// last step - rather than a hardcoded per-action jump table (there is no more
// "analyze/plan always goes to implement": whatever Build put after current in the
// selected stages is what runs next). wf is consulted only to look up that order and
// whether Implement is part of this run - it is never walked as a queue.
//
// Only Test and Review retry backward into Implement, and only when Implement is
// actually part of this workflow (TASKS.md В5): a workflow with no development has
// nothing to fix.
//   - A failing Test with no Implement ends the run (reason "tests failed") - the
//     task was "run the tests," and that is what it did.
//   - A failing Review with no Implement still completes the run (reason "review
//     found issues") - the task was "review this diff and report," and reporting a
//     failing verdict fulfils it. The verdict itself is already carried on the
//     step's StepExecution.ReviewResult and shown by the normal reporting path -
//     Next does not need to do anything further with the reason here.
//
// reason is a short, human-readable note on the transition; it is only meaningful
// (and only used by Engine) when next is StateFailed.
func Next(current State, outcome Outcome, wf domain.Workflow) (next State, reason string) {
	if outcome == OutcomeFailure {
		hasImplement := wf.Contains(domain.ActionImplement)
		switch current {
		case StateTest:
			if hasImplement {
				return StateImplement, "tests failed, retrying implement"
			}
			return StateFailed, "tests failed"
		case StateReview:
			if hasImplement {
				return StateImplement, "review found issues, retrying implement"
			}
			return StateDone, "review found issues"
		default:
			return StateFailed, fmt.Sprintf("%s failed", current)
		}
	}

	if next, ok := forward(current, wf); ok {
		return next, ""
	}
	// current is not one of wf's steps at all - a caller bug (e.g. Next invoked with
	// a stale state after the workflow was rebuilt out from under it), not a
	// retryable outcome, so this fails the run rather than silently completing it.
	return StateFailed, fmt.Sprintf("%s is not part of this workflow", current)
}

// forward returns the state that follows current in wf.Steps, or StateDone if
// current is wf's last step. ok is false if current is not one of wf's steps.
func forward(current State, wf domain.Workflow) (next State, ok bool) {
	for i, step := range wf.Steps {
		if State(step.Action) != current {
			continue
		}
		if i+1 < len(wf.Steps) {
			return State(wf.Steps[i+1].Action), true
		}
		return StateDone, true
	}
	return "", false
}

// isBackwardEntryToImplement flags a transition as a fix-iteration retry: entering
// Implement from Test or Review (as opposed to the forward Analyze/Plan -> Implement
// transition, which is not a retry).
func isBackwardEntryToImplement(from, to State) bool {
	if to != StateImplement {
		return false
	}
	return from == StateTest || from == StateReview
}
