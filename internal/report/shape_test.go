package report

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"orchestrator/internal/domain"
)

// sizedEntry is a used run whose primary distribution has the given size, with planning's
// probability set to planning (nil leaves the stage without one).
func sizedEntry(size float64, planning *float64, questions string) RunEntry {
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
		Stages: []domain.StageOutcome{{Stage: domain.StagePlanning, Probability: planning}},
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
	g := Shape(entries, DefaultSizeTarget, 1)
	if len(g) != 1 || len(g[0].Correlations) != 1 {
		t.Fatalf("Shape = %+v", g)
	}
	c := g[0].Correlations[0]
	if c.Stage != domain.StagePlanning || math.Abs(c.R-1) > 1e-9 || !c.Redundant() || c.Runs != 10 {
		t.Errorf("planning = %+v, want r = 1 and redundant", c)
	}
}

func TestShape_NoSpreadRefusesCuts(t *testing.T) {
	var entries []RunEntry
	for range 20 {
		entries = append(entries, sizedEntry(1.05, nil, ""))
	}
	g := Shape(entries, DefaultSizeTarget, 1)[0]
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
	g := Shape(entries, DefaultSizeTarget, 3)[0]
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
	if g := Shape(entries, DefaultSizeTarget, 4)[0]; g.Cuts != nil || !strings.Contains(g.NoCuts, "not enough data") {
		t.Errorf("min runs 4: cuts = %v, %q", g.Cuts, g.NoCuts)
	}
}

func TestShape_GroupsAndSkips(t *testing.T) {
	manual := sizedEntry(1, nil, "")
	manual.Trace.Source = domain.DecisionSourceManual
	entries := []RunEntry{sizedEntry(1, nil, "aa"), sizedEntry(1, nil, "bb"), manual, {}}
	g := Shape(entries, DefaultSizeTarget, 1)
	if len(g) != 2 || g[0].Key.QuestionsHash != "aa" || g[1].Key.QuestionsHash != "bb" || g[0].Runs != 1 {
		t.Errorf("groups = %+v, want one per questions hash, manual and traceless runs left out", g)
	}

	var out bytes.Buffer
	renderShape(&out, g, DefaultSizeTarget)
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
