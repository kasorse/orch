package summary

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"orchbench/internal/results"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden* from the current output")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (go test ./internal/summary -update): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the rendering (go test ./internal/summary -update rewrites it)\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func fixture(t *testing.T) []results.Record {
	t.Helper()
	recs, err := results.ReadFile(filepath.Join("testdata", "results.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return recs.Cells
}

func render(t *testing.T, r Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// rec is a result line with the fields most tests need.
func rec(task, config string, rep int, result string) results.Record {
	return results.Record{Schema: results.Schema, Task: task, Config: config, Rep: rep, Result: result}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func find(t *testing.T, stats []ConfigStats, config string) ConfigStats {
	t.Helper()
	for _, s := range stats {
		if s.Config == config {
			return s
		}
	}
	t.Fatalf("no stats for config %s", config)
	return ConfigStats{}
}

func TestGoldenSummary(t *testing.T) {
	// The fixture has 4 tasks, so the threshold is 3 to show a group both ways.
	r := Compute(fixture(t), Options{Seed: 1, MinTasks: 3})
	checkGolden(t, "summary.golden.md", []byte(render(t, r)))
}

func TestSummaryIsDeterministic(t *testing.T) {
	recs := fixture(t)
	opt := Options{Seed: 5, MinTasks: 3}

	first, second := Compute(recs, opt), Compute(recs, opt)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two reports of the same input differ:\n%+v\n%+v", first, second)
	}
	if render(t, first) != render(t, second) {
		t.Fatal("two renderings of the same input differ")
	}

	reversed := make([]results.Record, len(recs))
	for i, r := range recs {
		reversed[len(recs)-1-i] = r
	}
	if render(t, Compute(reversed, opt)) != render(t, first) {
		t.Fatal("the order of the records changed the report")
	}
}

func TestSeedChangesOnlyTheInterval(t *testing.T) {
	var recs []results.Record
	for i := range 9 {
		task := fmt.Sprintf("t%d", i)
		recs = append(recs, rec(task, "A", 1, results.Pass), rec(task, "A", 2, results.Fail))
		recs = append(recs, rec(task, "B", 1, results.Pass), rec(task, "B", 2, results.Pass))
		if i%3 == 0 {
			recs[len(recs)-2].Result = results.Fail
		}
	}

	a := Compute(recs, Options{Seed: 1}).Paired.Rows[0]
	again := Compute(recs, Options{Seed: 1}).Paired.Rows[0]
	if a != again {
		t.Errorf("the same seed gave %+v and %+v", a, again)
	}

	b := Compute(recs, Options{Seed: 2}).Paired.Rows[0]
	lo, hi := a.LowPP, a.HighPP
	a.LowPP, a.HighPP, b.LowPP, b.HighPP = 0, 0, 0, 0
	if a != b {
		t.Errorf("another seed changed more than the interval: %+v and %+v", a, b)
	}
	if lo > a.DiffPP || hi < a.DiffPP {
		t.Errorf("interval [%v, %v] does not contain the difference %v", lo, hi, a.DiffPP)
	}
}

func TestBootstrapByHand(t *testing.T) {
	tests := []struct {
		name     string
		diffs    []float64
		lo, hi   float64
		positive bool
	}{
		{"every task a win", []float64{1, 1, 1}, 1, 1, true},
		{"identical sides", []float64{0, 0, 0, 0}, 0, 0, false},
		{"one win, one loss", []float64{1, -1}, -1, 1, false},
		{"one task", []float64{0.5}, 0.5, 0.5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi := bootstrap(tt.diffs, DefaultResamples, 1)
			if lo != tt.lo || hi != tt.hi {
				t.Errorf("interval = [%v, %v], want [%v, %v]", lo, hi, tt.lo, tt.hi)
			}
			if tt.positive != (lo > 0) {
				t.Errorf("interval [%v, %v]: above zero = %v, want %v", lo, hi, lo > 0, tt.positive)
			}
		})
	}

	if lo, hi := bootstrap(nil, DefaultResamples, 1); lo != 0 || hi != 0 {
		t.Errorf("no tasks: interval = [%v, %v]", lo, hi)
	}
}

func TestPairedByHand(t *testing.T) {
	t.Run("every task a win", func(t *testing.T) {
		var recs []results.Record
		for _, task := range []string{"t1", "t2", "t3"} {
			recs = append(recs, rec(task, "A", 1, results.Fail), rec(task, "B", 1, results.Pass))
		}
		row := Compute(recs, Options{}).Paired.Rows[0]
		if row.Config != "B" || row.Tasks != 3 || row.Wins != 3 || row.Ties != 0 || row.Losses != 0 {
			t.Fatalf("row = %+v", row)
		}
		if row.DiffPP != 100 || row.LowPP != 100 || row.HighPP != 100 {
			t.Errorf("difference %v, interval [%v, %v], want 100 and [100, 100]", row.DiffPP, row.LowPP, row.HighPP)
		}
	})

	t.Run("identical sides", func(t *testing.T) {
		var recs []results.Record
		for _, task := range []string{"t1", "t2"} {
			recs = append(recs, rec(task, "A", 1, results.Pass), rec(task, "B", 1, results.Pass))
		}
		row := Compute(recs, Options{}).Paired.Rows[0]
		if row.Ties != 2 || row.DiffPP != 0 || row.LowPP != 0 || row.HighPP != 0 {
			t.Errorf("row = %+v, want two ties and 0 [0, 0]", row)
		}
	})

	t.Run("one win and one loss", func(t *testing.T) {
		recs := []results.Record{
			rec("t1", "A", 1, results.Fail), rec("t1", "B", 1, results.Pass),
			rec("t2", "A", 1, results.Pass), rec("t2", "B", 1, results.Fail),
		}
		row := Compute(recs, Options{}).Paired.Rows[0]
		if row.Wins != 1 || row.Losses != 1 || row.DiffPP != 0 || row.LowPP >= 0 || row.HighPP <= 0 {
			t.Errorf("row = %+v, want 1 win, 1 loss, mean 0 and an interval around it", row)
		}
	})
}

func TestMajorityNeedsMoreThanHalf(t *testing.T) {
	// B passes one of two repetitions: not a majority, so the task is a tie with A, and the
	// pass-rate difference still counts the half.
	recs := []results.Record{
		rec("t1", "A", 1, results.Fail), rec("t1", "A", 2, results.Fail),
		rec("t1", "B", 1, results.Pass), rec("t1", "B", 2, results.Fail),
		rec("t2", "A", 1, results.Fail), rec("t2", "A", 2, results.Fail),
		rec("t2", "B", 1, results.Pass), rec("t2", "B", 2, results.Pass),
	}
	row := Compute(recs, Options{}).Paired.Rows[0]
	if row.Wins != 1 || row.Ties != 1 || row.Losses != 0 {
		t.Errorf("row = %+v, want 1 win (2 of 2) and 1 tie (1 of 2)", row)
	}
	if !near(row.DiffPP, 75) {
		t.Errorf("difference = %v pp, want 75", row.DiffPP)
	}
}

func TestTasksWithoutOneSideAreLeftOutAndCounted(t *testing.T) {
	recs := []results.Record{
		rec("both", "A", 1, results.Fail), rec("both", "B", 1, results.Pass),
		rec("only-b", "B", 1, results.Pass),
		rec("only-a", "A", 1, results.Pass),
		rec("infra-a", "A", 1, results.InfraError), rec("infra-a", "B", 1, results.Pass),
	}
	row := Compute(recs, Options{}).Paired.Rows[0]
	if row.Tasks != 1 || row.NoBaseline != 2 || row.NoConfig != 1 {
		t.Errorf("row = %+v, want 1 paired task, 2 without A, 1 without B", row)
	}
}

func TestPassRateIsTheMeanOfTheRepetitionRates(t *testing.T) {
	// Repetition 1: 2 of 3 pass, repetition 2: 0 of 1, repetition 3: 2 of 2. The mean of the
	// rates is 5/9, not the 4 of 6 cells that passed.
	recs := []results.Record{
		rec("t1", "A", 1, results.Pass), rec("t2", "A", 1, results.Pass), rec("t3", "A", 1, results.Fail),
		rec("t1", "A", 2, results.Fail),
		rec("t1", "A", 3, results.Pass), rec("t2", "A", 3, results.Pass),
	}
	recs[0].CostUSD, recs[1].CostUSD, recs[2].CostUSD = 1, 2, 3
	recs[3].CostUSD, recs[4].CostUSD, recs[5].CostUSD = 4, 5, 6

	s := find(t, Compute(recs, Options{}).Overall, "A")
	if s.Cells != 6 || s.Passes != 4 {
		t.Fatalf("stats = %+v", s)
	}
	if !near(s.PassRate, 5.0/9) || s.RateMin != 0 || s.RateMax != 1 {
		t.Errorf("pass rate %v (%v–%v), want 5/9 (0–1)", s.PassRate, s.RateMin, s.RateMax)
	}
	if !near(s.CostMean, 3.5) || !near(s.CostMedian, 3.5) {
		t.Errorf("cost mean %v, median %v, want 3.5 and 3.5", s.CostMean, s.CostMedian)
	}
	if !s.HasSolved || !near(s.CostSolved, 21.0/4) {
		t.Errorf("cost per solved task = %v (%v), want 21/4", s.CostSolved, s.HasSolved)
	}
}

func TestCostPerSolvedTaskNeedsAPass(t *testing.T) {
	recs := []results.Record{rec("t1", "A", 1, results.Fail)}
	recs[0].CostUSD = 1
	s := find(t, Compute(recs, Options{}).Overall, "A")
	if s.HasSolved {
		t.Errorf("stats = %+v, want no cost per solved task", s)
	}
	if got := render(t, Compute(recs, Options{})); !strings.Contains(got, "| A | 0% (0–0) | $1.00 | $1.00 | — |") {
		t.Errorf("rendering does not show a dash for the cost per solved task:\n%s", got)
	}
}

func TestInfraErrorIsCountedAndLeftOutOfEveryNumber(t *testing.T) {
	recs := []results.Record{
		rec("t1", "A", 1, results.Pass),
		rec("t1", "A", 2, results.InfraError),
		rec("t2", "A", 1, results.InfraError),
	}
	recs[0].CostUSD = 2
	recs[1].CostUSD, recs[1].WallMS, recs[1].TestsReset = 100, 100000, 9
	recs[2].CostUSD = 100

	r := Compute(recs, Options{})
	s := find(t, r.Overall, "A")
	if s.Infra != 2 || s.Cells != 1 || s.Passes != 1 || s.Tasks != 1 {
		t.Fatalf("stats = %+v, want 2 infra and 1 result cell", s)
	}
	if s.PassRate != 1 || s.CostMean != 2 || s.WallMS != 0 || s.TestsReset != 0 {
		t.Errorf("stats = %+v: an infra_error cell reached a rate or a cost", s)
	}
	if c := r.Coverage; c.Cells != 3 || c.Infra != 2 || c.Pass != 1 {
		t.Errorf("coverage = %+v, want 3 cells of which 2 infra", c)
	}
}

func TestFailuresByReasonAndTestsReset(t *testing.T) {
	recs := []results.Record{
		rec("t1", "A", 1, results.Fail), rec("t2", "A", 1, results.Fail), rec("t3", "A", 1, results.Fail),
		rec("t4", "A", 1, results.Fail), rec("t5", "A", 1, results.Fail), rec("t6", "A", 1, results.Pass),
	}
	for i, reason := range []string{"tree_changed", "oracle", "oracle", "answer", "weird"} {
		recs[i].FailReason = reason
	}
	recs[0].TestsReset, recs[5].TestsReset = 2, 4

	r := Compute(recs, Options{})
	if want := []string{"oracle", "regression", "answer", "tree_changed", "weird"}; !reflect.DeepEqual(r.Failures.Reasons, want) {
		t.Fatalf("reasons = %v, want %v", r.Failures.Reasons, want)
	}
	row := r.Failures.Rows[0]
	if want := []int{2, 0, 1, 1, 1}; !reflect.DeepEqual(row.Counts, want) || row.Fails != 5 {
		t.Errorf("row = %+v, want counts %v", row, want)
	}
	if !near(row.TestsReset, 1) {
		t.Errorf("mean tests_reset = %v, want 1 (6 over 6 cells)", row.TestsReset)
	}
}

func TestStageChoicePrecisionAndRecall(t *testing.T) {
	recs := []results.Record{rec("t1", "B", 1, results.Pass), rec("t2", "B", 1, results.Pass), rec("t3", "B", 1, results.Fail)}
	recs[0].Stages = []string{"analysis", "development"}
	recs[0].ExpectedStages = []string{"development", "testing"}
	recs[1].Stages = []string{"testing", "development"}
	recs[1].ExpectedStages = []string{"development", "testing"}
	recs[2].Stages = []string{"review"} // no expected_stages: not counted

	r := Compute(recs, Options{})
	if len(r.Stages) != 1 {
		t.Fatalf("stages = %+v", r.Stages)
	}
	sc := r.Stages[0]
	if sc.Config != ConfigB || sc.Cells != 2 || sc.Exact != 1 {
		t.Fatalf("choice = %+v, want B with 1 exact of 2 cells", sc)
	}
	want := []StageScore{
		{Stage: "analysis", FP: 1},
		{Stage: "development", TP: 2},
		{Stage: "testing", TP: 1, FN: 1},
	}
	if !reflect.DeepEqual(sc.Stages, want) {
		t.Fatalf("scores = %+v, want %+v", sc.Stages, want)
	}

	if p, ok := want[0].Precision(); !ok || p != 0 {
		t.Errorf("analysis precision = %v, %v, want 0", p, ok)
	}
	if _, ok := want[0].Recall(); ok {
		t.Error("analysis was never expected: its recall must not exist")
	}
	if p, ok := want[2].Precision(); !ok || p != 1 {
		t.Errorf("testing precision = %v, %v, want 1", p, ok)
	}
	if rc, ok := want[2].Recall(); !ok || rc != 0.5 {
		t.Errorf("testing recall = %v, %v, want 0.5", rc, ok)
	}

	if got := sc.Complexity; len(got) != 1 || got[0].Complexity != none || got[0].Cells != 3 || got[0].Passes != 2 {
		t.Errorf("complexity = %+v", got)
	}
}

func TestDecisionOverheadAndCostCompare(t *testing.T) {
	recs := []results.Record{
		rec("t1", "B", 1, results.Pass), rec("t1", "B", 2, results.Fail), rec("t1", "B", 3, results.InfraError),
		rec("t1", "C", 1, results.Pass), rec("t2", "B", 1, results.Pass),
	}
	for i := range recs[:2] {
		recs[i].CostUSD, recs[i].WallMS = 2, 100000
		recs[i].Decision = results.Decision{CostUSD: 0.5, MS: 10000, Calls: 3}
	}
	recs[4].CostUSD, recs[4].WallMS = 1, 50000
	recs[4].Decision = results.Decision{Calls: 0}
	recs[2].CostUSD, recs[2].Decision.CostUSD = 100, 100 // infra: ignored
	recs[3].CostUSD = 4

	r := Compute(recs, Options{})
	o := r.Overhead
	if !o.Present || o.Cells != 3 || !near(o.CostShare, 1.0/5) || !near(o.TimeShare, 20000.0/250000) || !near(o.Calls, 2) {
		t.Errorf("overhead = %+v", o)
	}
	c := r.CostCompare
	if len(c.Missing) != 0 || !near(c.BMean, 5.0/3) || c.CMean != 4 || !near(c.Ratio, 5.0/12) {
		t.Errorf("cost compare = %+v", c)
	}
	if c.PairedTasks != 1 || !near(c.PairedDiff, 2-4) {
		t.Errorf("paired cost = %d tasks, %v; want 1 task and -2", c.PairedTasks, c.PairedDiff)
	}
}

func TestNoBMeansNoBSections(t *testing.T) {
	r := Compute([]results.Record{rec("t1", "A", 1, results.Pass)}, Options{})
	if r.Overhead.Present || len(r.Stages) != 0 || len(r.CostCompare.Missing) != 2 {
		t.Fatalf("report = %+v", r)
	}
	got := render(t, r)
	for _, want := range []string{"no B results yet", "no B or C results yet", "no B or Bmock results yet", "no complete en/ru pair"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendering does not say %q:\n%s", want, got)
		}
	}
}

