package report

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"

	"orchestrator/internal/domain"
)

// This file is the shape section of `orchestrator decisions calibrate`
// (specs/decision-shape/SPEC.md §7.2): how the decisions' sizes spread, which stage answers
// only repeat the size, and the cuts that would give a target share per level. Like the rest of
// calibrate, it only reads and proposes.

// SizeTarget is the share of runs, in percent, each complexity should get: simple, medium,
// complex. Each is positive and they sum to 100.
type SizeTarget [3]int

// DefaultSizeTarget is the owner's default (specs/decision-shape/SPEC.md §11, O2).
var DefaultSizeTarget = SizeTarget{50, 35, 15}

func (t SizeTarget) String() string { return fmt.Sprintf("%d/%d/%d", t[0], t[1], t[2]) }

// sizeBins is the number of size bins of 0.1 between 0 and 2.
const sizeBins = 20

// minSizeIQR is the smallest interquartile range of size that separates tasks: below it the
// cuts would only split noise (SPEC.md §7.3).
const minSizeIQR = 0.2

// redundantR is the correlation with size above which a stage answer adds nothing to it.
const redundantR = 0.8

// DefaultSimpleMaxHard is the largest share of hard runs the simple level may hold
// (SPEC.md §7.3, rule 2).
const DefaultSimpleMaxHard = 0.2

// outcomeBins is the number of size bins of 0.2 the outcome table uses.
const outcomeBins = 10

// ShapeKey is what the shape section groups by: sizes from different decision models or from
// differently worded questions are on different scales (SPEC.md §7.1). An empty
// QuestionsHash is the group of traces written before the hash existed.
type ShapeKey struct {
	Provider      string
	Model         string
	QuestionsHash string
}

// StageCorrelation is Pearson's r between one stage's probability and size, over the Runs
// traces that have that probability; R is NaN when it is undefined (under 3 runs, or either
// side constant).
type StageCorrelation struct {
	Stage domain.Stage
	Runs  int
	R     float64
}

// Redundant reports whether the stage's answer only repeats the size.
func (c StageCorrelation) Redundant() bool { return !math.IsNaN(c.R) && c.R >= redundantR }

// ShapeGroup is one group's sizes and what they say.
type ShapeGroup struct {
	Key  ShapeKey
	Runs int
	// Min, Q25, Median, Q75 and Max describe the sizes, quartiles linearly interpolated.
	Min, Q25, Median, Q75, Max float64
	// Bins counts the sizes per 0.1, from [0, 0.1) to [1.9, 2].
	Bins [sizeBins]int
	// Levels counts the complexity each run was decided at, under the policy of its day.
	Levels       map[domain.Complexity]int
	Correlations []StageCorrelation
	// Cuts are the cuts for the target, nil when NoCuts says why not.
	Cuts   *domain.ComplexityCuts
	NoCuts string
	// Outcome counts, per size bin of 0.2, the runs with an outcome and the hard ones among them
	// (fix iterations, or stopped by a limit). A failed run without a fix iteration is left out:
	// on the measured data it is the environment, not the task (SPEC.md §1.1).
	Outcome [outcomeBins]HardCount
	// OutcomeCut is the highest medium cut below which simple runs stay at most SimpleMaxHard
	// hard, nil when OutcomeNote says why not.
	OutcomeCut  *float64
	OutcomeNote string
	// From are the cuts the group's decisions were made with: nil when they recorded none,
	// FromMixed when they differ.
	From      *domain.ComplexityCuts
	FromMixed bool
}

// HardCount is how many runs a bin holds and how many of them were hard.
type HardCount struct{ Runs, Hard int }

// Proposal is the cuts to write: the target's, with medium lowered to OutcomeCut when that is
// lower and still under the complex cut. nil when the target gives none.
func (g ShapeGroup) Proposal() *domain.ComplexityCuts {
	if g.Cuts == nil {
		return nil
	}
	p := *g.Cuts
	if g.OutcomeCut != nil && *g.OutcomeCut < p.Medium && *g.OutcomeCut > 0 {
		p.Medium = *g.OutcomeCut
	}
	return &p
}

// IQR is the interquartile range of the sizes.
func (g ShapeGroup) IQR() float64 { return g.Q75 - g.Q25 }

