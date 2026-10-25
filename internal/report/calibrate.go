package report

import (
	"cmp"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"orchestrator/internal/domain"
)

// This file is `orchestrator decisions calibrate` (specs/governance/SPEC.md §7.1–§7.3). It
// reads what runs wrote and proposes; nothing here writes a file or a config.
//
// The lowering rule reads the runs where policy.explore turned a stage on below its threshold
// (§7.4, stages[].explored); the raise rule never does, since its windows start at the threshold.

// bucketCount is the number of probability buckets of 0.1 between 0 and 1.
const bucketCount = 10

// probEps absorbs the float error of a probability that sits on a bucket edge (0.3*10 is not
// always 3).
const probEps = 1e-9

// skipNoTrace is a run that never got as far as a decision.json.
const skipNoTrace = "without a decision trace"

// comparedStages are the stages that get a comparison table and no proposal: a run has no
// per-run signal of whether they paid off (§7.1).
var comparedStages = []domain.Stage{domain.StageResearch, domain.StageInvestigation}

// CalibKey is what traces are grouped by: probabilities from different decision models, or
// from different policies, are not comparable (§7.1, §7.2). An empty PolicyHash is the group
// of traces written before the hash existed.
type CalibKey struct {
	Provider   string
	Model      string
	PolicyHash string
}

func (k CalibKey) String() string {
	return orNoValue(k.Provider) + "/" + orNoValue(k.Model)
}

func orNoValue(s string) string {
	if s == "" {
		return noValue
	}
	return s
}

// CalibStats is a set of runs and how they ended.
type CalibStats struct {
	Runs      int
	Completed int
	FixSum    int
	CostUSD   float64
}

func (s *CalibStats) add(out domain.Outcome) {
	s.Runs++
	if out.Status == domain.RunCompleted {
		s.Completed++
	}
	s.FixSum += out.FixIterations
	s.CostUSD += cost(out.Usage.CostUSD)
}

func (s CalibStats) completedCell() string {
	if s.Runs == 0 {
		return noValue
	}
	return fmt.Sprintf("%d (%.1f%%)", s.Completed, 100*float64(s.Completed)/float64(s.Runs))
}

func (s CalibStats) fixCell() string {
	if s.Runs == 0 {
		return noValue
	}
	return fmt.Sprintf("%.2f", float64(s.FixSum)/float64(s.Runs))
}

func (s CalibStats) costCell() string {
	if s.Runs == 0 {
		return money(0, false)
	}
	return money(s.CostUSD/float64(s.Runs), true)
}

// Bucket is the runs of one 0.1-wide probability bucket and how many of them the stage paid
// off in.
type Bucket struct {
	Runs    int
	PaidOff int
}

// Proposal is a new threshold for a stage and the runs it rests on: the buckets of the runs
// with P in [From, To) for a raise, and of the explored runs with P in [To, From) for a
// lowering (To < From).
type Proposal struct {
	From, To float64
	Runs     int
	Evidence [bucketCount]Bucket
}

// StageCalibration is the testing or the review stage of one group.
type StageCalibration struct {
	Stage domain.Stage
	// Ran is the runs where the stage was enabled with a probability and a step of its
	// action executed.
	Ran     int
	Buckets [bucketCount]Bucket
	// Thresholds are the distinct thresholds the runs recorded, ascending. More than one
	// happens only in the group of traces without a policy hash, and then there is no
	// proposal.
	Thresholds []float64
	Proposal   *Proposal
}

// Mixed reports whether the runs were decided under different thresholds.
func (s StageCalibration) Mixed() bool { return len(s.Thresholds) > 1 }

// ComparisonRow is one stage at one complexity, with and without the stage in the workflow.
type ComparisonRow struct {
	Stage      domain.Stage
	Complexity domain.Complexity
	With       CalibStats
	Without    CalibStats
}

// ModelRow is the runs that used one route for one action at one complexity.
type ModelRow struct {
	Complexity domain.Complexity
	// Route is "<action> <provider>/<model>", or noValue for traces without models.
	Route string
	Stats CalibStats
}