func TestTranslationPairs(t *testing.T) {
	mk := func(task, pair, lang string, passes ...bool) []results.Record {
		var out []results.Record
		for i, pass := range passes {
			r := rec(task, "B", i+1, results.Fail)
			if pass {
				r.Result = results.Pass
			}
			r.Pair, r.Lang, r.Stages = pair, lang, []string{"development"}
			out = append(out, r)
		}
		return out
	}
	var recs []results.Record
	// Pair a: the pair field is on the Russian side only; both pass, same stages.
	recs = append(recs, mk("a-ru", "a", "ru", true, true)...)
	recs = append(recs, mk("a", "", "en", true, true)...)
	// Pair b: the English side passes, the Russian one fails.
	recs = append(recs, mk("b", "b-ru", "en", true, true)...)
	recs = append(recs, mk("b-ru", "b", "ru", false, false)...)
	// Pair c: the Russian task has no cell, so the pair is only counted.
	recs = append(recs, mk("c", "c-ru", "en", true)...)

	tr := Compute(recs, Options{}).Translation
	if len(tr.Pairs) != 2 || tr.Incomplete != 1 {
		t.Fatalf("translation = %+v, want 2 pairs and 1 incomplete", tr)
	}
	if p := tr.Pairs[0]; p.EN != "a" || p.RU != "a-ru" || !p.SameStages || !p.Passes[0].Both {
		t.Errorf("pair a = %+v", p)
	}
	if p := tr.Pairs[1]; p.EN != "b" || p.RU != "b-ru" || p.Passes[0].Both {
		t.Errorf("pair b = %+v", p)
	}
	if tr.SameStages != 2 || tr.StagesOf != 2 || tr.Both[0].Both != 1 || tr.Both[0].Of != 2 {
		t.Errorf("totals = %+v", tr)
	}
}