// Shape groups the used decisions (the ones Calibrate uses) by decision provider, model and
// questions hash - not by policy hash: size does not depend on the cuts, so applying a proposal
// must not start the history over (SPEC.md §7.1) - and describes each group's sizes and
// outcomes. minRuns is the fewest runs a level, or the simple runs of the outcome rule, need;
// simpleMaxHard is the share of hard runs the simple level may hold.
func Shape(entries []RunEntry, target SizeTarget, minRuns int, simpleMaxHard float64) []ShapeGroup {
	minRuns = max(minRuns, 1)
	type acc struct {
		sizes  []float64
		probs  map[domain.Stage][][2]float64 // (size, probability) pairs
		levels map[domain.Complexity]int
		hard   []sizedOutcome
		cuts   []domain.ComplexityCuts
	}
	groups := map[ShapeKey]*acc{}
	for _, e := range entries {
		t := e.Trace
		if t == nil || calibrationSkip(t) != "" {
			continue
		}
		src, ok := t.Complexity.Sources[t.Complexity.Primary]
		if !ok {
			continue
		}
		size := src.Decision.Size()
		key := ShapeKey{Provider: t.Provider, Model: t.Model, QuestionsHash: t.QuestionsHash}
		a := groups[key]
		if a == nil {
			a = &acc{probs: map[domain.Stage][][2]float64{}, levels: map[domain.Complexity]int{}}
			groups[key] = a
		}
		a.sizes = append(a.sizes, size)
		a.levels[t.Complexity.Selected]++
		if c := t.Complexity.Cuts; c != nil {
			a.cuts = append(a.cuts, *c)
		}
		if out := runOutcome(e); out.Status != domain.RunFailed || out.FixIterations > 0 {
			a.hard = append(a.hard, sizedOutcome{size, out.FixIterations > 0 || out.Status == domain.RunStopped})
		}
		for _, so := range t.Stages {
			if so.Probability != nil {
				a.probs[so.Stage] = append(a.probs[so.Stage], [2]float64{size, *so.Probability})
			}
		}
	}

	var out []ShapeGroup
	for key, a := range groups {
		s := slices.Sorted(slices.Values(a.sizes))
		g := ShapeGroup{
			Key: key, Runs: len(s), Levels: a.levels,
			Min: s[0], Q25: quantile(s, 0.25), Median: quantile(s, 0.5), Q75: quantile(s, 0.75), Max: s[len(s)-1],
		}
		for _, v := range s {
			g.Bins[min(int(v*10+probEps), sizeBins-1)]++
		}
		for _, st := range domain.StageOrder() {
			if pairs := a.probs[st]; len(pairs) > 0 {
				g.Correlations = append(g.Correlations, StageCorrelation{Stage: st, Runs: len(pairs), R: pearson(pairs)})
			}
		}
		g.Cuts, g.NoCuts = targetCuts(s, g.IQR(), target, minRuns)
		for _, h := range a.hard {
			b := &g.Outcome[min(int(h.size*5+probEps), outcomeBins-1)]
			b.Runs++
			if h.hard {
				b.Hard++
			}
		}
		if g.Cuts == nil && strings.HasPrefix(g.NoCuts, "size does not") {
			g.OutcomeNote = "size does not separate tasks"
		} else {
			g.OutcomeCut, g.OutcomeNote = outcomeCut(a.hard, minRuns, simpleMaxHard)
		}
		if len(a.cuts) > 0 {
			from := a.cuts[0]
			g.From = &from
			g.FromMixed = slices.ContainsFunc(a.cuts, func(c domain.ComplexityCuts) bool { return c != from })
		}
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b ShapeGroup) int {
		return cmp.Or(
			strings.Compare(a.Key.Provider, b.Key.Provider),
			strings.Compare(a.Key.Model, b.Key.Model),
			strings.Compare(a.Key.QuestionsHash, b.Key.QuestionsHash))
	})
	return out
}