// CalibGroup is every run decided by one model under one policy.
type CalibGroup struct {
	Key        CalibKey
	Runs       int
	Testing    StageCalibration
	Review     StageCalibration
	Comparison []ComparisonRow
	Models     []ModelRow
}

// Proposals are the stages of the group that have a new threshold, testing first.
func (g CalibGroup) Proposals() []StageCalibration {
	var out []StageCalibration
	for _, s := range []StageCalibration{g.Testing, g.Review} {
		if s.Proposal != nil {
			out = append(out, s)
		}
	}
	return out
}

// Calibration is the whole report.
type Calibration struct {
	MinRuns    int
	Traces     int
	Used       int
	Skipped    map[string]int
	Unreadable []UnreadableRun
	Groups     []CalibGroup
	// Shape is the size section (specs/decision-shape/SPEC.md §7.2) for SizeTarget; the caller
	// fills it with Shape, and the report leaves it out when it is empty.
	Shape         []ShapeGroup
	SizeTarget    SizeTarget
	SimpleMaxHard float64
	// Clarity is the P(clear) distribution per decision model and questions hash, NeedsInput the
	// runs that stopped "task needs input" (specs/clarify CL08). Both only show: there is no
	// proposal for policy.clarify.threshold (A1).
	Clarity    []ClarityGroup
	NeedsInput NeedsInputStats
}

// ClarityGroup is the runs of one decision model and questions hash that were asked the clarity
// question: P(clear) of the first check in buckets of 0.1, and how the checks ended. Traces from
// differently worded questions are on different scales, as for the size (ShapeKey).
type ClarityGroup struct {
	Key  ShapeKey
	Runs int
	// First counts the first check (the pre-check, the request alone) per bucket.
	First [bucketCount]int
	// Rechecked is the runs that had a re-check after the clarify step.
	Rechecked int
	// Status counts how the last check ended; Thresholds are the distinct policy.clarify.threshold
	// values the runs recorded, ascending.
	Status     map[domain.ClarityStatus]int
	Thresholds []float64
}

// NeedsInputStats is the share of runs that stopped "task needs input". Such a run has no
// decision.json, so it is counted over every run read, and its last P(clear) comes from run.json.
type NeedsInputStats struct {
	Runs, Total int
	// Last counts the last check of those runs per bucket.
	Last [bucketCount]int
}

func (s *NeedsInputStats) add(run domain.Run) {
	s.Runs++
	if c := run.Task.Clarification; c != nil && len(c.Checks) > 0 {
		if p := c.Checks[len(c.Checks)-1]; !math.IsNaN(p) {
			s.Last[bucketOf(p)]++
		}
	}
}

type clarityAcc struct {
	runs       int
	first      [bucketCount]int
	rechecked  int
	status     map[domain.ClarityStatus]int
	thresholds map[float64]bool
}

func (a *clarityAcc) add(c *domain.ClarityTrace) {
	if len(c.Checks) == 0 || math.IsNaN(c.Checks[0]) {
		return
	}
	a.runs++
	a.first[bucketOf(c.Checks[0])]++
	if len(c.Checks) > 1 {
		a.rechecked++
	}
	if a.status == nil {
		a.status, a.thresholds = map[domain.ClarityStatus]int{}, map[float64]bool{}
	}
	a.status[c.Status]++
	a.thresholds[c.Threshold] = true
}

// sample is one run where a stage ran: its probability, whether the stage paid off, and
// whether policy.explore turned it on below its threshold.
type sample struct {
	p        float64
	paidOff  bool
	explored bool
}

type stageAcc struct {
	samples    []sample
	thresholds map[float64]bool
}

type compKey struct {
	stage      domain.Stage
	complexity domain.Complexity
	with       bool
}

type modelKey struct {
	complexity domain.Complexity
	route      string
}

type groupAcc struct {
	runs            int
	testing, review stageAcc
	comparison      map[compKey]*CalibStats
	models          map[modelKey]*CalibStats
}

