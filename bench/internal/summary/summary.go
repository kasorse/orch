// Package summary turns the result lines of a benchmark (specs/benchmark/SPEC.md §9) into the
// numbers B4 decides on (§10). Compute is a pure function of the records and the options and
// does no I/O; Render writes the Markdown. Every iteration goes through sorted keys, so the same
// records and seed give the same report byte for byte.
package summary

import (
	"cmp"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"

	"orchbench/internal/results"
)

// What --by groups the tables by.
const (
	ByLanguage    = "language"
	ByCategory    = "category"
	ByContributor = "contributor"
)

// ValidBy reports whether by is a grouping Compute knows.
func ValidBy(by string) bool {
	return slices.Contains([]string{ByLanguage, ByCategory, ByContributor}, by)
}

// The defaults of Options.
const (
	DefaultBaseline  = "A"
	DefaultMinTasks  = 10
	DefaultResamples = 10000
)

// The configs the decision sections look at (SPEC.md §8): B decides the stages itself, B′ is B
// with the mock decision, C runs every stage.
const (
	ConfigB     = "B"
	ConfigBmock = "Bmock"
	ConfigC     = "C"
)

// bootstrapStream is the second word of the bootstrap's PCG ("orchbenc"), fixed so that the
// seed alone decides the interval.
const bootstrapStream = 0x6f72636862656e63

// stageOrder is the order of the workflow's stages (CLAUDE.md), complexityOrder the order of
// the policy's levels. A name in neither comes after them, in lexical order.
var (
	stageOrder      = []string{"research", "analysis", "planning", "development", "testing", "review"}
	complexityOrder = []string{"simple", "medium", "complex"}
	failOrder       = []string{results.FailOracle, results.FailRegression, results.FailAnswer, results.FailTreeChanged}
)

// none stands for an empty language, category, contributor or complexity.
const none = "(none)"

// Options are what Compute is told besides the records.
type Options struct {
	// Baseline is the config every other one is paired against; default A.
	Baseline string
	// Seed is the bootstrap's seed.
	Seed uint64
	// By is the grouping of the second breakdown: language (default), category or contributor.
	By string
	// MinTasks is the number of tasks below which a group is marked insufficient data; default 10.
	MinTasks int
	// Resamples is the number of bootstrap resamples; default 10000.
	Resamples int
}

func (o Options) withDefaults() Options {
	if o.Baseline == "" {
		o.Baseline = DefaultBaseline
	}
	if o.By == "" {
		o.By = ByLanguage
	}
	if o.MinTasks <= 0 {
		o.MinTasks = DefaultMinTasks
	}
	if o.Resamples <= 0 {
		o.Resamples = DefaultResamples
	}
	return o
}

// Report is everything the Markdown shows.
type Report struct {
	Options  Options
	Coverage Coverage
	// Overall has one row per config, sorted by name.
	Overall  []ConfigStats
	Failures FailureTable
	// Categories is the breakdown by category, Groups the one by Options.By.
	Categories  []Group
	Groups      []Group
	Paired      Paired
	Overhead    Overhead
	CostCompare CostCompare
	// Stages has B and B′, whichever has results.
	Stages      []StageChoice
	Translation Translation
}

// Coverage says what the report is based on and what the expected grid of tasks × configs ×
// repetitions lacks.
type Coverage struct {
	// Cells is the number of cells with a result line, of which Pass, Fail and Infra.
	Cells, Pass, Fail, Infra int
	Tasks, Configs, Reps     int
	// Expected is Tasks × Configs × Reps, Missing what of it has no line (a run in progress, or
	// a config added later).
	Expected, Missing int
	// Replaced is the number of result lines a later line for the same cell (`run --redo`)
	// replaced, Partial 1 when a half-written last line was ignored. Compute does not see the
	// lines, so the caller that read them sets both.
	Replaced, Partial int
}

// ConfigStats are the numbers of one config over some records. An infra_error cell is counted in
// Infra and is in none of the rates and costs.
type ConfigStats struct {
	Config string
	// Cells counts the cells that are results (pass or fail), Tasks their distinct tasks.
	Cells, Tasks, Infra, Passes int
	// PassRate is the mean over repetitions of the per-repetition pass rate, RateMin and RateMax
	// the smallest and largest of those rates. All are fractions.
	PassRate, RateMin, RateMax float64
	// Costs are per cell. CostSolved is the total cost over the passes.
	CostMean, CostMedian, CostSolved float64
	// HasSolved is false when nothing passed.
	HasSolved bool
	// WallMS, LLMCalls, FixIterations and TestsReset are means per cell.
	WallMS, LLMCalls, FixIterations, TestsReset float64
	// FailReasons counts the failed cells by fail_reason ("unspecified" when a line has none).
	FailReasons map[string]int
}

