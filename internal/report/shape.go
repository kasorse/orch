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
}

// IQR is the interquartile range of the sizes.
func (g ShapeGroup) IQR() float64 { return g.Q75 - g.Q25 }

// Shape groups the used decisions (the ones Calibrate uses) by decision provider, model and
// questions hash, and describes each group's sizes. minRuns is the fewest runs a level needs
// before a cut is proposed for it.
func Shape(entries []RunEntry, target SizeTarget, minRuns int) []ShapeGroup {
	minRuns = max(minRuns, 1)
	type acc struct {
		sizes  []float64
		probs  map[domain.Stage][][2]float64 // (size, probability) pairs
		levels map[domain.Complexity]int
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
func renderShape(w io.Writer, groups []ShapeGroup, target SizeTarget) {
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
	}
}
