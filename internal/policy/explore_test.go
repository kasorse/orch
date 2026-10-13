package policy

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"testing"

	"orchestrator/internal/domain"
)

func exploreDraw(seed string, stage domain.Stage) uint64 {
	h := fnv.New64a()
	h.Write([]byte(seed + "/" + string(stage)))
	return h.Sum64() % 1000
}

// exploreSeed finds a run id whose draw for stage is below limit (under) or at or above it.
func exploreSeed(t *testing.T, stage domain.Stage, limit uint64, under bool) string {
	t.Helper()
	for i := range 10000 {
		seed := fmt.Sprintf("run-%d", i)
		if (exploreDraw(seed, stage) < limit) == under {
			return seed
		}
	}
	t.Fatalf("no seed found for %s", stage)
	return ""
}

func fp(v float64) *float64 { return &v }

func explorePolicy(band, rate float64) domain.DecisionPolicy {
	return domain.DecisionPolicy{Explore: &domain.ExploreSettings{Band: band, Rate: rate}}
}

func exploreDecision(source domain.DecisionSource, prob float64) domain.Decision {
	return domain.Decision{Source: source, Stages: domain.StageSelection{Stages: []domain.StageOutcome{
		{Stage: domain.StageDevelopment, Probability: fp(0.9), Threshold: fp(0.6), Enabled: true, Reason: "p=0.90 >= 0.60"},
		{Stage: domain.StageTesting, Probability: fp(prob), Threshold: fp(0.5), Reason: "p below threshold"},
		{Stage: domain.StageReview, Probability: fp(prob), Threshold: fp(0.5), Reason: "p below threshold"},
	}}}
}

func stageOf(t *testing.T, d domain.Decision, st domain.Stage) domain.StageOutcome {
	t.Helper()
	for _, so := range d.Stages.Stages {
		if so.Stage == st {
			return so
		}
	}
	t.Fatalf("no outcome for %s", st)
	return domain.StageOutcome{}
}

func TestExplore_BelowTheRateTurnsTheStageOn(t *testing.T) {
	seed := exploreSeed(t, domain.StageTesting, 100, true)
	got := Explore(exploreDecision(domain.DecisionSourceJev, 0.47), explorePolicy(0.05, 0.1), seed)

	so := stageOf(t, got, domain.StageTesting)
	if !so.Enabled || !so.Explored || so.Forced {
		t.Errorf("testing = %+v, want enabled and explored, not forced", so)
	}
	if want := "explored (P 0.47, threshold 0.50)"; so.Reason != want {
		t.Errorf("reason = %q, want %q", so.Reason, want)
	}
	if !got.Stages.Stages[0].Enabled || got.Stages.Stages[0].Explored {
		t.Errorf("an enabled stage must stay as it was: %+v", got.Stages.Stages[0])
	}
}

func TestExplore_AboveTheRateLeavesTheStageOff(t *testing.T) {
	seed := exploreSeed(t, domain.StageTesting, 100, false)
	in := exploreDecision(domain.DecisionSourceJev, 0.47)
	got := Explore(in, explorePolicy(0.05, 0.1), seed)

	if so := stageOf(t, got, domain.StageTesting); so.Enabled || so.Explored {
		t.Errorf("testing = %+v, want it left off", so)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("nothing was explored, but the decision changed:\n%+v\nwas\n%+v", got, in)
	}
}

func TestExplore_NeverOutsideTheBand(t *testing.T) {
	p := explorePolicy(0.05, 0.5)
	for _, prob := range []float64{0.0, 0.3, 0.4499, 0.5, 0.51, 1.0} {
		for i := range 300 {
			got := Explore(exploreDecision(domain.DecisionSourceJev, prob), p, fmt.Sprintf("run-%d", i))
			for _, st := range []domain.Stage{domain.StageTesting, domain.StageReview} {
				if so := stageOf(t, got, st); so.Enabled || so.Explored {
					t.Fatalf("P %v is outside [0.45, 0.50) but %s = %+v (run-%d)", prob, st, so, i)
				}
			}
		}
	}
}

