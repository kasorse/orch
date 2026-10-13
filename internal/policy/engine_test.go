package policy

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// testPolicy mirrors the example thresholds in specs/JEV integration/TASKS.md T02:
// research 0.70, analysis 0.60, planning 0.65, development 0.60, testing 0.60,
// review 0.70; complex forces analysis, planning, review; size cuts 0.5 and 1.4.
func testPolicy() domain.DecisionPolicy {
	return domain.DecisionPolicy{
		ProjectContextThreshold: 0.5,
		ComplexityCuts:          domain.ComplexityCuts{Medium: 0.5, Complex: 1.4},
		StageThreshold: map[domain.Stage]float64{
			domain.StageResearch:    0.70,
			domain.StageAnalysis:    0.60,
			domain.StagePlanning:    0.65,
			domain.StageDevelopment: 0.60,
			domain.StageTesting:     0.60,
			domain.StageReview:      0.70,
		},
		ForceStages: map[domain.Complexity][]domain.Stage{
			domain.ComplexityComplex: {domain.StageAnalysis, domain.StagePlanning, domain.StageReview},
		},
	}
}

func TestSelectContextDepth(t *testing.T) {
	p := testPolicy()
	tests := []struct {
		name string
		prob float64
		want domain.ContextDepth
	}{
		{"just below threshold", 0.49, domain.ContextDepthNone},
		{"exactly at threshold", 0.50, domain.ContextDepthProject},
		{"well above threshold", 0.91, domain.ContextDepthProject},
		{"well below threshold", 0.05, domain.ContextDepthNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := domain.ContextDepthDecision{
				Probabilities: map[domain.ContextDepth]float64{
					domain.ContextDepthProject: tt.prob,
					domain.ContextDepthNone:    1 - tt.prob,
				},
			}
			if got := SelectContextDepth(d, p); got != tt.want {
				t.Errorf("SelectContextDepth(P(project)=%v) = %s, want %s", tt.prob, got, tt.want)
			}
		})
	}
}

func complexityDist(simple, medium, complex float64) domain.ComplexityDecision {
	return domain.ComplexityDecision{
		Probabilities: map[domain.Complexity]float64{
			domain.ComplexitySimple:  simple,
			domain.ComplexityMedium:  medium,
			domain.ComplexityComplex: complex,
		},
	}
}

func TestSelectComplexity(t *testing.T) {
	p := testPolicy()
	tests := []struct {
		name                    string
		simple, medium, complex float64
		want                    domain.Complexity
	}{
		{"clear simple (size 0.08)", 0.94, 0.04, 0.02, domain.ComplexitySimple},
		{"clear medium (size 1.27)", 0.05, 0.63, 0.32, domain.ComplexityMedium},
		{"clear complex (size 1.70)", 0.10, 0.10, 0.80, domain.ComplexityComplex},
		{"complex cut is inclusive (size 1.40)", 0.20, 0.20, 0.60, domain.ComplexityComplex},
		{"just below the complex cut (size 1.39)", 0.11, 0.39, 0.50, domain.ComplexityMedium},
		{"medium cut is inclusive (size 0.50)", 0.50, 0.50, 0.00, domain.ComplexityMedium},
		{"below the medium cut (size 0.49)", 0.61, 0.29, 0.10, domain.ComplexitySimple},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := complexityDist(tt.simple, tt.medium, tt.complex)
			if got := SelectComplexity(d, p); got != tt.want {
				t.Errorf("SelectComplexity(%v/%v/%v) = %s, want %s", tt.simple, tt.medium, tt.complex, got, tt.want)
			}
		})
	}
}

// TestSelectComplexity_SizeSeparatesWhatArgmaxMerges is the reason for the size
// (specs/decision-shape/SPEC.md §5.1): two distributions with the same "medium" argmax
// land on different levels once the cut sits between their sizes.
func TestSelectComplexity_SizeSeparatesWhatArgmaxMerges(t *testing.T) {
	p := testPolicy()
	p.ComplexityCuts = domain.ComplexityCuts{Medium: 0.5, Complex: 1.2}
	low, high := complexityDist(0.20, 0.60, 0.20), complexityDist(0.05, 0.60, 0.35)
	if got := SelectComplexity(low, p); got != domain.ComplexityMedium {
		t.Errorf("SelectComplexity(.20/.60/.20, size 1.00) = %s, want medium", got)
	}
	if got := SelectComplexity(high, p); got != domain.ComplexityComplex {
		t.Errorf("SelectComplexity(.05/.60/.35, size 1.30) = %s, want complex", got)
	}
}