// targetCuts places each cut midway between the last size of a level and the first of the
// next, at the target's shares of the sorted sizes s. Equal sizes stay in one level: the cut
// moves to the gap between different sizes nearest to the share. It proposes nothing when the
// sizes do not spread, a level would get under minRuns runs, or no gap is left between levels.
func targetCuts(s []float64, iqr float64, target SizeTarget, minRuns int) (*domain.ComplexityCuts, string) {
	if iqr < minSizeIQR-probEps {
		return nil, fmt.Sprintf("size does not separate tasks (interquartile range %.2f, under %.2f)", iqr, minSizeIQR)
	}
	n := len(s)
	k1 := n * target[0] / 100
	k2 := n * (target[0] + target[1]) / 100
	if k1 < minRuns || k2-k1 < minRuns || n-k2 < minRuns {
		return nil, fmt.Sprintf("not enough data (%d runs; each level needs %d)", n, minRuns)
	}
	cuts := domain.ComplexityCuts{Medium: gapCut(s, k1), Complex: gapCut(s, k2)}
	if !(0 < cuts.Medium && cuts.Medium < cuts.Complex && cuts.Complex <= 2) {
		return nil, fmt.Sprintf("equal sizes leave no gap for %s (medium %.2f, complex %.2f)", target, cuts.Medium, cuts.Complex)
	}
	return &cuts, ""
}

// cutsYAML is cuts as the config writes them, to two decimals: what the diff prints is also
// what it compares.
func cutsYAML(c domain.ComplexityCuts) string {
	return fmt.Sprintf("{medium: %.2f, complex: %.2f}", c.Medium, c.Complex)
}

// sizedOutcome is one run's size and whether it was hard.
type sizedOutcome struct {
	size float64
	hard bool
}

// outcomeCut is rule 2 of SPEC.md §7.3: the highest edge e of 0.1 such that the runs with size
// below e number at least minRuns and at most maxHard of them were hard. It proposes nothing
// when the hard share does not grow with size (the runs below the median size are hard at
// least as often as the rest) or when no edge keeps simple runs that easy.
func outcomeCut(runs []sizedOutcome, minRuns int, maxHard float64) (*float64, string) {
	if len(runs) < 2*minRuns {
		return nil, fmt.Sprintf("not enough runs with an outcome (%d; needs %d)", len(runs), 2*minRuns)
	}
	sorted := slices.SortedFunc(slices.Values(runs), func(a, b sizedOutcome) int { return cmp.Compare(a.size, b.size) })
	half := len(sorted) / 2
	if hardShare(sorted[:half]) >= hardShare(sorted[half:]) {
		return nil, "size does not predict outcome (runs below the median size are hard at least as often)"
	}
	var best *float64
	for edge := 1; edge <= sizeBins; edge++ {
		e := float64(edge) / 10
		i, _ := slices.BinarySearchFunc(sorted, e-probEps, func(r sizedOutcome, t float64) int { return cmp.Compare(r.size, t) })
		if below := sorted[:i]; len(below) >= minRuns && hardShare(below) <= maxHard+probEps {
			best = &e
		}
	}
	if best == nil {
		return nil, fmt.Sprintf("no size keeps %d or more simple runs at most %.0f%% hard", minRuns, 100*maxHard)
	}
	return best, ""
}

func hardShare(runs []sizedOutcome) float64 {
	hard := 0
	for _, r := range runs {
		if r.hard {
			hard++
		}
	}
	return float64(hard) / float64(len(runs))
}

// gapCut is the midpoint of the gap between different sizes nearest to index k of the sorted
// s (the gap before s[i] for the i closest to k with s[i-1] < s[i]; below k on a tie), or NaN
// when all sizes are equal.
func gapCut(s []float64, k int) float64 {
	for d := 0; d < len(s); d++ {
		for _, i := range []int{k - d, k + d} {
			if i >= 1 && i < len(s) && s[i-1] < s[i] {
				return (s[i-1] + s[i]) / 2
			}
		}
	}
	return math.NaN()
}

// quantile is the q-quantile of the sorted s, linearly interpolated.
func quantile(s []float64, q float64) float64 {
	i := q * float64(len(s)-1)
	lo := int(i)
	hi := min(lo+1, len(s)-1)
	return s[lo] + (s[hi]-s[lo])*(i-float64(lo))
}

