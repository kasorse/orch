package report

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// sizedEntry is a used run whose primary distribution has the given size, with investigation's
// probability set to investigation (nil leaves the stage without one).
func sizedEntry(size float64, investigation *float64, questions string) RunEntry {
	probs := map[domain.Complexity]float64{domain.ComplexitySimple: 1 - size, domain.ComplexityMedium: size}
	if size > 1 {
		probs = map[domain.Complexity]float64{domain.ComplexityMedium: 2 - size, domain.ComplexityComplex: size - 1}
	}
	t := &domain.DecisionTrace{
		Source: domain.DecisionSourceLLM, Provider: "claude", Model: "opus", QuestionsHash: questions,
		Complexity: domain.ComplexityOutcome{
			Primary:  domain.ComplexitySourceChoice,
			Selected: domain.ComplexityMedium,
			Sources: map[domain.ComplexitySource]domain.ComplexitySourceOutcome{
				domain.ComplexitySourceChoice: {Decision: domain.ComplexityDecision{Probabilities: probs}},
			},
		},
		Stages: []domain.StageOutcome{{Stage: domain.StageInvestigation, Probability: investigation}},
	}
	return RunEntry{Trace: t}
}

func TestShape_RedundantStage(t *testing.T) {
	var entries []RunEntry
	for i := range 10 {
		size := 0.1 + 0.15*float64(i)
		p := size / 2
		entries = append(entries, sizedEntry(size, &p, ""))
	}
	g := Shape(entries, DefaultSizeTarget, 1, DefaultSimpleMaxHard)
	if len(g) != 1 || len(g[0].Correlations) != 1 {
		t.Fatalf("Shape = %+v", g)
	}
	c := g[0].Correlations[0]
	if c.Stage != domain.StageInvestigation || math.Abs(c.R-1) > 1e-9 || !c.Redundant() || c.Runs != 10 {
		t.Errorf("investigation = %+v, want r = 1 and redundant", c)
	}
}

func TestShape_NoSpreadRefusesCuts(t *testing.T) {
	var entries []RunEntry
	for range 20 {
		entries = append(entries, sizedEntry(1.05, nil, ""))
	}
	g := Shape(entries, DefaultSizeTarget, 1, DefaultSimpleMaxHard)[0]
	if g.Cuts != nil || !strings.Contains(g.NoCuts, "size does not separate tasks") {
		t.Errorf("cuts = %v, %q; want the refusal", g.Cuts, g.NoCuts)
	}
	if len(g.Correlations) != 0 {
		t.Errorf("a stage without probabilities got a correlation: %+v", g.Correlations)
	}
}

// 20 sizes 0.05, 0.15, ..., 1.95: 50/35/15 puts 10 below the medium cut and 17 below the
// complex cut, midway between neighbours.
func TestShape_TargetCuts(t *testing.T) {
	var entries []RunEntry
	for i := range 20 {
		entries = append(entries, sizedEntry(0.05+0.1*float64(i), nil, ""))
	}
	g := Shape(entries, DefaultSizeTarget, 3, DefaultSimpleMaxHard)[0]
	if g.Cuts == nil {
		t.Fatalf("no cuts: %s", g.NoCuts)
	}
	if math.Abs(g.Cuts.Medium-1.0) > 1e-9 || math.Abs(g.Cuts.Complex-1.7) > 1e-9 {
		t.Errorf("cuts = %+v, want medium 1.00, complex 1.70", *g.Cuts)
	}
	if math.Abs(g.Median-1.0) > 1e-9 || g.Min != 0.05 || math.Abs(g.Max-1.95) > 1e-9 {
		t.Errorf("median %v, min %v, max %v", g.Median, g.Min, g.Max)
	}
	for i, n := range g.Bins {
		if n != 1 {
			t.Errorf("bin %d has %d sizes, want 1", i, n)
		}
	}

	// complex would get 3 runs: under --min-runs 4 nothing is proposed.
	if g := Shape(entries, DefaultSizeTarget, 4, DefaultSimpleMaxHard)[0]; g.Cuts != nil || !strings.Contains(g.NoCuts, "not enough data") {
		t.Errorf("min runs 4: cuts = %v, %q", g.Cuts, g.NoCuts)
	}
}