func TestPartialInputSaysHowManyCells(t *testing.T) {
	var recs []results.Record
	for _, task := range []string{"t1", "t2"} {
		for _, config := range []string{"A", "B"} {
			recs = append(recs, rec(task, config, 1, results.Pass))
		}
	}

	full := Compute(recs, Options{})
	if c := full.Coverage; c.Cells != 4 || c.Expected != 4 || c.Missing != 0 {
		t.Fatalf("coverage = %+v", c)
	}
	if got := render(t, full); strings.Contains(got, "partial:") || !strings.Contains(got, "Based on 4 cells from 2 tasks × 2 configs × up to 1 rep") {
		t.Errorf("a complete grid is reported as partial or without its size:\n%s", got)
	}

	partial := Compute(recs[:3], Options{})
	if c := partial.Coverage; c.Cells != 3 || c.Expected != 4 || c.Missing != 1 {
		t.Fatalf("coverage = %+v", c)
	}
	if got := render(t, partial); !strings.Contains(got, "partial: 3 of 4 cells") {
		t.Errorf("a partial grid does not say how many cells it is based on:\n%s", got)
	}

	// The fixture misses one cell of C and has a replaced line.
	if c := Compute(fixture(t), Options{}).Coverage; c.Cells != 31 || c.Expected != 32 || c.Missing != 1 || c.Tasks != 4 || c.Reps != 2 {
		t.Errorf("fixture coverage = %+v", c)
	}
}