// pearson is the correlation of the pairs' two sides, NaN when undefined.
func pearson(pairs [][2]float64) float64 {
	n := float64(len(pairs))
	if len(pairs) < 3 {
		return math.NaN()
	}
	var mx, my float64
	for _, p := range pairs {
		mx += p[0]
		my += p[1]
	}
	mx, my = mx/n, my/n
	var sxy, sxx, syy float64
	for _, p := range pairs {
		dx, dy := p[0]-mx, p[1]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx < probEps || syy < probEps {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

// renderShape writes the shape section; a group is headed by its decision model and the first
// digits of its questions hash.
func renderShape(w io.Writer, groups []ShapeGroup, target SizeTarget, simpleMaxHard float64) {
	if len(groups) == 0 {
		return
	}
	fmt.Fprintln(w, "\nsize and stages (specs/decision-shape/SPEC.md §7.2)")
	for _, g := range groups {
		fmt.Fprintf(w, "\n== %s/%s  questions %s  (%d runs)\n", orNoValue(g.Key.Provider), orNoValue(g.Key.Model), shortHash(g.Key.QuestionsHash), g.Runs)
		fmt.Fprintf(w, "size: min %.2f  q25 %.2f  median %.2f  q75 %.2f  max %.2f  (interquartile range %.2f)\n",
			g.Min, g.Q25, g.Median, g.Q75, g.Max, g.IQR())
		for i, c := range g.Bins {
			if c > 0 {
				fmt.Fprintf(w, "  %.1f-%.1f %4d  %s\n", float64(i)/10, float64(i+1)/10, c, strings.Repeat("#", c))
			}
		}
		var levels []string
		for _, c := range domain.ComplexityOrder() {
			levels = append(levels, fmt.Sprintf("%s %d", c, g.Levels[c]))
		}
		fmt.Fprintf(w, "levels decided: %s\n", strings.Join(levels, ", "))
		fmt.Fprintln(w, "stage P against size (Pearson r):")
		for _, c := range g.Correlations {
			r := noValue
			if !math.IsNaN(c.R) {
				r = fmt.Sprintf("%+.2f", c.R)
			}
			mark := ""
			if c.Redundant() {
				mark = "  redundant with size"
			}
			fmt.Fprintf(w, "  %-12s %5s  (%d runs)%s\n", c.Stage, r, c.Runs, mark)
		}
		if g.Cuts == nil {
			fmt.Fprintf(w, "cuts for %s: %s\n", target, g.NoCuts)
		} else {
			fmt.Fprintf(w, "cuts for %s: medium %.2f, complex %.2f\n", target, g.Cuts.Medium, g.Cuts.Complex)
		}
		fmt.Fprintln(w, "hard runs (fix iterations or stopped) by size:")
		for i, b := range g.Outcome {
			if b.Runs > 0 {
				fmt.Fprintf(w, "  %.1f-%.1f %4d  %d hard\n", float64(i)/5, float64(i+1)/5, b.Runs, b.Hard)
			}
		}
		if g.OutcomeCut == nil {
			fmt.Fprintf(w, "medium cut by outcome (simple at most %.0f%% hard): %s\n", 100*simpleMaxHard, g.OutcomeNote)
		} else {
			fmt.Fprintf(w, "medium cut by outcome (simple at most %.0f%% hard): %.2f\n", 100*simpleMaxHard, *g.OutcomeCut)
		}
	}
}

// renderCutsDiff writes the proposal of policy.complexity.cuts per group, as a diff against the
// cuts the group's decisions were made with.
func renderCutsDiff(w io.Writer, groups []ShapeGroup) {
	if len(groups) == 0 {
		return
	}
	fmt.Fprintln(w, "\npolicy.complexity.cuts (a proposal; nothing is written)")
	for _, g := range groups {
		fmt.Fprintf(w, "# %s/%s  questions %s\n", orNoValue(g.Key.Provider), orNoValue(g.Key.Model), shortHash(g.Key.QuestionsHash))
		p := g.Proposal()
		switch {
		case p == nil:
			fmt.Fprintf(w, "  no change proposed: %s\n", g.NoCuts)
			continue
		case g.From != nil && !g.FromMixed && cutsYAML(*g.From) == cutsYAML(*p):
			fmt.Fprintln(w, "  no change proposed")
			continue
		}
		from := noValue
		if g.FromMixed {
			from = "mixed"
		} else if g.From != nil {
			from = cutsYAML(*g.From)
		}
		fmt.Fprintf(w, "-   cuts: %s\n", from)
		fmt.Fprintf(w, "+   cuts: %s\n", cutsYAML(*p))
		if p.Medium != g.Cuts.Medium {
			fmt.Fprintf(w, "      # medium lowered from %.2f (the target's) by the outcome rule\n", g.Cuts.Medium)
		}
	}
}