// Calibrate groups the runs' decisions by decision provider, model and policy hash and
// computes, per group, the testing and review buckets with the raise rule of §7.3, the
// comparison of the stages that get no proposal, and the model table. A trace is skipped for
// the reasons `decisions report` uses: manual, chosen by the user, and mock. minRuns is the
// smallest number of runs a bucket or a window needs to say anything.
func Calibrate(entries []RunEntry, unreadable []UnreadableRun, minRuns int) Calibration {
	minRuns = max(minRuns, 1)
	c := Calibration{MinRuns: minRuns, Traces: len(entries), Skipped: map[string]int{}, Unreadable: unreadable}
	groups := map[CalibKey]*groupAcc{}
	clarity := map[ShapeKey]*clarityAcc{}
	c.NeedsInput.Total = len(entries)

	for _, e := range entries {
		if e.Run.FailureReason == domain.FailureTaskNeedsInput {
			c.NeedsInput.add(e.Run)
		}
		t := e.Trace
		if t == nil {
			c.Skipped[skipNoTrace]++
			continue
		}
		if reason := calibrationSkip(t); reason != "" {
			c.Skipped[reason]++
			continue
		}
		c.Used++
		key := CalibKey{Provider: t.Provider, Model: t.Model, PolicyHash: t.PolicyHash}
		g := groups[key]
		if g == nil {
			g = &groupAcc{comparison: map[compKey]*CalibStats{}, models: map[modelKey]*CalibStats{}}
			groups[key] = g
		}
		g.add(e, runOutcome(e))
		if t.Clarity != nil {
			ck := ShapeKey{Provider: t.Provider, Model: t.Model, QuestionsHash: t.QuestionsHash}
			if clarity[ck] == nil {
				clarity[ck] = &clarityAcc{}
			}
			clarity[ck].add(t.Clarity)
		}
	}

	for key, g := range groups {
		c.Groups = append(c.Groups, g.finish(key, minRuns))
	}
	for key, a := range clarity {
		if a.runs == 0 {
			continue
		}
		g := ClarityGroup{Key: key, Runs: a.runs, First: a.first, Rechecked: a.rechecked, Status: a.status}
		for th := range a.thresholds {
			g.Thresholds = append(g.Thresholds, th)
		}
		slices.Sort(g.Thresholds)
		c.Clarity = append(c.Clarity, g)
	}
	slices.SortFunc(c.Clarity, func(a, b ClarityGroup) int {
		return cmp.Or(
			strings.Compare(a.Key.Provider, b.Key.Provider),
			strings.Compare(a.Key.Model, b.Key.Model),
			strings.Compare(a.Key.QuestionsHash, b.Key.QuestionsHash))
	})
	slices.SortFunc(c.Groups, func(a, b CalibGroup) int {
		return cmp.Or(
			strings.Compare(a.Key.Provider, b.Key.Provider),
			strings.Compare(a.Key.Model, b.Key.Model),
			strings.Compare(a.Key.PolicyHash, b.Key.PolicyHash))
	})
	return c
}

// calibrationSkip is why a trace is left out of calibration, "" when it is used: the reasons
// `decisions report` uses - manual, chosen by the user, and mock.
func calibrationSkip(t *domain.DecisionTrace) string {
	switch {
	case t.Source == domain.DecisionSourceManual:
		return SkipManual
	case t.Source == domain.DecisionSourceUser:
		return SkipUser
	case strings.HasPrefix(t.Model, mockModelPrefix):
		return SkipMock
	}
	return ""
}

// runOutcome is the outcome run.json recorded or, for a run written before that, the one
// derived from its record.
func runOutcome(e RunEntry) domain.Outcome {
	if e.Run.Outcome != nil {
		return *e.Run.Outcome
	}
	return domain.ComputeOutcome(e.Run, e.Trace)
}

func (g *groupAcc) add(e RunEntry, out domain.Outcome) {
	t, run := e.Trace, e.Run
	g.runs++
	g.testing.add(t, run, domain.StageTesting, domain.ActionTest, testingPaidOff)
	g.review.add(t, run, domain.StageReview, domain.ActionReview, reviewPaidOff)

	complexity := t.Complexity.Selected
	if !slices.Contains(domain.ComplexityOrder(), complexity) {
		return
	}
	for _, st := range comparedStages {
		so, ok := traceStage(t, st)
		if !ok {
			continue
		}
		k := compKey{stage: st, complexity: complexity, with: so.Enabled}
		stats(g.comparison, k).add(out)
	}

	if len(t.Models) == 0 {
		stats(g.models, modelKey{complexity, noValue}).add(out)
		return
	}
	for action, r := range t.Models {
		stats(g.models, modelKey{complexity, fmt.Sprintf("%s %s/%s", action, r.Provider, r.Model)}).add(out)
	}
}

