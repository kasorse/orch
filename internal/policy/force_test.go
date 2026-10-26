package policy

import (
	"reflect"
	"testing"

	"orchestrator/internal/domain"
)

// belowDevelopment is what SelectStages gives a task whose development is under its threshold:
// research on, development off, testing and review dropped for lack of a code change.
func belowDevelopment(reviewProb float64, reviewForced bool) domain.Decision {
	return domain.Decision{
		Source:     domain.DecisionSourceJev,
		Complexity: domain.ComplexityOutcome{Selected: domain.ComplexityMedium},
		Stages: domain.StageSelection{Stages: []domain.StageOutcome{
			{Stage: domain.StageResearch, Probability: fp(0.8), Threshold: fp(0.5), Enabled: true, Reason: "p=0.80 >= 0.50"},
			{Stage: domain.StageDevelopment, Probability: fp(0.3), Threshold: fp(0.6), Reason: "p=0.30 < 0.60"},
			{Stage: domain.StageTesting, Probability: fp(0.2), Threshold: fp(0.5), Reason: "p=0.20 < 0.50"},
			{Stage: domain.StageReview, Probability: fp(reviewProb), Threshold: fp(0.5), Forced: reviewForced, Reason: droppedNoCode},
		}},
	}
}

func TestForceDevelopment_TurnsDevelopmentAndTestingOn(t *testing.T) {
	got := ForceDevelopment(belowDevelopment(0.3, false), "story task")

	dev := stageOf(t, got, domain.StageDevelopment)
	if !dev.Enabled || !dev.Forced || dev.Reason != "story task" {
		t.Errorf("development = %+v, want enabled, forced, reason %q", dev, "story task")
	}
	if dev.Probability == nil || *dev.Probability != 0.3 || *dev.Threshold != 0.6 {
		t.Errorf("development lost its probability and threshold: %+v", dev)
	}
	if test := stageOf(t, got, domain.StageTesting); !test.Enabled || test.Reason != "story task" {
		t.Errorf("testing = %+v, want enabled with reason %q", test, "story task")
	}
	if r := stageOf(t, got, domain.StageReview); r.Enabled || r.Reason != droppedNoCode {
		t.Errorf("review = %+v, want still dropped: its probability is under its threshold", r)
	}
	if a := stageOf(t, got, domain.StageResearch); a.Reason != "p=0.80 >= 0.50" {
		t.Errorf("research = %+v, want untouched", a)
	}
}

func TestForceDevelopment_ClearsExplored(t *testing.T) {
	dec := belowDevelopment(0.3, false)
	dec.Stages.Stages[1].Explored = true
	if dev := stageOf(t, ForceDevelopment(dec, "story task"), domain.StageDevelopment); dev.Explored {
		t.Errorf("development = %+v, forced is not explored", dev)
	}
}

func TestForceDevelopment_RestoresAReviewTheCodeChangeDropped(t *testing.T) {
	got := ForceDevelopment(belowDevelopment(0.7, false), "story task")
	if r := stageOf(t, got, domain.StageReview); !r.Enabled || r.Reason != "p=0.70 >= 0.50" {
		t.Errorf("review = %+v, want enabled with its own reason", r)
	}

	got = ForceDevelopment(belowDevelopment(0.1, true), "story task")
	if r := stageOf(t, got, domain.StageReview); !r.Enabled || r.Reason != "forced: complexity=medium" {
		t.Errorf("forced review = %+v, want enabled as the template forces it", r)
	}
}

func TestForceDevelopment_DevelopmentAlreadyOnIsReturnedAsItWas(t *testing.T) {
	dec := exploreDecision(domain.DecisionSourceJev, 0.1)
	got := ForceDevelopment(dec, "story task")
	if !reflect.DeepEqual(got, dec) {
		t.Errorf("got %+v, want %+v", got, dec)
	}
	if dev := stageOf(t, got, domain.StageDevelopment); dev.Forced || dev.Reason != "p=0.90 >= 0.60" {
		t.Errorf("development = %+v, want the policy's own outcome", dev)
	}
}

func TestForceDevelopment_ManualDecisionWithoutDevelopment(t *testing.T) {
	dec, err := domain.ManualDecision([]domain.Stage{domain.StageResearch})
	if err != nil {
		t.Fatal(err)
	}
	got := ForceDevelopment(dec, "story task")
	if dev := stageOf(t, got, domain.StageDevelopment); !dev.Enabled || !dev.Forced || dev.Reason != "story task" {
		t.Errorf("development = %+v, want forced on", dev)
	}
	if test := stageOf(t, got, domain.StageTesting); !test.Enabled {
		t.Errorf("testing = %+v, want on with development", test)
	}
	if got.Source != domain.DecisionSourceManual {
		t.Errorf("source = %q, want it kept", got.Source)
	}
}

func TestForceDevelopment_AfterAnEditThatSwitchedDevelopmentOff(t *testing.T) {
	dec := belowDevelopment(0.7, false)
	on := ForceDevelopment(dec, "story task")
	edited := on.WithEdit(domain.ComplexityMedium, []domain.Stage{domain.StageResearch}, nil)
	if stageOf(t, edited, domain.StageDevelopment).Enabled {
		t.Fatal("the edit should have switched development off")
	}
	got := ForceDevelopment(edited, "story task")
	if dev := stageOf(t, got, domain.StageDevelopment); !dev.Enabled || !dev.Forced || dev.Reason != "story task" {
		t.Errorf("development = %+v, want forced on again", dev)
	}
	if got.Edit == nil {
		t.Error("the edit record was lost")
	}
	if r := stageOf(t, got, domain.StageReview); r.Enabled {
		t.Errorf("review = %+v, the operator deselected it", r)
	}
}

func TestForceDevelopment_DoesNotMutateTheInput(t *testing.T) {
	dec := belowDevelopment(0.7, false)
	before := belowDevelopment(0.7, false)
	ForceDevelopment(dec, "story task")
	if !reflect.DeepEqual(dec, before) {
		t.Errorf("input changed: %+v", dec)
	}
}

func TestSkipPlanning_TurnsTheReadOnlyStagesOff(t *testing.T) {
	dec := belowDevelopment(0.7, false)
	dec.Stages.Stages = append(dec.Stages.Stages, domain.StageOutcome{Stage: domain.StageInvestigation, Enabled: true, Forced: true, Reason: "forced: complexity=complex"})
	before := belowDevelopment(0.7, false)
	got := SkipPlanning(ForceDevelopment(dec, "story task"), "story task: planned")
	for _, st := range []domain.Stage{domain.StageResearch, domain.StageInvestigation} {
		if s := stageOf(t, got, st); s.Enabled || s.Forced || s.Reason != "story task: planned" {
			t.Errorf("%s = %+v, want off with the reason", st, s)
		}
	}
	if dev := stageOf(t, got, domain.StageDevelopment); !dev.Enabled {
		t.Errorf("development = %+v, want it kept on", dev)
	}
	if !reflect.DeepEqual(dec.Stages.Stages[:4], before.Stages.Stages) {
		t.Errorf("input changed: %+v", dec)
	}
}