// FailureTable is the failures of every config by fail_reason: the four of SPEC.md §7.5 and §5.3
// first, any other after them.
type FailureTable struct {
	Reasons []string
	Rows    []FailureRow
}

// FailureRow is one config of a FailureTable; Counts is aligned with the table's Reasons.
type FailureRow struct {
	Config     string
	Cells      int
	Fails      int
	Counts     []int
	TestsReset float64
}

// Group is the per-config numbers of the records that share a language, category or contributor.
type Group struct {
	Name  string
	Tasks int
	// Insufficient is set below Options.MinTasks: the numbers are shown, a conclusion from them
	// is not.
	Insufficient bool
	Configs      []ConfigStats
}

// Paired is every config against the baseline.
type Paired struct {
	Baseline string
	// BaselineSeen is false when no cell of the baseline config exists, which is the case early
	// in a run; there is nothing to pair against then.
	BaselineSeen bool
	Rows         []PairRow
}

// PairRow is one config against the baseline over the tasks both have a result for. Wins, Ties
// and Losses are by majority of the repetitions; the differences are in percentage points.
type PairRow struct {
	Config string
	// Tasks is the number of tasks both sides have a result for.
	Tasks int
	// Wins, Ties and Losses count those tasks.
	Wins, Ties, Losses int
	// DiffPP is the config's pass rate minus the baseline's, LowPP and HighPP the bounds of its
	// 95% interval.
	DiffPP, LowPP, HighPP float64
	// NoBaseline counts the tasks the config has a result for and the baseline lacks, NoConfig
	// the reverse.
	NoBaseline, NoConfig int
}

// Overhead is what B's decision rounds cost (SPEC.md §11.2), over B's result cells.
type Overhead struct {
	Present bool
	Cells   int
	// CostShare and TimeShare are fractions; Has* is false when the total is zero.
	CostShare, TimeShare float64
	HasCost, HasTime     bool
	// Calls is the mean number of decision calls per run.
	Calls float64
}

// CostCompare is B against C on cost.
type CostCompare struct {
	// Missing names the configs of B and C without a result.
	Missing []string
	// BMean and CMean are the mean cost per cell.
	BMean, CMean float64
	// Ratio is BMean/CMean when HasRatio.
	Ratio    float64
	HasRatio bool
	// PairedTasks is the number of tasks with a result of both, PairedDiff the mean over them
	// of B's cost minus C's, each cost the mean over the task's repetitions.
	PairedTasks int
	PairedDiff  float64
}

// StageChoice is one config's chosen stages against expected_stages.
type StageChoice struct {
	Config string
	// Cells counts the result cells that have expected_stages, Exact those whose stage set
	// equals them.
	Cells, Exact int
	Stages       []StageScore
	// Complexity is the distribution over all of the config's result cells.
	Complexity []ComplexityRow
}

// StageScore is one stage's confusion over the cells counted in StageChoice.Cells.
type StageScore struct {
	Stage      string
	TP, FP, FN int
}

// Precision is TP/(TP+FP); ok is false when nothing was chosen.
func (s StageScore) Precision() (v float64, ok bool) {
	if s.TP+s.FP == 0 {
		return 0, false
	}
	return float64(s.TP) / float64(s.TP+s.FP), true
}

// Recall is TP/(TP+FN); ok is false when the stage was never expected.
func (s StageScore) Recall() (v float64, ok bool) {
	if s.TP+s.FN == 0 {
		return 0, false
	}
	return float64(s.TP) / float64(s.TP+s.FN), true
}

// ComplexityRow is how many of a config's cells the decision gave one complexity, and how many
// of them passed.
type ComplexityRow struct {
	Complexity    string
	Cells, Passes int
}

// Translation is the en/ru pairs (SPEC.md §10).
type Translation struct {
	Pairs []PairResult
	// Incomplete counts the pairs whose other side has no cell.
	Incomplete int
	// SameStages counts the pairs where B chose the same stages on both sides, out of
	// StagesOf, the pairs where B has a result on both.
	SameStages, StagesOf int
	// Both has, per config, the pairs where both sides passed by majority, out of those where
	// the config has a result on both sides.
	Both []PairTotal
}