func stats[K comparable](m map[K]*CalibStats, k K) *CalibStats {
	s := m[k]
	if s == nil {
		s = &CalibStats{}
		m[k] = s
	}
	return s
}

func traceStage(t *domain.DecisionTrace, st domain.Stage) (domain.StageOutcome, bool) {
	for _, so := range t.Stages {
		if so.Stage == st {
			return so, true
		}
	}
	return domain.StageOutcome{}, false
}

// add records the run if the stage ran in it: it was enabled with a provider probability
// and a step of its action executed. A stage that was off, or never reached because an
// earlier step failed, says nothing about its threshold.
func (a *stageAcc) add(t *domain.DecisionTrace, run domain.Run, st domain.Stage, action domain.ActionType, paidOff func(domain.Run) bool) {
	so, ok := traceStage(t, st)
	if !ok || !so.Enabled || so.Probability == nil || so.Threshold == nil {
		return
	}
	p := *so.Probability
	if math.IsNaN(p) || p < 0 || p > 1 || math.IsNaN(*so.Threshold) {
		return
	}
	if !slices.ContainsFunc(run.Executions, func(ex domain.StepExecution) bool { return ex.Action == action }) {
		return
	}
	if a.thresholds == nil {
		a.thresholds = map[float64]bool{}
	}
	a.thresholds[*so.Threshold] = true
	a.samples = append(a.samples, sample{p: p, paidOff: paidOff(run), explored: so.Explored})
}

func (a *stageAcc) finish(st domain.Stage, minRuns int) StageCalibration {
	sc := StageCalibration{Stage: st, Ran: len(a.samples)}
	for _, s := range a.samples {
		b := &sc.Buckets[bucketOf(s.p)]
		b.Runs++
		if s.paidOff {
			b.PaidOff++
		}
	}
	for th := range a.thresholds {
		sc.Thresholds = append(sc.Thresholds, th)
	}
	slices.Sort(sc.Thresholds)

	// Runs decided under different thresholds cannot say what raising one would have done.
	if len(sc.Thresholds) == 1 {
		sc.Proposal = raiseProposal(a.samples, sc.Thresholds[0], minRuns)
		if sc.Proposal == nil {
			sc.Proposal = lowerProposal(a.samples, sc.Thresholds[0], minRuns)
		}
	}
	return sc
}

func (g *groupAcc) finish(key CalibKey, minRuns int) CalibGroup {
	out := CalibGroup{
		Key:     key,
		Runs:    g.runs,
		Testing: g.testing.finish(domain.StageTesting, minRuns),
		Review:  g.review.finish(domain.StageReview, minRuns),
	}
	for _, st := range comparedStages {
		for _, c := range domain.ComplexityOrder() {
			with, without := g.comparison[compKey{st, c, true}], g.comparison[compKey{st, c, false}]
			if with == nil && without == nil {
				continue
			}
			row := ComparisonRow{Stage: st, Complexity: c}
			if with != nil {
				row.With = *with
			}
			if without != nil {
				row.Without = *without
			}
			out.Comparison = append(out.Comparison, row)
		}
	}
	for k, s := range g.models {
		out.Models = append(out.Models, ModelRow{Complexity: k.complexity, Route: k.route, Stats: *s})
	}
	order := domain.ComplexityOrder()
	slices.SortFunc(out.Models, func(a, b ModelRow) int {
		return cmp.Or(slices.Index(order, a.Complexity)-slices.Index(order, b.Complexity), strings.Compare(a.Route, b.Route))
	})
	return out
}

// bucketOf is the bucket a probability falls in: [0, 0.1) is 0 and 1.0 joins the last.
func bucketOf(p float64) int {
	return min(max(int(math.Floor(p*bucketCount+probEps)), 0), bucketCount-1)
}