func TestShape_GroupsAndSkips(t *testing.T) {
	manual := sizedEntry(1, nil, "")
	manual.Trace.Source = domain.DecisionSourceManual
	entries := []RunEntry{sizedEntry(1, nil, "aa"), sizedEntry(1, nil, "bb"), manual, {}}
	g := Shape(entries, DefaultSizeTarget, 1, DefaultSimpleMaxHard)
	if len(g) != 2 || g[0].Key.QuestionsHash != "aa" || g[1].Key.QuestionsHash != "bb" || g[0].Runs != 1 {
		t.Errorf("groups = %+v, want one per questions hash, manual and traceless runs left out", g)
	}

	var out bytes.Buffer
	renderShape(&out, g, DefaultSizeTarget, DefaultSimpleMaxHard)
	if !strings.Contains(out.String(), "== claude/opus  questions") || !strings.Contains(out.String(), "cuts for 50/35/15:") {
		t.Errorf("rendered:\n%s", out.String())
	}
}

// Equal sizes at the share stay in one level: the cut moves to the nearest gap.
func TestGapCut(t *testing.T) {
	s := []float64{1.0, 1.2, 1.45, 1.45, 1.45, 1.5, 1.6}
	if got := gapCut(s, 3); math.Abs(got-1.325) > 1e-9 {
		t.Errorf("gapCut at a tie = %v, want 1.325 (the gap below it)", got)
	}
	if got := gapCut(s, 5); math.Abs(got-1.475) > 1e-9 {
		t.Errorf("gapCut on a gap = %v, want 1.475", got)
	}
	if got := gapCut([]float64{1, 1, 1}, 1); !math.IsNaN(got) {
		t.Errorf("gapCut with no gap = %v, want NaN", got)
	}
}

func withOutcome(e RunEntry, status domain.RunStatus, fix int) RunEntry {
	e.Run.Outcome = &domain.Outcome{Status: status, FixIterations: fix}
	return e
}

// Sizes 0.05 .. 1.95; the runs at 0.65 and above needed a fix. Below 0.7 one run in 7 was hard
// (14%), below 0.8 two in 8 (25%), so the outcome rule allows simple up to 0.7, lower than the
// target's medium cut of 1.0.
func TestShape_OutcomeRule(t *testing.T) {
	var entries []RunEntry
	for i := range 20 {
		size := 0.05 + 0.1*float64(i)
		fix := 0
		if size > 0.6 {
			fix = 1
		}
		entries = append(entries, withOutcome(sizedEntry(size, nil, ""), domain.RunCompleted, fix))
	}
	g := Shape(entries, DefaultSizeTarget, 3, DefaultSimpleMaxHard)[0]
	if g.OutcomeCut == nil || math.Abs(*g.OutcomeCut-0.7) > 1e-9 {
		t.Fatalf("outcome cut = %v (%s), want 0.7", g.OutcomeCut, g.OutcomeNote)
	}
	p := g.Proposal()
	if p == nil || math.Abs(p.Medium-0.7) > 1e-9 || math.Abs(p.Complex-1.7) > 1e-9 {
		t.Errorf("proposal = %+v, want medium 0.70 (the outcome's), complex 1.70 (the target's)", p)
	}
	if b := g.Outcome[0]; b.Runs != 2 || b.Hard != 0 {
		t.Errorf("first outcome bin = %+v", b)
	}

	var out bytes.Buffer
	renderCutsDiff(&out, []ShapeGroup{g})
	for _, want := range []string{"-   cuts: —", "+   cuts: {medium: 0.70, complex: 1.70}", "lowered from 1.00"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("diff lacks %q:\n%s", want, out.String())
		}
	}
}

