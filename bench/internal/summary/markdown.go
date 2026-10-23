package summary

import (
	"fmt"
	"io"
	"math"
	"strings"
)

// dash stands for a number that does not exist: a rate over no cells, a precision over no
// chosen stage.
const dash = "—"

// Render writes the report as Markdown (SPEC.md §10, example §13.11). The layout is fixed, so
// the same report gives the same bytes. A section without data says so in one line.
func Render(w io.Writer, r Report) error {
	var b strings.Builder
	writeHeader(&b, r)
	if r.Coverage.Cells > 0 {
		writeOverall(&b, r)
		writeFailures(&b, r)
		writeGroups(&b, "By category", r.Categories, r.Options.MinTasks)
		if r.Options.By != ByCategory {
			writeGroups(&b, "By "+r.Options.By, r.Groups, r.Options.MinTasks)
		}
		writePaired(&b, r)
		writeOverhead(&b, r)
		writeCostCompare(&b, r)
		writeStages(&b, r)
		writeComplexity(&b, r)
		writeTranslation(&b, r)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeHeader(b *strings.Builder, r Report) {
	c := r.Coverage
	b.WriteString("# Benchmark summary\n\n")
	if c.Cells == 0 {
		b.WriteString("No results yet: results.jsonl has no cell.\n")
		return
	}
	fmt.Fprintf(b, "Based on %s from %s × %s × up to %s: %d pass, %d fail, %d infra_error (counted, left out of every rate and cost).\n",
		plural(c.Cells, "cell"), plural(c.Tasks, "task"), plural(c.Configs, "config"), plural(c.Reps, "rep"), c.Pass, c.Fail, c.Infra)
	if c.Missing > 0 {
		fmt.Fprintf(b, "partial: %d of %d cells, %d missing from the grid of tasks × configs × reps\n", c.Cells, c.Expected, c.Missing)
	}
	if c.Replaced > 0 {
		fmt.Fprintf(b, "%s replaced by a later line for the same cell (redo)\n", plural(c.Replaced, "result line"))
	}
	if c.Partial > 0 {
		b.WriteString("the last line of results.jsonl is half written and was ignored (a run is writing)\n")
	}
	fmt.Fprintf(b, "\nBaseline %s · seed %d · %d bootstrap resamples · a group under %s is marked insufficient data\n",
		r.Options.Baseline, r.Options.Seed, r.Options.Resamples, plural(r.Options.MinTasks, "task"))
}

func writeOverall(b *strings.Builder, r Report) {
	section(b, fmt.Sprintf("Overall (%s × %s)", plural(r.Coverage.Tasks, "task"), plural(r.Coverage.Reps, "rep")))
	t := newTable("config", "pass rate", "cost/task mean", "cost/task median", "cost/solved", "time/task", "LLM calls", "fix iterations", "infra")
	for _, s := range r.Overall {
		if s.Cells == 0 {
			t.add(s.Config, dash, dash, dash, dash, dash, dash, dash, itoa(s.Infra))
			continue
		}
		t.add(s.Config, passRate(s), money(s.CostMean), money(s.CostMedian), solved(s), duration(s.WallMS),
			one(s.LLMCalls), one(s.FixIterations), itoa(s.Infra))
	}
	t.write(b)
}

func writeFailures(b *strings.Builder, r Report) {
	section(b, "Failures by fail_reason")
	header := append(append([]string{"config", "fails"}, r.Failures.Reasons...), "mean tests_reset")
	t := newTable(header...)
	for _, row := range r.Failures.Rows {
		cells := []string{row.Config, itoa(row.Fails)}
		for _, n := range row.Counts {
			cells = append(cells, itoa(n))
		}
		if row.Cells == 0 {
			cells = append(cells, dash)
		} else {
			cells = append(cells, one(row.TestsReset))
		}
		t.add(cells...)
	}
	t.write(b)
}

func writeGroups(b *strings.Builder, title string, groups []Group, minTasks int) {
	section(b, title)
	fmt.Fprintf(b, "A group under %s is shown with its numbers and marked insufficient data: no conclusion from it.\n", plural(minTasks, "task"))
	for _, g := range groups {
		heading := plural(g.Tasks, "task")
		if g.Insufficient {
			heading += ", insufficient data"
		}
		fmt.Fprintf(b, "\n### %s (%s)\n\n", g.Name, heading)
		t := newTable("config", "pass rate", "cost/task mean", "infra")
		for _, s := range g.Configs {
			if s.Cells == 0 {
				t.add(s.Config, dash, dash, itoa(s.Infra))
				continue
			}
			t.add(s.Config, passRate(s), money(s.CostMean), itoa(s.Infra))
		}
		t.write(b)
	}
}

func writePaired(b *strings.Builder, r Report) {
	p := r.Paired
	section(b, fmt.Sprintf("Paired vs %s (per task, majority over reps)", p.Baseline))
	switch {
	case !p.BaselineSeen:
		fmt.Fprintf(b, "no %s results: nothing to pair against (--baseline names a config of this directory)\n", p.Baseline)
		return
	case len(p.Rows) == 0:
		fmt.Fprintf(b, "no config besides %s yet\n", p.Baseline)
		return
	}
	fmt.Fprintf(b, "A task counts when both configs have a result for it. Difference of pass rates in percentage points, with a 95%% percentile-bootstrap interval over tasks (%d resamples, seed %d).\n\n",
		r.Options.Resamples, r.Options.Seed)
	t := newTable("config", "tasks", "wins", "ties", "losses", "pass-rate difference", "95% CI", "tasks without "+p.Baseline, "tasks without config")
	for _, row := range p.Rows {
		if row.Tasks == 0 {
			t.add(row.Config, "0", dash, dash, dash, dash, dash, itoa(row.NoBaseline), itoa(row.NoConfig))
			continue
		}
		t.add(row.Config, itoa(row.Tasks), itoa(row.Wins), itoa(row.Ties), itoa(row.Losses),
			signed(row.DiffPP)+" pp", "["+signed(row.LowPP)+", "+signed(row.HighPP)+"]", itoa(row.NoBaseline), itoa(row.NoConfig))
	}
	t.write(b)
}

func writeOverhead(b *strings.Builder, r Report) {
	section(b, "Decision overhead (B)")
	o := r.Overhead
	if !o.Present {
		b.WriteString("no B results yet\n")
		return
	}
	share := func(v float64, ok bool) string {
		if !ok {
			return dash
		}
		return pct(v)
	}
	fmt.Fprintf(b, "%s of cost, %s of time; %s decision calls per run (%s)\n",
		share(o.CostShare, o.HasCost), share(o.TimeShare, o.HasTime), one(o.Calls), plural(o.Cells, "cell"))
}

func writeCostCompare(b *strings.Builder, r Report) {
	section(b, "B vs C cost")
	c := r.CostCompare
	if len(c.Missing) > 0 {
		fmt.Fprintf(b, "no %s results yet\n", strings.Join(c.Missing, " or "))
		return
	}
	ratio := dash
	if c.HasRatio {
		ratio = fmt.Sprintf("%.2fx", c.Ratio)
	}
	fmt.Fprintf(b, "B %s per cell, C %s per cell: B costs %s C.", money(c.BMean), money(c.CMean), ratio)
	if c.PairedTasks > 0 {
		fmt.Fprintf(b, " Paired over %s: B − C = %s per task.", plural(c.PairedTasks, "task"), signedMoney(c.PairedDiff))
	} else {
		b.WriteString(" No task has a result for both.")
	}
	b.WriteString("\n")
}

func writeStages(b *strings.Builder, r Report) {
	section(b, "Stage choice (B, C) vs expected_stages")
	if len(r.Stages) == 0 {
		b.WriteString("no B or C results yet\n")
		return
	}
	b.WriteString("The chosen stages against expected_stages as sets, over the cells that have expected_stages.\n")
	for _, sc := range r.Stages {
		fmt.Fprintf(b, "\n### %s\n\n", sc.Config)
		if sc.Cells == 0 {
			b.WriteString("no cell has expected_stages\n")
			continue
		}
		fmt.Fprintf(b, "exact match %d/%d cells\n\n", sc.Exact, sc.Cells)
		t := newTable("stage", "TP", "FP", "FN", "precision", "recall")
		for _, s := range sc.Stages {
			p, okP := s.Precision()
			rc, okR := s.Recall()
			t.add(s.Stage, itoa(s.TP), itoa(s.FP), itoa(s.FN), ratio2(p, okP), ratio2(rc, okR))
		}
		t.write(b)
	}
}

func writeComplexity(b *strings.Builder, r Report) {
	section(b, "Complexity (B, C)")
	if len(r.Stages) == 0 {
		b.WriteString("no B or C results yet\n")
		return
	}
	t := newTable("config", "complexity", "cells", "pass rate")
	for _, sc := range r.Stages {
		for _, row := range sc.Complexity {
			t.add(sc.Config, row.Complexity, itoa(row.Cells), pct(float64(row.Passes)/float64(row.Cells)))
		}
	}
	t.write(b)
}

func writeTranslation(b *strings.Builder, r Report) {
	section(b, "Translation (en/ru)")
	t := r.Translation
	if len(t.Pairs) == 0 {
		b.WriteString("no complete en/ru pair")
		if t.Incomplete > 0 {
			fmt.Fprintf(b, " (%s with a side missing)", plural(t.Incomplete, "pair"))
		}
		b.WriteString("\n")
		return
	}
	header := []string{"pair (en / ru)", "B stages en", "B stages ru", "same stages"}
	for _, total := range t.Both {
		header = append(header, total.Config+" both passed")
	}
	tab := newTable(header...)
	for _, p := range t.Pairs {
		row := []string{p.EN + " / " + p.RU, dash, dash, dash}
		if p.HasStages {
			row[1], row[2], row[3] = stageList(p.StagesEN), stageList(p.StagesRU), yesNo(p.SameStages)
		}
		for _, pp := range p.Passes {
			if pp.Known {
				row = append(row, yesNo(pp.Both))
			} else {
				row = append(row, dash)
			}
		}
		tab.add(row...)
	}
	tab.write(b)

	fmt.Fprintf(b, "\nsame stages %d/%d pairs; both passed:", t.SameStages, t.StagesOf)
	for i, total := range t.Both {
		sep := ","
		if i == 0 {
			sep = ""
		}
		fmt.Fprintf(b, "%s %s %d/%d", sep, total.Config, total.Both, total.Of)
	}
	b.WriteString("\n")
	if t.Incomplete > 0 {
		fmt.Fprintf(b, "%s left out: the other side has no cell\n", plural(t.Incomplete, "pair"))
	}
}

// section starts a section: a blank line, its heading, a blank line.
func section(b *strings.Builder, title string) {
	b.WriteString("\n## " + title + "\n\n")
}

// table is a Markdown table; its cells are not padded, so a row is a function of its values only.
type table struct {
	header []string
	rows   [][]string
}

func newTable(header ...string) *table {
	return &table{header: header}
}

func (t *table) add(cells ...string) {
	row := make([]string, len(cells))
	for i, c := range cells {
		row[i] = strings.ReplaceAll(c, "|", `\|`)
	}
	t.rows = append(t.rows, row)
}

func (t *table) write(b *strings.Builder) {
	line := func(cells []string) {
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	line(t.header)
	sep := make([]string, len(t.header))
	for i := range sep {
		sep[i] = "---"
	}
	line(sep)
	for _, row := range t.rows {
		line(row)
	}
}

func itoa(n int) string { return fmt.Sprint(n) }

// plural is "1 task", "2 tasks".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func stageList(stages []string) string {
	if len(stages) == 0 {
		return dash
	}
	return strings.Join(stages, ",")
}

// pct is a fraction as a whole percent.
func pct(v float64) string { return fmt.Sprintf("%.0f%%", 100*v) }

// passRate is `63% (60–67)`: the mean over repetitions and the smallest and largest of them.
func passRate(s ConfigStats) string {
	return fmt.Sprintf("%s (%.0f–%.0f)", pct(s.PassRate), 100*s.RateMin, 100*s.RateMax)
}

func money(v float64) string { return fmt.Sprintf("$%.2f", v) }

func solved(s ConfigStats) string {
	if !s.HasSolved {
		return dash
	}
	return money(s.CostSolved)
}

// signedMoney is a difference of costs with a true minus sign.
func signedMoney(v float64) string {
	if v < 0 {
		return fmt.Sprintf("−$%.2f", -v)
	}
	return fmt.Sprintf("+$%.2f", v)
}

func one(v float64) string { return fmt.Sprintf("%.1f", v) }

func ratio2(v float64, ok bool) string {
	if !ok {
		return dash
	}
	return fmt.Sprintf("%.2f", v)
}

// signed rounds to whole percentage points with an explicit sign and a true minus; zero has none.
func signed(v float64) string {
	r := math.Round(v)
	switch {
	case r == 0:
		return "0"
	case r > 0:
		return fmt.Sprintf("+%.0f", r)
	}
	return fmt.Sprintf("−%.0f", -r)
}

// duration is milliseconds as `4m12s`, `45s` or `1h02m`.
func duration(ms float64) string {
	s := int64(math.Round(ms / 1000))
	h, m, sec := s/3600, s%3600/60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}