func bucketLabel(i int) string {
	return fmt.Sprintf("%.1f-%.1f", float64(i)/bucketCount, float64(i+1)/bucketCount)
}

// raiseProposal is the lowest bucket edge t above the current threshold such that the stage
// paid off in none of at least minRuns runs with P in [current, t) (§7.3); nil when there is
// none. A paid-off run in the window can only stay in every wider window, so the first
// window with enough runs decides. The last window includes P = 1.
func raiseProposal(samples []sample, current float64, minRuns int) *Proposal {
	for k := 1; k <= bucketCount; k++ {
		edge := float64(k) / bucketCount
		if edge <= current+probEps {
			continue
		}
		p := Proposal{From: current, To: edge}
		paidOff := 0
		for _, s := range samples {
			if s.p < current-probEps || (k < bucketCount && s.p >= edge-probEps) {
				continue
			}
			p.Runs++
			b := &p.Evidence[bucketOf(s.p)]
			b.Runs++
			if s.paidOff {
				b.PaidOff++
				paidOff++
			}
		}
		if p.Runs < minRuns {
			continue
		}
		if paidOff > 0 {
			return nil
		}
		return &p
	}
	return nil
}

// lowerProposal is the lowering rule of §7.3: the lowest bucket edge t below the current
// threshold such that the explored runs (stages[].explored, turned on below the threshold by
// policy.explore) with P in [t, current) paid off at least as often as the runs at or above
// the threshold. It needs at least minRuns runs on both sides and a baseline that paid off at
// least once; it is nil otherwise. A stage forced on below its threshold is not explored and
// says nothing here. Edges below the lowest explored run are not considered, since they would
// add no run to the window and propose a threshold nobody tried. The proposal's evidence is
// the window [To, From).
func lowerProposal(samples []sample, current float64, minRuns int) *Proposal {
	baseRuns, basePaid := 0, 0
	lowest := bucketCount
	for _, s := range samples {
		switch {
		case s.p >= current-probEps:
			baseRuns++
			if s.paidOff {
				basePaid++
			}
		case s.explored:
			lowest = min(lowest, bucketOf(s.p))
		}
	}
	if baseRuns < minRuns || basePaid == 0 {
		return nil
	}
	for k := lowest; k < bucketCount; k++ {
		edge := float64(k) / bucketCount
		if edge >= current-probEps {
			break
		}
		p := Proposal{From: current, To: edge}
		paidOff := 0
		for _, s := range samples {
			if !s.explored || s.p < edge-probEps || s.p >= current-probEps {
				continue
			}
			p.Runs++
			b := &p.Evidence[bucketOf(s.p)]
			b.Runs++
			if s.paidOff {
				b.PaidOff++
				paidOff++
			}
		}
		// paidOff/p.Runs >= basePaid/baseRuns, without dividing.
		if p.Runs >= minRuns && paidOff*baseRuns >= basePaid*p.Runs {
			return &p
		}
	}
	return nil
}

// testingPaidOff reports whether testing paid off in the run (§7.1): a test step failed
// after an implement. Either a later test passed, or the run ended with the failure exposed;
// in both the test found what the implement broke. The pre-run test is not an execution and
// is not counted, and a run whose test step failed without a test result (the sandbox could
// not start) found nothing.
func testingPaidOff(run domain.Run) bool {
	implemented := false
	for _, ex := range run.Executions {
		switch {
		case ex.Action == domain.ActionImplement:
			implemented = true
		case ex.Action == domain.ActionTest && implemented && ex.TestResult != nil && !ex.TestResult.Success:
			return true
		}
	}
	return false
}

// reviewPaidOff reports whether review paid off in the run (§7.1): a review's verdict sent
// the run back and an implement followed. The verdict is the authoritative ReviewResult.Decide
// (a critical or high issue, or an unmet criterion), or, when the run recorded
// options.review_fix_medium, a medium issue. A review that failed with nothing after it (no
// implement in the workflow, or the fix limit reached) did not change the code and does not
// count.
func reviewPaidOff(run domain.Run) bool {
	for i, ex := range run.Executions {
		if ex.Action != domain.ActionReview || ex.ReviewResult == nil {
			continue
		}
		r := *ex.ReviewResult
		sentBack := r.Decide() == domain.ReviewFail || (run.Options.ReviewFixMedium && len(r.MediumIssues()) > 0)
		if sentBack && slices.ContainsFunc(run.Executions[i+1:], func(later domain.StepExecution) bool {
			return later.Action == domain.ActionImplement
		}) {
			return true
		}
	}
	return false
}

