package workflow

import (
	"reflect"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/policy"
)

// outcome is a small StageOutcome builder for tests that only care about which stages
// are enabled, not the full probability/threshold trace.
func outcome(stage domain.Stage, enabled bool) domain.StageOutcome {
	return domain.StageOutcome{Stage: stage, Enabled: enabled}
}

// TestBuild_OrdersStagesRegardlessOfInputOrder is TASKS.md T04's acceptance example:
// a selection whose Stages entries are listed out of order (review, development,
// analysis, testing) must still produce a workflow ordered analysis, development,
// testing, review - Build walks domain.StageOrder() itself rather than trusting the
// selection's own order (SPEC.md §13: stage order is never something a caller or a
// decision provider controls).
func TestBuild_OrdersStagesRegardlessOfInputOrder(t *testing.T) {
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		outcome(domain.StageReview, true),
		outcome(domain.StageDevelopment, true),
		outcome(domain.StageAnalysis, true),
		outcome(domain.StageTesting, true),
	}}

	w, err := Build(sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionAnalyze, domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func TestBuild_OnlyEnabledStagesAreIncluded(t *testing.T) {
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		outcome(domain.StageResearch, false),
		outcome(domain.StageAnalysis, true),
		outcome(domain.StagePlanning, false),
		outcome(domain.StageDevelopment, true),
		outcome(domain.StageTesting, false),
		outcome(domain.StageReview, true),
	}}

	w, err := Build(sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionAnalyze, domain.ActionImplement, domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

// TestBuild_SetsStage checks that each Step also records which stage produced it, not
// just the action it maps to - T11's decision trace and T19's interactive editing both
// need to point a Step back at its Stage.
func TestBuild_SetsStage(t *testing.T) {
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		outcome(domain.StageResearch, true),
		outcome(domain.StageAnalysis, true),
	}}

	w, err := Build(sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(w.Steps))
	}
	if w.Steps[0].Stage != domain.StageResearch || w.Steps[0].Action != domain.ActionResearch {
		t.Errorf("step 0 = %+v, want stage research / action research", w.Steps[0])
	}
	if w.Steps[1].Stage != domain.StageAnalysis || w.Steps[1].Action != domain.ActionAnalyze {
		t.Errorf("step 1 = %+v, want stage analysis / action analyze", w.Steps[1])
	}
}

func TestBuild_Deterministic(t *testing.T) {
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		outcome(domain.StageAnalysis, true),
		outcome(domain.StagePlanning, true),
		outcome(domain.StageDevelopment, true),
		outcome(domain.StageTesting, true),
		outcome(domain.StageReview, true),
	}}

	w1, err1 := Build(sel)
	w2, err2 := Build(sel)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if !reflect.DeepEqual(w1, w2) {
		t.Errorf("expected deterministic output, got %v and %v", w1, w2)
	}
}

func TestBuild_NoEnabledStages(t *testing.T) {
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		outcome(domain.StageResearch, false),
		outcome(domain.StageAnalysis, false),
	}}

	_, err := Build(sel)
	if err == nil {
		t.Fatal("expected an error when no stage is enabled")
	}
}

func TestBuild_EmptySelection(t *testing.T) {
	_, err := Build(domain.StageSelection{})
	if err == nil {
		t.Fatal("expected an error for an empty selection")
	}
}

// TestBuild_UnknownStage is defensive: domain.StageSelection is a public struct a
// caller could in principle hand-build with a stage name outside domain.StageOrder.
// Build must fail loudly rather than silently drop it or panic.
func TestBuild_UnknownStage(t *testing.T) {
	sel := domain.StageSelection{Stages: []domain.StageOutcome{
		{Stage: "deploy", Enabled: true},
	}}

	_, err := Build(sel)
	if err == nil {
		t.Fatal("expected an error for a stage with no action mapping")
	}
}

// TestBuild_FromPolicySelection chains policy.SelectStages straight into Build, using
// MVP.md §38's repository-change example, to confirm the two packages compose the way
// classification.Service (T09) will use them.
func TestBuild_FromPolicySelection(t *testing.T) {
	p := domain.DecisionPolicy{
		StageThreshold: map[domain.Stage]float64{
			domain.StageResearch:    0.70,
			domain.StageAnalysis:    0.60,
			domain.StagePlanning:    0.65,
			domain.StageDevelopment: 0.60,
			domain.StageTesting:     0.60,
			domain.StageReview:      0.70,
		},
	}
	stages := domain.StageDecisions{
		Research:    domain.ProbabilityDecision{Probability: 0.31},
		Analysis:    domain.ProbabilityDecision{Probability: 0.92},
		Planning:    domain.ProbabilityDecision{Probability: 0.83},
		Development: domain.ProbabilityDecision{Probability: 0.99},
		Testing:     domain.ProbabilityDecision{Probability: 0.96},
		Review:      domain.ProbabilityDecision{Probability: 0.82},
	}

	sel := policy.SelectStages(stages, domain.ComplexityMedium, p)
	w, err := Build(sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.ActionType{domain.ActionAnalyze, domain.ActionPlan, domain.ActionImplement, domain.ActionTest, domain.ActionReview}
	if !reflect.DeepEqual(actions(w), want) {
		t.Errorf("got %v, want %v", actions(w), want)
	}
}

func actions(w domain.Workflow) []domain.ActionType {
	out := make([]domain.ActionType, len(w.Steps))
	for i, s := range w.Steps {
		out[i] = s.Action
	}
	return out
}