// A failed run without a fix iteration is the environment: it is left out of the outcome table.
// Stopped by a limit is hard.
func TestShape_OutcomeCounting(t *testing.T) {
	entries := []RunEntry{
		withOutcome(sizedEntry(0.5, nil, ""), domain.RunFailed, 0),
		withOutcome(sizedEntry(0.5, nil, ""), domain.RunStopped, 0),
		withOutcome(sizedEntry(0.5, nil, ""), domain.RunFailed, 2),
		withOutcome(sizedEntry(0.5, nil, ""), domain.RunCompleted, 0),
	}
	b := Shape(entries, DefaultSizeTarget, 1, DefaultSimpleMaxHard)[0].Outcome[2]
	if b.Runs != 3 || b.Hard != 2 {
		t.Errorf("bin 0.4-0.6 = %+v, want 3 runs, 2 hard", b)
	}
}

func TestShape_SizeDoesNotPredictOutcome(t *testing.T) {
	var entries []RunEntry
	for i := range 20 {
		size := 0.05 + 0.1*float64(i)
		fix := 0
		if size < 1 { // the small tasks are the hard ones
			fix = 1
		}
		entries = append(entries, withOutcome(sizedEntry(size, nil, ""), domain.RunCompleted, fix))
	}
	g := Shape(entries, DefaultSizeTarget, 3, DefaultSimpleMaxHard)[0]
	if g.OutcomeCut != nil || !strings.Contains(g.OutcomeNote, "size does not predict outcome") {
		t.Errorf("outcome cut = %v, %q", g.OutcomeCut, g.OutcomeNote)
	}
	if p := g.Proposal(); p == nil || *p != *g.Cuts {
		t.Errorf("proposal = %v, want the target's cuts alone", p)
	}
}

// The refusal on no spread suppresses both rules.
func TestShape_RefusalSuppressesBothRules(t *testing.T) {
	var entries []RunEntry
	for i := range 20 {
		entries = append(entries, withOutcome(sizedEntry(1.0+0.001*float64(i), nil, ""), domain.RunCompleted, i%2))
	}
	g := Shape(entries, DefaultSizeTarget, 3, DefaultSimpleMaxHard)[0]
	if g.Proposal() != nil || g.OutcomeCut != nil || g.OutcomeNote != "size does not separate tasks" {
		t.Errorf("proposal %v, outcome cut %v (%q); want neither", g.Proposal(), g.OutcomeCut, g.OutcomeNote)
	}
}

// Size does not depend on the cuts: two policies with one wording are one group, and the diff
// starts from "mixed".
func TestShape_GroupsAcrossPolicyHashes(t *testing.T) {
	var entries []RunEntry
	for i := range 20 {
		e := sizedEntry(0.05+0.1*float64(i), nil, "aa")
		e.Trace.PolicyHash = []string{"p1", "p2"}[i%2]
		e.Trace.Complexity.Cuts = &[]domain.ComplexityCuts{{Medium: 0.5, Complex: 1.4}, {Medium: 0.6, Complex: 1.5}}[i%2]
		entries = append(entries, e)
	}
	g := Shape(entries, DefaultSizeTarget, 3, DefaultSimpleMaxHard)
	if len(g) != 1 || g[0].Runs != 20 || !g[0].FromMixed {
		t.Fatalf("groups = %+v, want one group of 20 with mixed cuts", g)
	}
	var out bytes.Buffer
	renderCutsDiff(&out, g)
	if !strings.Contains(out.String(), "-   cuts: mixed") {
		t.Errorf("diff:\n%s", out.String())
	}

	// Decided under exactly the proposal: no change.
	for i := range entries {
		entries[i].Trace.Complexity.Cuts = &domain.ComplexityCuts{Medium: 1.0, Complex: 1.7}
	}
	out.Reset()
	renderCutsDiff(&out, Shape(entries, DefaultSizeTarget, 3, DefaultSimpleMaxHard))
	if !strings.Contains(out.String(), "  no change proposed") {
		t.Errorf("diff:\n%s", out.String())
	}
}