func TestExplore_BandEdgeIsIncluded(t *testing.T) {
	// 0.5 - 0.05 is not exactly 0.45 in floating point; the edge must still be in the band.
	seed := exploreSeed(t, domain.StageTesting, 500, true)
	got := Explore(exploreDecision(domain.DecisionSourceJev, 0.45), explorePolicy(0.05, 0.5), seed)
	if so := stageOf(t, got, domain.StageTesting); !so.Explored {
		t.Errorf("P at threshold - band was not explored: %+v", so)
	}
}

func TestExplore_NeverChangesAnEnabledOrForcedStage(t *testing.T) {
	in := exploreDecision(domain.DecisionSourceJev, 0.47)
	in.Stages.Stages[1].Enabled = true
	in.Stages.Stages[1].Forced = true
	in.Stages.Stages[1].Reason = "forced: complexity=complex"
	in.Stages.Stages[2].Probability = nil // a stage without a probability is skipped
	for i := range 300 {
		got := Explore(in, explorePolicy(0.2, 0.5), fmt.Sprintf("run-%d", i))
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("run-%d changed a decision with nothing to explore:\n%+v", i, got)
		}
	}
}

func TestExplore_ManualAndNoSettingsReturnTheInput(t *testing.T) {
	seed := exploreSeed(t, domain.StageTesting, 500, true)

	manual := exploreDecision(domain.DecisionSourceManual, 0.47)
	if got := Explore(manual, explorePolicy(0.05, 0.5), seed); !reflect.DeepEqual(got, manual) {
		t.Errorf("a manual decision was explored: %+v", got)
	}
	jev := exploreDecision(domain.DecisionSourceJev, 0.47)
	if got := Explore(jev, domain.DecisionPolicy{}, seed); !reflect.DeepEqual(got, jev) {
		t.Errorf("a decision was explored without policy.explore: %+v", got)
	}
}

func TestExplore_SameInputsSameOutput(t *testing.T) {
	seed := exploreSeed(t, domain.StageTesting, 500, true)
	p := explorePolicy(0.05, 0.5)
	a := Explore(exploreDecision(domain.DecisionSourceJev, 0.47), p, seed)
	b := Explore(exploreDecision(domain.DecisionSourceJev, 0.47), p, seed)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("the same inputs gave\n%+v\nand\n%+v", a, b)
	}
}

func TestExplore_DoesNotMutateTheInput(t *testing.T) {
	seed := exploreSeed(t, domain.StageTesting, 500, true)
	in := exploreDecision(domain.DecisionSourceJev, 0.47)
	want := exploreDecision(domain.DecisionSourceJev, 0.47)
	got := Explore(in, explorePolicy(0.05, 0.5), seed)

	if !stageOf(t, got, domain.StageTesting).Explored {
		t.Fatal("the test needs an explored stage")
	}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("Explore modified its input:\n%+v\nwas\n%+v", in, want)
	}
}

// Development is the task, and turning it on would need testing with it
// (specs/decision-shape/SPEC.md §6.5): it is never explored, even when the draw picks it.
func TestExplore_NeverDevelopment(t *testing.T) {
	seed := exploreSeed(t, domain.StageDevelopment, 500, true)
	dec := domain.Decision{Source: domain.DecisionSourceLLM, Stages: domain.StageSelection{Stages: []domain.StageOutcome{
		{Stage: domain.StageDevelopment, Probability: fp(0.58), Threshold: fp(0.6), Reason: "p=0.58 < 0.60"},
	}}}
	got := Explore(dec, explorePolicy(0.05, 0.5), seed)
	if so := stageOf(t, got, domain.StageDevelopment); so.Enabled || so.Explored {
		t.Errorf("development = %+v, want it left off", so)
	}
}