// RenderCalibration writes the report as plain text, ending with the YAML diff. runsDir only
// names where the runs were read. Every order is fixed or sorted and no time is printed, so
// the same runs give the same bytes.
func RenderCalibration(w io.Writer, runsDir string, c Calibration) {
	fmt.Fprintf(w, "Calibration report: %s\n", runsDir)
	if c.Traces == 0 && len(c.Unreadable) == 0 {
		fmt.Fprintln(w, "no runs yet")
		return
	}
	fmt.Fprintf(w, "runs: %d, used: %d, min runs: %d\n", c.Traces, c.Used, c.MinRuns)
	var skipped []string
	for _, reason := range append(slices.Clone(skipOrder), skipNoTrace) {
		if n := c.Skipped[reason]; n > 0 {
			skipped = append(skipped, fmt.Sprintf("%d %s", n, reason))
		}
	}
	if len(c.Unreadable) > 0 {
		skipped = append(skipped, fmt.Sprintf("%d unreadable", len(c.Unreadable)))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "skipped: %s\n", strings.Join(skipped, ", "))
	}
	for _, u := range c.Unreadable {
		fmt.Fprintf(w, "  %s: %s\n", u.Path, firstLine(u.Err))
	}
	if len(c.Groups) == 0 {
		fmt.Fprintln(w, "\nnothing to calibrate: no run was decided by Jev or an LLM")
		return
	}

	for _, g := range c.Groups {
		fmt.Fprintf(w, "\n== %s  policy %s  (%d runs)\n", g.Key, shortHash(g.Key.PolicyHash), g.Runs)
		renderStage(w, g.Testing, c.MinRuns)
		renderStage(w, g.Review, c.MinRuns)
		renderComparison(w, g.Comparison)
		renderModels(w, g.Models)
	}
	renderShape(w, c.Shape, c.SizeTarget, c.SimpleMaxHard)
	renderClarity(w, c.Clarity, c.NeedsInput)

	fmt.Fprintln(w, "\npolicy.stages.thresholds (a proposal; nothing is written)")
	for _, g := range c.Groups {
		fmt.Fprintf(w, "# %s  policy %s\n", g.Key, shortHash(g.Key.PolicyHash))
		props := g.Proposals()
		if len(props) == 0 {
			fmt.Fprintln(w, "  no change proposed")
			continue
		}
		for _, s := range props {
			fmt.Fprintf(w, "-   %s: %.2f\n", s.Stage, s.Proposal.From)
			fmt.Fprintf(w, "+   %s: %.2f\n", s.Stage, s.Proposal.To)
			for i, b := range s.Proposal.Evidence {
				if b.Runs > 0 {
					fmt.Fprintf(w, "      # %s: %d runs, %d paid off\n", bucketLabel(i), b.Runs, b.PaidOff)
				}
			}
		}
	}
	renderCutsDiff(w, c.Shape)
}

func renderStage(w io.Writer, s StageCalibration, minRuns int) {
	threshold := "threshold " + noValue
	switch {
	case s.Mixed():
		threshold = "mixed thresholds"
	case len(s.Thresholds) == 1:
		threshold = fmt.Sprintf("threshold %.2f", s.Thresholds[0])
	}
	fmt.Fprintf(w, "\n%s: %s, ran in %d runs\n", s.Stage, threshold, s.Ran)
	if s.Ran == 0 {
		return
	}
	fmt.Fprintf(w, "  %-9s %5s  %s\n", "P", "runs", "paid off")
	for i, b := range s.Buckets {
		if b.Runs == 0 {
			continue
		}
		rate := "not enough data"
		if b.Runs >= minRuns {
			rate = fmt.Sprintf("%d (%.1f%%)", b.PaidOff, 100*float64(b.PaidOff)/float64(b.Runs))
		}
		fmt.Fprintf(w, "  %-9s %5d  %s\n", bucketLabel(i), b.Runs, rate)
	}
}

