package app

import (
	"errors"
	"math"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/classification"
	"orchestrator/internal/domain"
	"orchestrator/internal/persistence"
)

// undecidedTrace is the trace a decision step had when it ended the run: the pre-check, the clarify
// call and two re-checks.
func undecidedTrace(domain.Workflow) domain.DecisionTrace {
	return domain.DecisionTrace{
		Request: "add an export",
		Latency: domain.DecisionLatency{PreCheckMS: 10},
		Usage: &domain.DecisionUsage{
			ContextDepth:   &domain.Usage{InputTokens: 1, CostUSD: 0.01},
			Clarify:        []domain.Usage{{InputTokens: 5, CostUSD: 0.05}},
			ClarityRecheck: []domain.Usage{{InputTokens: 1, CostUSD: 0.02}, {InputTokens: 1, CostUSD: 0.02}},
		},
	}
}

// A run that stops with "task needs input" has no decision.json, and still counts what its
// decision step cost in run.json's outcome.
func TestStopUndecided_NeedsInputCountsTheDecisionCalls(t *testing.T) {
	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	c := &domain.Clarification{
		Threshold: 0.8, Checks: []float64{0.42, 0.55}, Status: domain.ClarityUnclear,
		Rounds: []domain.ClarifyRound{{
			Kind: domain.ClarifyRoundClarify, Mode: domain.ClarifyAuto,
			Points: []domain.ClarifyPoint{{Unclear: "locale", Question: "Which locale?"}},
		}},
	}

	err = stopUndecided(store, run, decisionOutcome{trace: undecidedTrace}, &classification.NeedsInputError{Clarification: c})
	var limitErr *apperr.LimitExceededError
	if !errors.As(err, &limitErr) {
		t.Fatalf("err = %T %v, want a stop (exit 4)", err, err)
	}

	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.RunStopped || loaded.FailureReason != domain.FailureTaskNeedsInput {
		t.Errorf("run = %s %q, want stopped with task needs input", loaded.Status, loaded.FailureReason)
	}
	o := loaded.Outcome
	if o == nil {
		t.Fatal("run.json has no outcome")
	}
	if d := o.DecisionOverhead; d.Calls != 4 || math.Abs(d.CostUSD-0.10) > 1e-9 || d.MS != 10 {
		t.Errorf("decision_overhead = %+v, want 4 calls costing 0.10 in 10ms", d)
	}
	if o.Usage.Calls != 4 || math.Abs(o.Usage.CostUSD-0.10) > 1e-9 {
		t.Errorf("usage = %+v, want the 4 decision calls", o.Usage)
	}
	if _, _, err := store.LoadDecision(run.ID); err == nil {
		t.Error("a run without a decision must not get a decision.json")
	}
}

// A failed clarify call fails the run, and the calls made before it are counted all the same.
func TestStopUndecided_FailureCountsTheDecisionCalls(t *testing.T) {
	store := persistence.NewStore(t.TempDir())
	run, err := store.NewRun(domain.Task{Description: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	cause := &apperr.ProviderError{Action: "clarify", Kind: apperr.FailureExit}
	if err := stopUndecided(store, run, decisionOutcome{trace: undecidedTrace}, cause); !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the clarify call's failure", err)
	}
	loaded, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != domain.RunFailed || loaded.Outcome == nil || loaded.Outcome.DecisionOverhead.Calls != 4 {
		t.Errorf("run = %s, outcome %+v; want failed with the 4 decision calls counted", loaded.Status, loaded.Outcome)
	}

	// Without a trace (the decision step never started) the outcome has no decision calls.
	run, err = store.NewRun(domain.Task{Description: "add an export"})
	if err != nil {
		t.Fatal(err)
	}
	_ = stopUndecided(store, run, decisionOutcome{}, cause)
	if loaded, err := store.LoadRun(run.ID); err != nil || loaded.Outcome == nil || loaded.Outcome.DecisionOverhead.Calls != 0 {
		t.Errorf("outcome %+v, err %v; want one without decision calls", loaded.Outcome, err)
	}
}