// PairResult is one pair, the English task first.
type PairResult struct {
	EN, RU string
	// StagesEN and StagesRU are the stage set B chose most often over the repetitions.
	StagesEN, StagesRU []string
	// HasStages is false when B has no result on a side; SameStages then means nothing.
	HasStages, SameStages bool
	// Passes has one entry per config, in the order of Translation.Both.
	Passes []PairPass
}

// PairPass is whether both sides of a pair passed by majority for one config; Known is false
// when the config has no result on a side.
type PairPass struct {
	Config      string
	Known, Both bool
}

// PairTotal is the both-passed count of one config.
type PairTotal struct {
	Config   string
	Both, Of int
}

// Compute builds the report. It keeps the latest record of a cell when records repeat one.
func Compute(recs []results.Record, opt Options) Report {
	opt = opt.withDefaults()
	recs, _ = results.Latest(recs)

	byConfig := splitBy(recs, func(r results.Record) string { return r.Config })
	configs := slices.Sorted(maps.Keys(byConfig))

	rep := Report{Options: opt, Coverage: coverage(recs)}
	for _, c := range configs {
		rep.Overall = append(rep.Overall, configStats(c, byConfig[c]))
	}
	rep.Failures = failures(rep.Overall)
	rep.Categories = groupStats(recs, func(r results.Record) string { return orNone(r.Category) }, opt.MinTasks)
	rep.Groups = groupStats(recs, groupKey(opt.By), opt.MinTasks)

	tasks, perTask := perTaskCells(recs)
	rep.Paired = Paired{Baseline: opt.Baseline, BaselineSeen: len(byConfig[opt.Baseline]) > 0}
	if rep.Paired.BaselineSeen {
		for _, c := range configs {
			if c != opt.Baseline {
				rep.Paired.Rows = append(rep.Paired.Rows, pairRow(tasks, perTask, opt, c))
			}
		}
	}

	rep.Overhead = overhead(byConfig[ConfigB])
	rep.CostCompare = costCompare(rep.Overall, tasks, perTask)
	for _, c := range []string{ConfigB, ConfigBmock} {
		if len(byConfig[c]) > 0 {
			rep.Stages = append(rep.Stages, stageChoice(c, byConfig[c]))
		}
	}
	rep.Translation = translation(recs, configs, perTask)
	return rep
}

func groupKey(by string) func(results.Record) string {
	switch by {
	case ByCategory:
		return func(r results.Record) string { return orNone(r.Category) }
	case ByContributor:
		return func(r results.Record) string { return orNone(r.Contributor) }
	}
	return func(r results.Record) string { return orNone(r.Language) }
}

func orNone(s string) string {
	if s == "" {
		return none
	}
	return s
}

// splitBy groups records by key, keeping their order within a group.
func splitBy(recs []results.Record, key func(results.Record) string) map[string][]results.Record {
	out := make(map[string][]results.Record)
	for _, r := range recs {
		k := key(r)
		out[k] = append(out[k], r)
	}
	return out
}

func distinctTasks(recs []results.Record) int {
	seen := make(map[string]struct{})
	for _, r := range recs {
		seen[r.Task] = struct{}{}
	}
	return len(seen)
}

func coverage(recs []results.Record) Coverage {
	var c Coverage
	tasks, configs := make(map[string]struct{}), make(map[string]struct{})
	for _, r := range recs {
		c.Cells++
		switch r.Result {
		case results.Pass:
			c.Pass++
		case results.Fail:
			c.Fail++
		default:
			c.Infra++
		}
		tasks[r.Task] = struct{}{}
		configs[r.Config] = struct{}{}
		c.Reps = max(c.Reps, r.Rep)
	}
	c.Tasks, c.Configs = len(tasks), len(configs)
	c.Expected = c.Tasks * c.Configs * c.Reps
	c.Missing = c.Expected - c.Cells
	return c
}