func stageDecisions(research, analysis, planning, development, testing, review float64) domain.StageDecisions {
	return domain.StageDecisions{
		Research:    domain.ProbabilityDecision{Probability: research},
		Analysis:    domain.ProbabilityDecision{Probability: analysis},
		Planning:    domain.ProbabilityDecision{Probability: planning},
		Development: domain.ProbabilityDecision{Probability: development},
		Testing:     domain.ProbabilityDecision{Probability: testing},
		Review:      domain.ProbabilityDecision{Probability: review},
	}
}

func TestSelectStages_ThresholdBoundary(t *testing.T) {
	p := testPolicy()

	below := stageDecisions(0, 0, 0, 0, 0, 0.69)
	sel := SelectStages(below, domain.ComplexitySimple, p)
	if enabled(sel, domain.StageReview) {
		t.Error("review at p=0.69 (threshold 0.70) should be disabled")
	}

	atThreshold := stageDecisions(0, 0.60 /* analysis: keep something enabled */, 0, 0, 0, 0.70)
	sel = SelectStages(atThreshold, domain.ComplexitySimple, p)
	if !enabled(sel, domain.StageReview) {
		t.Error("review at p=0.70 (threshold 0.70) should be enabled")
	}
}

func TestSelectStages_Forced(t *testing.T) {
	p := testPolicy()
	// Every probability is far below its threshold; only forced stages should end up
	// enabled, since something must clear the В6 fallback check first - development
	// stays disabled to prove force is scoped to analysis/planning/review only.
	d := stageDecisions(0.01, 0.05, 0.05, 0.01, 0.01, 0.05)

	sel := SelectStages(d, domain.ComplexityComplex, p)

	for _, st := range []domain.Stage{domain.StageAnalysis, domain.StagePlanning, domain.StageReview} {
		out := outcomeFor(sel, st)
		if !out.Enabled {
			t.Errorf("%s should be forced-enabled under complexity=complex", st)
		}
		if !out.Forced {
			t.Errorf("%s.Forced should be true", st)
		}
		if out.Reason != "forced: complexity=complex" {
			t.Errorf("%s.Reason = %q, want %q", st, out.Reason, "forced: complexity=complex")
		}
	}
	for _, st := range []domain.Stage{domain.StageResearch, domain.StageDevelopment, domain.StageTesting} {
		if enabled(sel, st) {
			t.Errorf("%s should not be enabled (p far below threshold, not forced)", st)
		}
	}
}

func TestSelectStages_ForcedAndClearedThreshold(t *testing.T) {
	p := testPolicy()
	// review both clears its own threshold and is forced by complexity=complex - the
	// reason should mention both.
	d := stageDecisions(0, 0.9, 0, 0, 0, 0.95)
	sel := SelectStages(d, domain.ComplexityComplex, p)
	out := outcomeFor(sel, domain.StageReview)
	if !out.Enabled || !out.Forced {
		t.Fatalf("review should be enabled and forced, got %+v", out)
	}
	if !strings.Contains(out.Reason, "0.95") || !strings.Contains(out.Reason, "forced") {
		t.Errorf("Reason = %q, want it to mention both the probability and forced", out.Reason)
	}
}

// TestSelectStages_EmptyFallback is TASKS.md В6: nothing clears its threshold and
// nothing is forced (complexity=simple has no force list) - analysis alone is
// enabled rather than an empty workflow.
func TestSelectStages_EmptyFallback(t *testing.T) {
	p := testPolicy()
	d := stageDecisions(0.01, 0.02, 0.01, 0.01, 0.01, 0.01)

	sel := SelectStages(d, domain.ComplexitySimple, p)

	got := sel.EnabledStages()
	if len(got) != 1 || got[0] != domain.StageAnalysis {
		t.Fatalf("EnabledStages() = %v, want [analysis]", got)
	}
	out := outcomeFor(sel, domain.StageAnalysis)
	if out.Forced {
		t.Error("the В6 fallback stage should not be reported as forced by complexity")
	}
	if !strings.Contains(out.Reason, "defaulting to analysis") {
		t.Errorf("Reason = %q, want it to explain the fallback", out.Reason)
	}
}

func TestSelectStages_Deterministic(t *testing.T) {
	p := testPolicy()
	d := stageDecisions(0.72, 0.91, 0.83, 0.99, 0.96, 0.82)

	a := SelectStages(d, domain.ComplexityMedium, p)
	b := SelectStages(d, domain.ComplexityMedium, p)

	if len(a.Stages) != len(b.Stages) {
		t.Fatalf("got different lengths: %d vs %d", len(a.Stages), len(b.Stages))
	}
	for i := range a.Stages {
		if a.Stages[i].Stage != b.Stages[i].Stage || a.Stages[i].Enabled != b.Stages[i].Enabled {
			t.Errorf("stage %d differs between calls: %+v vs %+v", i, a.Stages[i], b.Stages[i])
		}
	}
}

