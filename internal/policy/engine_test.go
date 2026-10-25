package policy

import (
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// testPolicy: the asked stages' thresholds of specs/JEV integration/TASKS.md T02 (research
// 0.70, development 0.60), the example's template (medium: planning, review; complex:
// planning, review) and size cuts 0.5 and 1.4.
func testPolicy() domain.DecisionPolicy {
	return domain.DecisionPolicy{
		ProjectContextThreshold: 0.5,
		ComplexityCuts:          domain.ComplexityCuts{Medium: 0.5, Complex: 1.4},
		StageThreshold: map[domain.Stage]float64{
			domain.StageResearch:    0.70,
			domain.StageDevelopment: 0.60,
		},
		ForceStages: map[domain.Complexity][]domain.Stage{
			domain.ComplexityMedium:  {domain.StagePlanning, domain.StageReview},
			domain.ComplexityComplex: {domain.StagePlanning, domain.StageReview},
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

func stageDecisions(research, development float64) domain.StageDecisions {
	var d domain.StageDecisions
	d.Set(domain.StageResearch, domain.ProbabilityDecision{Probability: research})
	d.Set(domain.StageDevelopment, domain.ProbabilityDecision{Probability: development})
	return d
}

func TestSelectStages_ThresholdBoundary(t *testing.T) {
	p := testPolicy()
	if sel := SelectStages(stageDecisions(0, 0.59), domain.ComplexitySimple, p); enabled(sel, domain.StageDevelopment) {
		t.Error("development at p=0.59 (threshold 0.60) should be disabled")
	}
	if sel := SelectStages(stageDecisions(0, 0.60), domain.ComplexitySimple, p); !enabled(sel, domain.StageDevelopment) {
		t.Error("development at p=0.60 (threshold 0.60) should be enabled")
	}
}

// One case per row of specs/decision-shape/SPEC.md §6.3, plus research and the template on a
// question.
func TestSelectStages_Shape(t *testing.T) {
	p := testPolicy()
	pl, r, d, te, rv := domain.StagePlanning, domain.StageResearch, domain.StageDevelopment, domain.StageTesting, domain.StageReview
	for _, tc := range []struct {
		name       string
		decisions  domain.StageDecisions
		complexity domain.Complexity
		want       []domain.Stage
	}{
		{"simple change", stageDecisions(0.1, 0.99), domain.ComplexitySimple, []domain.Stage{d, te}},
		{"medium change", stageDecisions(0.1, 0.99), domain.ComplexityMedium, []domain.Stage{pl, d, te, rv}},
		{"complex change", stageDecisions(0.1, 0.99), domain.ComplexityComplex, []domain.Stage{pl, d, te, rv}},
		{"complex change that needs research", stageDecisions(0.8, 0.99), domain.ComplexityComplex, []domain.Stage{r, pl, d, te, rv}},
		{"a simple question that needs research", stageDecisions(0.8, 0.1), domain.ComplexitySimple, []domain.Stage{r}},
		{"a simple question falls back to planning", stageDecisions(0.1, 0.1), domain.ComplexitySimple, []domain.Stage{pl}},
		{"a design question keeps the plan, drops review", stageDecisions(0.1, 0.1), domain.ComplexityComplex, []domain.Stage{pl}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantStages(t, SelectStages(tc.decisions, tc.complexity, p).EnabledStages(), tc.want...)
		})
	}
}

func TestSelectStages_Reasons(t *testing.T) {
	p := testPolicy()

	sel := SelectStages(stageDecisions(0.1, 0.99), domain.ComplexitySimple, p)
	if out := outcomeFor(sel, domain.StageTesting); !out.Enabled || out.Reason != "with development" || out.Probability != nil || out.Threshold != nil {
		t.Errorf("testing = %+v, want enabled with development and no probability", out)
	}
	if out := outcomeFor(sel, domain.StagePlanning); out.Enabled || out.Reason != "not asked, not in the template" {
		t.Errorf("planning = %+v", out)
	}

	sel = SelectStages(stageDecisions(0.9, 0.1), domain.ComplexityComplex, p)
	if out := outcomeFor(sel, domain.StageReview); out.Enabled || !out.Forced || out.Reason != "dropped: no code change" {
		t.Errorf("review = %+v, want forced but dropped without development", out)
	}
	if out := outcomeFor(sel, domain.StagePlanning); !out.Enabled || out.Reason != "forced: complexity=complex" {
		t.Errorf("planning = %+v", out)
	}
	if out := outcomeFor(sel, domain.StageResearch); !out.Enabled || !strings.Contains(out.Reason, "0.90") {
		t.Errorf("research = %+v, want it on with its probability in the reason", out)
	}
}

// TestSelectStages_EmptyFallback is TASKS.md В6: nothing clears its threshold and
// nothing is forced (complexity=simple has no force list) - planning alone is
// enabled rather than an empty workflow.
func TestSelectStages_EmptyFallback(t *testing.T) {
	p := testPolicy()
	sel := SelectStages(stageDecisions(0.01, 0.01), domain.ComplexitySimple, p)

	got := sel.EnabledStages()
	if len(got) != 1 || got[0] != domain.StagePlanning {
		t.Fatalf("EnabledStages() = %v, want [planning]", got)
	}
	out := outcomeFor(sel, domain.StagePlanning)
	if out.Forced {
		t.Error("the В6 fallback stage should not be reported as forced by complexity")
	}
	if want := "forced: no stage cleared its threshold, defaulting to planning"; out.Reason != want {
		t.Errorf("Reason = %q, want %q", out.Reason, want)
	}
}

func TestSelectStages_Deterministic(t *testing.T) {
	p := testPolicy()
	d := stageDecisions(0.72, 0.99)

	a := SelectStages(d, domain.ComplexityMedium, p)
	b := SelectStages(d, domain.ComplexityMedium, p)

	if len(a.Stages) != len(domain.StageOrder()) || len(a.Stages) != len(b.Stages) {
		t.Fatalf("got %d and %d outcomes, want one per stage", len(a.Stages), len(b.Stages))
	}
	for i := range a.Stages {
		if a.Stages[i].Stage != b.Stages[i].Stage || a.Stages[i].Enabled != b.Stages[i].Enabled {
			t.Errorf("stage %d differs between calls: %+v vs %+v", i, a.Stages[i], b.Stages[i])
		}
	}
}

func TestApplyForced(t *testing.T) {
	p := testPolicy()

	sel := ApplyForced(domain.ComplexityComplex, []domain.Stage{domain.StageDevelopment, domain.StageTesting}, nil, p)

	for _, st := range []domain.Stage{domain.StagePlanning, domain.StageReview} {
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
	if !enabled(sel, domain.StagePlanning) || !enabled(sel, domain.StageDevelopment) || enabled(sel, domain.StageResearch) {
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
		Stages:   stageDecisions(0.31, 0.99),
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
	wantStages(t, dec.Stages.EnabledStages(), domain.StagePlanning, domain.StageDevelopment, domain.StageTesting, domain.StageReview)
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
		Stages:   stageDecisions(0, 0.6),
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
		Stages: stageDecisions(0, 0.6),
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