// configStats is the numbers of one config; recs are its records.
func configStats(config string, recs []results.Record) ConfigStats {
	s := ConfigStats{Config: config, FailReasons: make(map[string]int)}

	type repCount struct{ cells, passes int }
	reps := make(map[int]*repCount)
	tasks := make(map[string]struct{})
	var costs []float64
	var cost, wall, calls, fix, reset float64

	for _, r := range recs {
		if r.Result == results.InfraError {
			s.Infra++
			continue
		}
		s.Cells++
		tasks[r.Task] = struct{}{}
		rc := reps[r.Rep]
		if rc == nil {
			rc = &repCount{}
			reps[r.Rep] = rc
		}
		rc.cells++
		if r.Result == results.Pass {
			s.Passes++
			rc.passes++
		} else {
			s.FailReasons[failReason(r)]++
		}
		costs = append(costs, r.CostUSD)
		cost += r.CostUSD
		wall += float64(r.WallMS)
		calls += float64(r.LLMCalls)
		fix += float64(r.FixIterations)
		reset += float64(r.TestsReset)
	}
	s.Tasks = len(tasks)
	if s.Cells == 0 {
		return s
	}

	n := float64(s.Cells)
	s.CostMean, s.CostMedian = cost/n, median(costs)
	if s.Passes > 0 {
		s.CostSolved, s.HasSolved = cost/float64(s.Passes), true
	}
	s.WallMS, s.LLMCalls, s.FixIterations, s.TestsReset = wall/n, calls/n, fix/n, reset/n

	var sum float64
	for i, rep := range slices.Sorted(maps.Keys(reps)) {
		rc := reps[rep]
		rate := float64(rc.passes) / float64(rc.cells)
		sum += rate
		if i == 0 || rate < s.RateMin {
			s.RateMin = rate
		}
		if i == 0 || rate > s.RateMax {
			s.RateMax = rate
		}
	}
	s.PassRate = sum / float64(len(reps))
	return s
}

func failReason(r results.Record) string {
	if r.FailReason == "" {
		return "unspecified"
	}
	return r.FailReason
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}
	return s[len(s)/2]
}