// The three MVP.md §37-39 examples, verified directly against SelectStages with
// testPolicy()'s thresholds.
func TestSelectStages_MVPExamples(t *testing.T) {
	p := testPolicy()

	t.Run("example 1: general question", func(t *testing.T) {
		d := stageDecisions(0.08, 0.94, 0.04, 0.03, 0.01, 0.01)
		got := SelectStages(d, domain.ComplexitySimple, p).EnabledStages()
		wantStages(t, got, domain.StageAnalysis)
	})

	t.Run("example 2: repository change", func(t *testing.T) {
		d := stageDecisions(0.31, 0.92, 0.83, 0.99, 0.96, 0.82)
		got := SelectStages(d, domain.ComplexityMedium, p).EnabledStages()
		wantStages(t, got, domain.StageAnalysis, domain.StagePlanning, domain.StageDevelopment, domain.StageTesting, domain.StageReview)
	})

	t.Run("example 3: investigation only", func(t *testing.T) {
		d := stageDecisions(0.72, 0.98, 0.18, 0.20, 0.11, 0.08)
		got := SelectStages(d, domain.ComplexityMedium, p).EnabledStages()
		wantStages(t, got, domain.StageResearch, domain.StageAnalysis)
	})
}

func TestApplyForced(t *testing.T) {
	p := testPolicy()

	sel := ApplyForced(domain.ComplexityComplex, []domain.Stage{domain.StageDevelopment, domain.StageTesting}, nil, p)

	for _, st := range []domain.Stage{domain.StageAnalysis, domain.StagePlanning, domain.StageReview} {
		out := outcomeFor(sel, st)
		if !out.Enabled || !out.Forced {
			t.Errorf("%s should be forced-enabled, got %+v", st, out)
		}
		if out.Probability != nil || out.Threshold != nil {
			t.Errorf("%s should have no probability/threshold (user-sourced), got %+v", st, out)
		}
	}
	for _, st := range []domain.Stage{domain.StageDevelopment, domain.StageTesting} {
		out := outcomeFor(sel, st)
		if !out.Enabled || out.Forced {
			t.Errorf("%s should be enabled by user pick, not forced, got %+v", st, out)
		}
	}
	if enabled(sel, domain.StageResearch) {
		t.Error("research was neither picked nor forced, should be disabled")
	}
}

func TestApplyForced_PickedAndForced(t *testing.T) {
	p := testPolicy()
	sel := ApplyForced(domain.ComplexityComplex, []domain.Stage{domain.StageReview}, nil, p)
	out := outcomeFor(sel, domain.StageReview)
	if !out.Enabled || !out.Forced {
		t.Fatalf("review should be enabled and forced, got %+v", out)
	}
	if !strings.Contains(out.Reason, "selected by user") || !strings.Contains(out.Reason, "forced") {
		t.Errorf("Reason = %q, want it to mention both", out.Reason)
	}
}

func TestApplyForced_ExcludedForcedStage(t *testing.T) {
	p := testPolicy()
	// review is forced for complex; the user switched it off, and a non-forced stage in
	// excluded (research) changes nothing.
	sel := ApplyForced(domain.ComplexityComplex, []domain.Stage{domain.StageDevelopment},
		[]domain.Stage{domain.StageReview, domain.StageResearch}, p)
	out := outcomeFor(sel, domain.StageReview)
	if out.Enabled || !out.Forced || !strings.Contains(out.Reason, "excluded by user") {
		t.Errorf("review = %+v, want forced but disabled by the user", out)
	}
	if !enabled(sel, domain.StageAnalysis) || !enabled(sel, domain.StageDevelopment) || enabled(sel, domain.StageResearch) {
		t.Errorf("other stages changed: %+v", sel.EnabledStages())
	}
}