func TestEmptyInput(t *testing.T) {
	r := Compute(nil, Options{})
	got := render(t, r)
	if !strings.Contains(got, "No results yet") || strings.Contains(got, "## ") {
		t.Errorf("rendering of no records:\n%s", got)
	}
}

func TestSmallGroupsAreMarkedInsufficient(t *testing.T) {
	var recs []results.Record
	for i := range 12 {
		language := "go"
		if i >= 9 {
			language = "python"
		}
		r := rec(fmt.Sprintf("t%02d", i), "A", 1, results.Pass)
		r.Language, r.Category = language, "bugfix"
		recs = append(recs, r)
	}

	r := Compute(recs, Options{})
	if len(r.Groups) != 2 || r.Groups[0].Name != "go" || r.Groups[0].Tasks != 9 || !r.Groups[0].Insufficient {
		t.Fatalf("groups = %+v", r.Groups)
	}
	if python := r.Groups[1]; python.Name != "python" || python.Tasks != 3 || !python.Insufficient {
		t.Errorf("python = %+v", python)
	}
	if len(r.Categories) != 1 || r.Categories[0].Tasks != 12 || r.Categories[0].Insufficient {
		t.Errorf("categories = %+v, want one group of 12 tasks with enough data", r.Categories)
	}
	got := render(t, r)
	for _, want := range []string{"### go (9 tasks, insufficient data)", "### python (3 tasks, insufficient data)", "### bugfix (12 tasks)"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendering does not contain %q:\n%s", want, got)
		}
	}

	if r := Compute(recs, Options{MinTasks: 9}); r.Groups[0].Insufficient || !r.Groups[1].Insufficient {
		t.Errorf("with MinTasks 9 the groups = %+v", r.Groups)
	}
}