func failures(stats []ConfigStats) FailureTable {
	extra := make(map[string]struct{})
	for _, s := range stats {
		for reason := range s.FailReasons {
			if !slices.Contains(failOrder, reason) {
				extra[reason] = struct{}{}
			}
		}
	}
	t := FailureTable{Reasons: append(slices.Clone(failOrder), slices.Sorted(maps.Keys(extra))...)}
	for _, s := range stats {
		row := FailureRow{Config: s.Config, Cells: s.Cells, TestsReset: s.TestsReset}
		for _, reason := range t.Reasons {
			n := s.FailReasons[reason]
			row.Counts = append(row.Counts, n)
			row.Fails += n
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func groupStats(recs []results.Record, key func(results.Record) string, minTasks int) []Group {
	byKey := splitBy(recs, key)
	var out []Group
	for _, name := range slices.Sorted(maps.Keys(byKey)) {
		g := Group{Name: name, Tasks: distinctTasks(byKey[name])}
		g.Insufficient = g.Tasks < minTasks
		byConfig := splitBy(byKey[name], func(r results.Record) string { return r.Config })
		for _, c := range slices.Sorted(maps.Keys(byConfig)) {
			g.Configs = append(g.Configs, configStats(c, byConfig[c]))
		}
		out = append(out, g)
	}
	return out
}

// taskCell is what one config did on one task over its result repetitions.
type taskCell struct {
	n, passes int
	cost      float64
}

func (c taskCell) rate() float64 {
	return float64(c.passes) / float64(c.n)
}

// majority is more than half of the repetitions passing: two repetitions with one pass each
// are not a pass.
func (c taskCell) majority() bool {
	return c.passes*2 > c.n
}

func (c taskCell) meanCost() float64 {
	return c.cost / float64(c.n)
}

// perTaskCells folds the result cells (not infra_error) into one taskCell per task and config.
// It returns the tasks that have one, sorted.
func perTaskCells(recs []results.Record) ([]string, map[string]map[string]taskCell) {
	by := make(map[string]map[string]taskCell)
	for _, r := range recs {
		if r.Result == results.InfraError {
			continue
		}
		if by[r.Task] == nil {
			by[r.Task] = make(map[string]taskCell)
		}
		c := by[r.Task][r.Config]
		c.n++
		if r.Result == results.Pass {
			c.passes++
		}
		c.cost += r.CostUSD
		by[r.Task][r.Config] = c
	}
	return slices.Sorted(maps.Keys(by)), by
}

// pairRow pairs config with the baseline: per task, the majority of the repetitions decides win,
// tie or loss, and the pass-rate difference has a percentile-bootstrap interval over tasks. A
// task one side has no result for is left out and counted.
func pairRow(tasks []string, by map[string]map[string]taskCell, opt Options, config string) PairRow {
	row := PairRow{Config: config}
	var diffs []float64
	for _, t := range tasks {
		c, hasC := by[t][config]
		b, hasB := by[t][opt.Baseline]
		switch {
		case hasC && hasB:
			diffs = append(diffs, c.rate()-b.rate())
			switch {
			case c.majority() && !b.majority():
				row.Wins++
			case !c.majority() && b.majority():
				row.Losses++
			default:
				row.Ties++
			}
		case hasC:
			row.NoBaseline++
		case hasB:
			row.NoConfig++
		}
	}
	row.Tasks = len(diffs)
	if row.Tasks == 0 {
		return row
	}
	var sum float64
	for _, d := range diffs {
		sum += d
	}
	row.DiffPP = 100 * sum / float64(len(diffs))
	lo, hi := bootstrap(diffs, opt.Resamples, opt.Seed)
	row.LowPP, row.HighPP = 100*lo, 100*hi
	return row
}

// bootstrap is the 95% percentile interval of the mean of diffs: resamples draws of len(diffs)
// values with replacement, the 2.5th and 97.5th percentile of their means (index
// floor(q*(resamples-1)) of the sorted means). The source is fixed by the seed alone.
func bootstrap(diffs []float64, resamples int, seed uint64) (lo, hi float64) {
	n := len(diffs)
	if n == 0 || resamples < 1 {
		return 0, 0
	}
	rng := rand.New(rand.NewPCG(seed, bootstrapStream))
	means := make([]float64, resamples)
	for i := range means {
		var sum float64
		for range n {
			sum += diffs[rng.IntN(n)]
		}
		means[i] = sum / float64(n)
	}
	slices.Sort(means)
	return means[int(0.025*float64(resamples-1))], means[int(0.975*float64(resamples-1))]
}

func overhead(recs []results.Record) Overhead {
	var o Overhead
	var cost, dcost, wall, dms, dcalls float64
	for _, r := range recs {
		if r.Result == results.InfraError {
			continue
		}
		o.Cells++
		cost += r.CostUSD
		dcost += r.Decision.CostUSD
		wall += float64(r.WallMS)
		dms += float64(r.Decision.MS)
		dcalls += float64(r.Decision.Calls)
	}
	if o.Cells == 0 {
		return o
	}
	o.Present = true
	if cost > 0 {
		o.CostShare, o.HasCost = dcost/cost, true
	}
	if wall > 0 {
		o.TimeShare, o.HasTime = dms/wall, true
	}
	o.Calls = dcalls / float64(o.Cells)
	return o
}

func costCompare(overall []ConfigStats, tasks []string, by map[string]map[string]taskCell) CostCompare {
	var cc CostCompare
	find := func(config string) (ConfigStats, bool) {
		for _, s := range overall {
			if s.Config == config && s.Cells > 0 {
				return s, true
			}
		}
		return ConfigStats{}, false
	}
	b, okB := find(ConfigB)
	c, okC := find(ConfigC)
	if !okB {
		cc.Missing = append(cc.Missing, ConfigB)
	}
	if !okC {
		cc.Missing = append(cc.Missing, ConfigC)
	}
	if len(cc.Missing) > 0 {
		return cc
	}

	cc.BMean, cc.CMean = b.CostMean, c.CostMean
	if c.CostMean > 0 {
		cc.Ratio, cc.HasRatio = b.CostMean/c.CostMean, true
	}
	var sum float64
	for _, t := range tasks {
		tb, hasB := by[t][ConfigB]
		tc, hasC := by[t][ConfigC]
		if hasB && hasC {
			cc.PairedTasks++
			sum += tb.meanCost() - tc.meanCost()
		}
	}
	if cc.PairedTasks > 0 {
		cc.PairedDiff = sum / float64(cc.PairedTasks)
	}
	return cc
}

// stageChoice compares what a config chose with expected_stages, as sets, over its result
// cells that have expected_stages; the complexity distribution is over all its result cells.
func stageChoice(config string, recs []results.Record) StageChoice {
	sc := StageChoice{Config: config}
	scores := make(map[string]*StageScore)
	score := func(stage string) *StageScore {
		if scores[stage] == nil {
			scores[stage] = &StageScore{Stage: stage}
		}
		return scores[stage]
	}
	complexity := make(map[string]*ComplexityRow)

	for _, r := range recs {
		if r.Result == results.InfraError {
			continue
		}
		name := orNone(r.Complexity)
		if complexity[name] == nil {
			complexity[name] = &ComplexityRow{Complexity: name}
		}
		complexity[name].Cells++
		if r.Result == results.Pass {
			complexity[name].Passes++
		}

		if len(r.ExpectedStages) == 0 {
			continue
		}
		sc.Cells++
		got, want := normStages(r.Stages), normStages(r.ExpectedStages)
		if slices.Equal(got, want) {
			sc.Exact++
		}
		for _, s := range got {
			if slices.Contains(want, s) {
				score(s).TP++
			} else {
				score(s).FP++
			}
		}
		for _, s := range want {
			if !slices.Contains(got, s) {
				score(s).FN++
			}
		}
	}

	for _, stage := range ranked(slices.Collect(maps.Keys(scores)), stageOrder) {
		sc.Stages = append(sc.Stages, *scores[stage])
	}
	for _, name := range ranked(slices.Collect(maps.Keys(complexity)), complexityOrder) {
		sc.Complexity = append(sc.Complexity, *complexity[name])
	}
	return sc
}

// ranked sorts names by their place in order, then lexically; a name not in order goes last.
func ranked(names []string, order []string) []string {
	rank := func(s string) int {
		if i := slices.Index(order, s); i >= 0 {
			return i
		}
		return len(order)
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b))
	})
	return names
}