func renderComparison(w io.Writer, rows []ComparisonRow) {
	fmt.Fprintln(w, "\nresearch, investigation: with and without the stage (no proposal)")
	if len(rows) == 0 {
		fmt.Fprintln(w, "  no run")
		return
	}
	const row = "  %-10s %-9s %-8s %5s %12s %8s\n"
	fmt.Fprintf(w, row, "complexity", "stage", "variant", "runs", "completed", "fix iter")
	for _, r := range rows {
		for _, v := range []struct {
			name  string
			stats CalibStats
		}{{"with", r.With}, {"without", r.Without}} {
			fmt.Fprintf(w, row, r.Complexity, r.Stage, v.name, strconv.Itoa(v.stats.Runs), v.stats.completedCell(), v.stats.fixCell())
		}
	}
}

// renderClarity writes the clarity section: the distribution of the first P(clear) per group and
// the share of runs that stopped "task needs input". It shows and proposes nothing (A1); it writes
// nothing at all when no run was asked the clarity question and none stopped for input.
func renderClarity(w io.Writer, groups []ClarityGroup, stopped NeedsInputStats) {
	if len(groups) == 0 && stopped.Runs == 0 {
		return
	}
	fmt.Fprintln(w, "\nclarity: P(clear) of the first check (specs/clarify; no proposal)")
	for _, g := range groups {
		threshold := "threshold " + noValue
		switch len(g.Thresholds) {
		case 0:
		case 1:
			threshold = fmt.Sprintf("threshold %.2f", g.Thresholds[0])
		default:
			threshold = "mixed thresholds"
		}
		fmt.Fprintf(w, "\n== %s/%s  questions %s  %s  (%d runs)\n", orNoValue(g.Key.Provider), orNoValue(g.Key.Model), shortHash(g.Key.QuestionsHash), threshold, g.Runs)
		for i, n := range g.First {
			if n > 0 {
				fmt.Fprintf(w, "  %-9s %5d\n", bucketLabel(i), n)
			}
		}
		var ended []string
		for _, s := range []domain.ClarityStatus{domain.ClarityClear, domain.ClarityUnclear, domain.ClarityDisabled} {
			if n := g.Status[s]; n > 0 {
				ended = append(ended, fmt.Sprintf("%s %d", s, n))
			}
		}
		fmt.Fprintf(w, "re-checked in %d runs; last check: %s\n", g.Rechecked, strings.Join(ended, ", "))
	}
	share := 0.0
	if stopped.Total > 0 {
		share = 100 * float64(stopped.Runs) / float64(stopped.Total)
	}
	fmt.Fprintf(w, "\nstopped %q: %d of %d runs (%.1f%%)\n", domain.FailureTaskNeedsInput, stopped.Runs, stopped.Total, share)
	if slices.ContainsFunc(stopped.Last[:], func(n int) bool { return n > 0 }) {
		fmt.Fprintln(w, "last P(clear) of those runs:")
	}
	for i, n := range stopped.Last {
		if n > 0 {
			fmt.Fprintf(w, "  %-9s %5d\n", bucketLabel(i), n)
		}
	}
}

func renderModels(w io.Writer, rows []ModelRow) {
	fmt.Fprintln(w, "\nmodels per complexity (no proposal)")
	if len(rows) == 0 {
		fmt.Fprintln(w, "  no run")
		return
	}
	width := len("action provider/model")
	for _, r := range rows {
		width = max(width, len([]rune(r.Route)))
	}
	const row = "  %-10s %-*s %5s %12s %8s %10s\n"
	fmt.Fprintf(w, row, "complexity", width, "action provider/model", "runs", "completed", "fix iter", "mean cost")
	for _, r := range rows {
		fmt.Fprintf(w, row, r.Complexity, width, r.Route, strconv.Itoa(r.Stats.Runs), r.Stats.completedCell(), r.Stats.fixCell(), r.Stats.costCell())
	}
}
