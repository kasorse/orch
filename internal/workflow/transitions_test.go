package workflow

import (
	"testing"

	"github.com/kasorse/orch/internal/domain"
)

func wfOf(actions ...domain.ActionType) domain.Workflow {
	steps := make([]domain.Step, len(actions))
	for i, a := range actions {
		steps[i] = domain.Step{Action: a}
	}
	return domain.Workflow{Steps: steps}
}

func TestNext_ForwardFollowsWorkflowOrder(t *testing.T) {
	// A workflow shape that could never come out of the fixed stage order
	// (StageOrder is research/investigation/development/testing/review), used
	// deliberately to prove forward progress reads wf.Steps rather than assuming any
	// particular hardcoded jump.
	wf := wfOf(domain.ActionInvestigate, domain.ActionResearch, domain.ActionReview, domain.ActionImplement)

	tests := []struct {
		current State
		want    State
	}{
		{StateInvestigate, StateResearch},
		{StateResearch, StateReview},
		{StateReview, StateImplement},
		{StateImplement, StateDone},
	}
	for _, tt := range tests {
		got, reason := Next(tt.current, OutcomeSuccess, wf)
		if got != tt.want {
			t.Errorf("Next(%s, success) = %s, want %s", tt.current, got, tt.want)
		}
		if got != StateFailed && reason != "" {
			t.Errorf("Next(%s, success) reason = %q, want empty", tt.current, reason)
		}
	}
}

func TestNext_LastStepSuccessGoesToDone(t *testing.T) {
	wf := wfOf(domain.ActionResearch)
	got, _ := Next(StateResearch, OutcomeSuccess, wf)
	if got != StateDone {
		t.Errorf("Next(research, success) on a single-step workflow = %s, want done", got)
	}
}

func TestNext_NonRetryableFailureAlwaysFails(t *testing.T) {
	// Every state except Test and Review fails the run outright on outcome=failure,
	// regardless of what else is in the workflow.
	for _, current := range []State{StateResearch, StateInvestigate, StateImplement} {
		wf := wfOf(domain.ActionType(current), domain.ActionTest, domain.ActionReview)
		got, reason := Next(current, OutcomeFailure, wf)
		if got != StateFailed {
			t.Errorf("Next(%s, failure) = %s, want failed", current, got)
		}
		if reason == "" {
			t.Errorf("Next(%s, failure) gave no reason", current)
		}
	}
}

func TestNext_TestFailure_WithImplement_RetriesImplement(t *testing.T) {
	wf := wfOf(domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	got, reason := Next(StateTest, OutcomeFailure, wf)
	if got != StateImplement {
		t.Errorf("Next(test, failure) = %s, want implement", got)
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

// TestNext_TestFailure_WithoutImplement_FailsRun is TASKS.md В5.
func TestNext_TestFailure_WithoutImplement_FailsRun(t *testing.T) {
	wf := wfOf(domain.ActionTest)
	got, reason := Next(StateTest, OutcomeFailure, wf)
	if got != StateFailed {
		t.Errorf("Next(test, failure) with no implement = %s, want failed", got)
	}
	if reason != "tests failed" {
		t.Errorf(`reason = %q, want "tests failed"`, reason)
	}
}

func TestNext_ReviewFailure_WithImplement_RetriesImplement(t *testing.T) {
	wf := wfOf(domain.ActionImplement, domain.ActionReview)
	got, reason := Next(StateReview, OutcomeFailure, wf)
	if got != StateImplement {
		t.Errorf("Next(review, failure) = %s, want implement", got)
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

// TestNext_ReviewFailure_WithoutImplement_StillCompletes is TASKS.md В5's asymmetric
// half: unlike a failing test, a failing review with no implement completes the run -
// see the doc comment on Next for why.
func TestNext_ReviewFailure_WithoutImplement_StillCompletes(t *testing.T) {
	wf := wfOf(domain.ActionReview)
	got, reason := Next(StateReview, OutcomeFailure, wf)
	if got != StateDone {
		t.Errorf("Next(review, failure) with no implement = %s, want done", got)
	}
	if reason == "" {
		t.Error("expected a non-empty reason even though the run completes")
	}
}

func TestNext_TestSuccess_FollowsWorkflowOrder(t *testing.T) {
	// Test succeeding must move forward by position, not by a hardcoded
	// RequiresReview-style check - here the next step is investigate, not review, to
	// prove that.
	wf := wfOf(domain.ActionTest, domain.ActionInvestigate)
	got, _ := Next(StateTest, OutcomeSuccess, wf)
	if got != StateInvestigate {
		t.Errorf("Next(test, success) = %s, want investigate (the next step in wf.Steps)", got)
	}
}

func TestNext_CurrentNotInWorkflow_FailsClosed(t *testing.T) {
	wf := wfOf(domain.ActionImplement)
	got, reason := Next(StateReview, OutcomeSuccess, wf)
	if got != StateFailed {
		t.Errorf("Next(review, success) with review absent from wf = %s, want failed", got)
	}
	if reason == "" {
		t.Error("expected a non-empty reason")
	}
}

func TestNext_Deterministic(t *testing.T) {
	wf := wfOf(domain.ActionImplement, domain.ActionTest, domain.ActionReview)
	s1, r1 := Next(StateTest, OutcomeFailure, wf)
	s2, r2 := Next(StateTest, OutcomeFailure, wf)
	if s1 != s2 || r1 != r2 {
		t.Errorf("Next is not deterministic: (%s, %q) vs (%s, %q)", s1, r1, s2, r2)
	}
}

func TestIsBackwardEntryToImplement(t *testing.T) {
	tests := []struct {
		from, to State
		want     bool
	}{
		{StateTest, StateImplement, true},
		{StateReview, StateImplement, true},
		{StateResearch, StateImplement, false},
		{StateInvestigate, StateImplement, false},
		{StateTest, StateReview, false},
		{StateImplement, StateDone, false},
	}
	for _, tt := range tests {
		if got := isBackwardEntryToImplement(tt.from, tt.to); got != tt.want {
			t.Errorf("isBackwardEntryToImplement(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}