// normStages is a stage list as a set in the workflow's order.
func normStages(stages []string) []string {
	out := slices.Clone(stages)
	slices.Sort(out)
	return ranked(slices.Compact(out), stageOrder)
}

// translation reads the pairs of the `pair` field: each unordered pair once, the English task
// first. A pair whose other task has no line at all is only counted.
func translation(recs []results.Record, configs []string, by map[string]map[string]taskCell) Translation {
	var t Translation
	lang, partner := make(map[string]string), make(map[string]string)
	for _, r := range recs {
		lang[r.Task] = r.Lang
		if r.Pair != "" {
			partner[r.Task] = r.Pair
		}
	}

	seen := make(map[[2]string]bool)
	for _, a := range slices.Sorted(maps.Keys(partner)) {
		b := partner[a]
		key := [2]string{min(a, b), max(a, b)}
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := lang[b]; !ok {
			t.Incomplete++
			continue
		}
		en, ru := orient(a, b, lang)
		t.Pairs = append(t.Pairs, pairResult(recs, configs, by, en, ru))
	}

	slices.SortFunc(t.Pairs, func(x, y PairResult) int { return cmp.Compare(x.EN, y.EN) })
	for _, p := range t.Pairs {
		if p.HasStages {
			t.StagesOf++
			if p.SameStages {
				t.SameStages++
			}
		}
	}
	for i, c := range configs {
		total := PairTotal{Config: c}
		for _, p := range t.Pairs {
			if p.Passes[i].Known {
				total.Of++
				if p.Passes[i].Both {
					total.Both++
				}
			}
		}
		t.Both = append(t.Both, total)
	}
	return t
}

// orient puts the Russian task second; two tasks of the same or an unknown language go in
// lexical order.
func orient(a, b string, lang map[string]string) (en, ru string) {
	switch {
	case lang[a] == "ru" && lang[b] != "ru":
		return b, a
	case lang[b] == "ru" && lang[a] != "ru":
		return a, b
	}
	return min(a, b), max(a, b)
}

func pairResult(recs []results.Record, configs []string, by map[string]map[string]taskCell, en, ru string) PairResult {
	p := PairResult{EN: en, RU: ru}
	stagesEN, okEN := modalStages(recs, ConfigB, en)
	stagesRU, okRU := modalStages(recs, ConfigB, ru)
	if okEN && okRU {
		p.StagesEN, p.StagesRU = stagesEN, stagesRU
		p.HasStages, p.SameStages = true, slices.Equal(stagesEN, stagesRU)
	}
	for _, c := range configs {
		a, hasA := by[en][c]
		b, hasB := by[ru][c]
		p.Passes = append(p.Passes, PairPass{Config: c, Known: hasA && hasB, Both: hasA && hasB && a.majority() && b.majority()})
	}
	return p
}

// modalStages is the stage set a config chose most often on a task over its result cells; a tie
// goes to the lexically smaller list. ok is false when there is no such cell.
func modalStages(recs []results.Record, config, task string) (stages []string, ok bool) {
	counts := make(map[string]int)
	sets := make(map[string][]string)
	for _, r := range recs {
		if r.Config != config || r.Task != task || r.Result == results.InfraError {
			continue
		}
		s := normStages(r.Stages)
		k := strings.Join(s, ",")
		counts[k]++
		sets[k] = s
	}
	best, bestN := "", 0
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	if bestN == 0 {
		return nil, false
	}
	return sets[best], true
}
