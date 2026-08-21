package workflow

import "orchestrator/internal/domain"

type State string

const (
	StateAnalyze   State = State(domain.ActionAnalyze)
	StatePlan      State = State(domain.ActionPlan)
	StateImplement State = State(domain.ActionImplement)
	StateTest      State = State(domain.ActionTest)
	StateReview    State = State(domain.ActionReview)
	StateAnswer    State = State(domain.ActionAnswer)
	StateDone      State = "done"
	StateFailed    State = "failed"
)

type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Next implements the exact transition table in MVP.md §11. wf is consulted only to
// know whether test/review are part of this run (RequiresTest/RequiresReview) - it is
// never walked as a queue.
func Next(current State, outcome Outcome, wf domain.Workflow) State {
	switch current {
	case StateAnswer:
		return StateDone

	case StateAnalyze, StatePlan:
		if outcome == OutcomeFailure {
			return StateFailed
		}
		return StateImplement

	case StateImplement:
		if outcome == OutcomeFailure {
			return StateFailed
		}
		if wf.RequiresTest() {
			return StateTest
		}
		if wf.RequiresReview() {
			return StateReview
		}
		return StateDone

	case StateTest:
		if outcome == OutcomeFailure {
			return StateImplement
		}
		if wf.RequiresReview() {
			return StateReview
		}
		return StateDone

	case StateReview:
		if outcome == OutcomeFailure {
			return StateImplement
		}
		return StateDone

	default:
		return StateFailed
	}
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