func TestEvaluate(t *testing.T) {
	p := testPolicy()
	depth := domain.ContextDepthDecision{
		Probabilities: map[domain.ContextDepth]float64{domain.ContextDepthProject: 0.91, domain.ContextDepthNone: 0.09},
	}
	classification := domain.TaskClassification{
		ContextDepth: domain.ContextDepthProject,
		Complexity: map[domain.ComplexitySource]domain.ComplexityDecision{
			domain.ComplexitySourceChoice: complexityDist(0.05, 0.63, 0.32),
			domain.ComplexitySourceScore:  complexityDist(0.05, 0.60, 0.35),
		},
		Stages:   stageDecisions(0.31, 0.92, 0.83, 0.99, 0.96, 0.82),
		Provider: "jev",
		Model:    "jev-1.13.0",
	}

	dec, err := Evaluate(depth, classification, domain.ComplexitySourceChoice, p)
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}

	if dec.Source != domain.DecisionSourceJev {
		t.Errorf("Source = %s, want jev", dec.Source)
	}
	if dec.ContextDepth.Selected != domain.ContextDepthProject {
		t.Errorf("ContextDepth.Selected = %s, want project", dec.ContextDepth.Selected)
	}
	if dec.ContextDepth.Probability != 0.91 || dec.ContextDepth.Threshold != 0.5 {
		t.Errorf("ContextDepth trace = %+v", dec.ContextDepth)
	}
	if dec.Complexity.Selected != domain.ComplexityMedium {
		t.Errorf("Complexity.Selected = %s, want medium", dec.Complexity.Selected)
	}
	if !dec.Complexity.Agree {
		t.Errorf("choice and score both selected medium, Agree should be true: %+v", dec.Complexity)
	}
	if len(dec.Complexity.Sources) != 2 {
		t.Errorf("expected both complexity sources in trace, got %d", len(dec.Complexity.Sources))
	}
	if s := dec.Complexity.Sources[domain.ComplexitySourceChoice].Size; s == nil || *s < 1.269 || *s > 1.271 {
		t.Errorf("choice size = %v, want 1.27", s)
	}
	if c := dec.Complexity.Cuts; c == nil || *c != p.ComplexityCuts {
		t.Errorf("Cuts = %v, want the policy's %v", c, p.ComplexityCuts)
	}
	wantStages(t, dec.Stages.EnabledStages(), domain.StageAnalysis, domain.StagePlanning, domain.StageDevelopment, domain.StageTesting, domain.StageReview)
}

func TestEvaluate_ShadowDisagreement(t *testing.T) {
	p := testPolicy()
	depth := domain.ContextDepthDecision{Probabilities: map[domain.ContextDepth]float64{domain.ContextDepthProject: 0.6, domain.ContextDepthNone: 0.4}}
	classification := domain.TaskClassification{
		ContextDepth: domain.ContextDepthProject,
		Complexity: map[domain.ComplexitySource]domain.ComplexityDecision{
			domain.ComplexitySourceChoice: complexityDist(0.10, 0.60, 0.30), // -> medium
			domain.ComplexitySourceScore:  complexityDist(0.05, 0.15, 0.80), // -> complex
		},
		Stages:   stageDecisions(0, 0.6, 0, 0, 0, 0),
		Provider: "jev",
	}

	dec, err := Evaluate(depth, classification, domain.ComplexitySourceChoice, p)
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if dec.Complexity.Agree {
		t.Error("choice (medium) and score (complex) disagree, Agree should be false")
	}
	if dec.Complexity.Selected != domain.ComplexityMedium {
		t.Errorf("decision must follow primary (choice=medium), got %s", dec.Complexity.Selected)
	}
	if dec.Complexity.Sources[domain.ComplexitySourceScore].Selected != domain.ComplexityComplex {
		t.Errorf("shadow (score) outcome should still be recorded as complex for the trace")
	}
}

func TestEvaluate_PrimaryNotAsked(t *testing.T) {
	p := testPolicy()
	depth := domain.ContextDepthDecision{Probabilities: map[domain.ContextDepth]float64{domain.ContextDepthProject: 0.1, domain.ContextDepthNone: 0.9}}
	classification := domain.TaskClassification{
		ContextDepth: domain.ContextDepthNone,
		Complexity: map[domain.ComplexitySource]domain.ComplexityDecision{
			domain.ComplexitySourceScore: complexityDist(0.9, 0.1, 0),
		},
		Stages: stageDecisions(0, 0.6, 0, 0, 0, 0),
	}

	_, err := Evaluate(depth, classification, domain.ComplexitySourceChoice, p)
	if err == nil {
		t.Fatal("expected an error when the primary complexity source was never asked")
	}
}

func outcomeFor(sel domain.StageSelection, stage domain.Stage) domain.StageOutcome {
	for _, o := range sel.Stages {
		if o.Stage == stage {
			return o
		}
	}
	return domain.StageOutcome{}
}

func enabled(sel domain.StageSelection, stage domain.Stage) bool {
	return outcomeFor(sel, stage).Enabled
}

func wantStages(t *testing.T, got []domain.Stage, want ...domain.Stage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