func TestByChoosesTheGrouping(t *testing.T) {
	a, b := rec("t1", "A", 1, results.Pass), rec("t2", "A", 1, results.Pass)
	a.Language, a.Category, a.Contributor = "go", "bugfix", "owner"
	b.Language, b.Category, b.Contributor = "go", "feature", "guest"

	for by, want := range map[string][]string{
		ByLanguage:    {"go"},
		ByCategory:    {"bugfix", "feature"},
		ByContributor: {"guest", "owner"},
	} {
		var names []string
		for _, g := range Compute([]results.Record{a, b}, Options{By: by}).Groups {
			names = append(names, g.Name)
		}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("--by %s: groups = %v, want %v", by, names, want)
		}
	}

	if !ValidBy(ByContributor) || ValidBy("colour") {
		t.Error("ValidBy accepts the wrong names")
	}
}

func TestAbsentBaselineIsANoteNotAPanic(t *testing.T) {
	r := Compute(fixture(t), Options{Baseline: "Z", MinTasks: 3})
	if r.Paired.BaselineSeen || len(r.Paired.Rows) != 0 {
		t.Fatalf("paired = %+v", r.Paired)
	}
	if got := render(t, r); !strings.Contains(got, "## Paired vs Z") || !strings.Contains(got, "no Z results: nothing to pair against") {
		t.Errorf("rendering does not explain the missing baseline:\n%s", got)
	}

	only := Compute([]results.Record{rec("t1", "A", 1, results.Pass)}, Options{})
	if got := render(t, only); !strings.Contains(got, "no config besides A yet") {
		t.Errorf("rendering does not explain that A is alone:\n%s", got)
	}
}

func TestFormatting(t *testing.T) {
	tests := []struct{ got, want string }{
		{duration(0), "0s"},
		{duration(45000), "45s"},
		{duration(252000), "4m12s"},
		{duration(3720000), "1h02m"},
		{signed(4.4), "+4"},
		{signed(-6.4), "−6"},
		{signed(-0.2), "0"},
		{signedMoney(-1), "−$1.00"},
		{signedMoney(0.5), "+$0.50"},
		{money(2.5), "$2.50"},
		{pct(0.5), "50%"},
		{plural(1, "cell"), "1 cell"},
		{plural(2, "cell"), "2 cells"},
		{stageList(nil), dash},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}
