package report

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/domain"
)

// RunSummary renders summary.md: one run's report for a reader - how it ended, what it
// cost, how long it took, and each step's time, cost, tokens, model, prompt, tool calls
// and code change (MVP.md §43, where every field is explained). It reads only the run's
// own record and decision.json (trace, nil when there is none) and makes no call; the
// same record always renders the same text.
func RunSummary(run domain.Run, trace *domain.DecisionTrace) string {
	out := domain.ComputeOutcome(run, trace)
	if run.Outcome != nil {
		out = *run.Outcome
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Run %s\n\n", run.ID)
	writeTotals(&b, run, trace, out)
	writeSteps(&b, run)
	writeTools(&b, run)
	writeChanges(&b, run)
	writeNotes(&b, run)
	b.WriteString("\nWhat each field means: MVP.md §43 (Run summary).\n")
	return b.String()
}

func writeTotals(b *strings.Builder, run domain.Run, trace *domain.DecisionTrace, out domain.Outcome) {
	b.WriteString("| Field | Value |\n| --- | --- |\n")
	row := func(k, v string) { fmt.Fprintf(b, "| %s | %s |\n", k, cell(v)) }

	row("Task", excerpt(run.Task.Description, 120))
	if src := run.Task.Source; src != nil {
		row("Source", fmt.Sprintf("%s %s (%s)", src.Kind, src.Key, src.URL))
	}
	row("Started", run.CreatedAt.Format("2006-01-02 15:04:05 -0700"))
	status := string(out.Status)
	if out.FailureReason != "" {
		status += ": " + failureLine(out.FailureReason)
	}
	row("Status", status)
	row("Workflow", runStageSet(run))
	row("Decision", decisionLine(run, trace))
	row("Steps", fmt.Sprintf("%d (fix iterations %d)", len(run.Executions), out.FixIterations))
	row("Wall time", duration(out.DurationMS))
	row("Active time", duration(out.ActiveMS))
	if out.DecisionOverhead.MS > 0 || out.DecisionOverhead.Calls > 0 {
		row("Decision time", duration(out.DecisionOverhead.MS))
	}

	cost := money(out.Usage.CostUSD, true)
	if n := out.Usage.PartialCalls; n > 0 {
		cost += fmt.Sprintf(" or more: %s ended without a report, their cost is unknown", plural(n, "call"))
	}
	row("Cost", cost)
	if out.DecisionOverhead.Calls > 0 {
		row("Decision cost", fmt.Sprintf("%s (%s)", money(out.DecisionOverhead.CostUSD, true), plural(out.DecisionOverhead.Calls, "call")))
	}
	u := out.Usage.Usage
	row("Tokens", fmt.Sprintf("prompt %s (input %s, cache read %s, cache write %s), output %s",
		tokens(u.PromptTokens()), tokens(u.InputTokens), tokens(u.CacheReadTokens), tokens(u.CacheCreationTokens), tokens(u.OutputTokens)))
	row("Provider calls", fmt.Sprintf("%d", out.Usage.Calls))

	tests := "not run"
	if p := out.TestsPassedFinal; p != nil {
		tests = map[bool]string{true: "passed", false: "failed"}[*p]
		if !*p {
			// A failed check means the tests did not run (specs/quality/SPEC.md §5.3).
			for i := len(run.Executions) - 1; i >= 0; i-- {
				if t := run.Executions[i].TestResult; t != nil {
					if t.FailedCheck != "" {
						tests = "failed (check " + t.FailedCheck + ")"
					}
					break
				}
			}
		}
	}
	row("Tests (last run)", tests)
	verdict := noValue
	if out.ReviewVerdict != "" {
		verdict = string(out.ReviewVerdict)
	}
	row("Review verdict", verdict)
	change := noValue
	if out.Diff != nil {
		change = diffLine(*out.Diff)
	}
	row("Code change", change)
	if run.Commit != "" {
		row("Commit", run.Commit)
	}
}

func decisionLine(run domain.Run, trace *domain.DecisionTrace) string {
	d := run.Decision
	if d == nil {
		return noValue
	}
	if d.Source == domain.DecisionSourceManual {
		return "manual (run --stage)"
	}
	s := string(d.Source)
	if trace != nil && trace.Provider != "" {
		s += ", " + trace.Provider
		if trace.Model != "" {
			s += " " + trace.Model
		}
	}
	if c := d.Complexity.Selected; c != "" {
		s += ", complexity " + string(c)
	}
	if d.Edit != nil {
		s += ", edited by the operator"
	}
	return s
}

func writeSteps(b *strings.Builder, run domain.Run) {
	if len(run.Executions) == 0 {
		return
	}
	b.WriteString("\n## Steps\n\n")
	b.WriteString("| # | Step | Result | Time | API time | Cost | Prompt tokens | Output tokens | Turns | Model | Prompt |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, e := range run.Executions {
		step := string(e.Action)
		if e.Attempt > 0 {
			step += fmt.Sprintf(" (resume %d)", e.Attempt)
		}
		apiTime, model, prompt := noValue, noValue, noValue
		if c := e.Call; c != nil {
			if c.APIMS > 0 {
				apiTime = duration(c.APIMS)
			}
			if len(c.Models) > 0 {
				names := make([]string, len(c.Models))
				for i, m := range c.Models {
					names[i] = m.Model
				}
				model = strings.Join(names, ", ")
			}
			prompt = size(c.PromptBytes)
			if len(c.Truncated) > 0 {
				parts := make([]string, len(c.Truncated))
				for i, p := range c.Truncated {
					parts[i] = string(p)
				}
				prompt += ", cut: " + strings.Join(parts, ", ")
			}
		}
		if model == noValue && e.Provider != nil {
			model = e.Provider.Model + " (configured)"
		}
		cost, inTok, outTok, turns := noValue, noValue, noValue, noValue
		if u := e.Usage; u != nil {
			bound := ""
			if u.Partial {
				bound = "≥ "
				cost = "unknown"
			} else {
				cost = money(u.CostUSD, true)
			}
			inTok = tokens(u.PromptTokens())
			outTok = bound + tokens(u.OutputTokens)
			turns = fmt.Sprintf("%d", u.Turns)
		}
		cells := []string{fmt.Sprintf("%d", e.Index), step, stepResult(e), duration(domain.ExecutionMS(e)),
			apiTime, cost, inTok, outTok, turns, model, prompt}
		for i := range cells {
			cells[i] = cell(cells[i])
		}
		fmt.Fprintf(b, "| %s |\n", strings.Join(cells, " | "))
	}
}

// stepResult is what one step came to: a test's exit code, a review's verdict, otherwise
// the step's status.
func stepResult(e domain.StepExecution) string {
	if e.Status == domain.StepFailed {
		return "failed"
	}
	switch {
	case e.TestResult != nil:
		if e.TestResult.Success {
			return "passed"
		}
		if c := e.TestResult.FailedCheck; c != "" {
			return fmt.Sprintf("failed (check %s, exit %d)", c, e.TestResult.ExitCode)
		}
		return fmt.Sprintf("failed (exit %d)", e.TestResult.ExitCode)
	case e.ReviewResult != nil:
		return fmt.Sprintf("%s (%s)", e.ReviewResult.Decide(), reviewCounts(*e.ReviewResult))
	}
	return string(e.Status)
}

// reviewCounts is the issue count, and the criteria met out of the total when the review
// judged any.
func reviewCounts(r domain.ReviewResult) string {
	s := plural(len(r.Issues), "issue")
	if n := len(r.Criteria); n > 0 {
		s += fmt.Sprintf(", %d/%d criteria met", n-len(r.UnmetCriteria()), n)
	}
	return s
}

func writeTools(b *strings.Builder, run domain.Run) {
	var rows []string
	for _, e := range run.Executions {
		if e.Call == nil || len(e.Call.Tools) == 0 {
			continue
		}
		type count struct {
			name string
			n    int
		}
		var counts []count
		total := 0
		for name, n := range e.Call.Tools {
			counts = append(counts, count{name, n})
			total += n
		}
		slices.SortFunc(counts, func(a, b count) int {
			return cmp.Or(b.n-a.n, strings.Compare(a.name, b.name))
		})
		parts := make([]string, len(counts))
		for i, c := range counts {
			parts[i] = fmt.Sprintf("%s %d", c.name, c.n)
		}
		rows = append(rows, fmt.Sprintf("| %d | %s | %d | %s |", e.Index, e.Action, total, cell(strings.Join(parts, ", "))))
	}
	if len(rows) == 0 {
		return
	}
	b.WriteString("\n## Tool calls\n\n| # | Step | Calls | By tool |\n| --- | --- | --- | --- |\n")
	b.WriteString(strings.Join(rows, "\n") + "\n")
}

func writeChanges(b *strings.Builder, run domain.Run) {
	var rows []string
	for _, e := range run.Executions {
		d := e.Diff
		if d == nil {
			continue
		}
		total := noValue
		if d.Run != nil {
			total = diffLine(*d.Run)
		}
		rows = append(rows, fmt.Sprintf("| %d | %s | %s | %s | %s |", e.Index, diffLine(d.Step), total,
			cell(pathList(d.NotReported)), cell(pathList(d.ReportedUnchanged))))
	}
	if len(rows) == 0 {
		return
	}
	b.WriteString("\n## Code changes\n\n")
	b.WriteString("| # | This step | Run so far | Changed, not in changed_files | In changed_files, unchanged |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	b.WriteString(strings.Join(rows, "\n") + "\n")
}

func writeNotes(b *strings.Builder, run domain.Run) {
	var notes []string
	for _, e := range run.Executions {
		if e.Usage != nil && e.Usage.Partial {
			notes = append(notes, fmt.Sprintf("#%d %s ended without the CLI's final report: its tokens are summed from the "+
				"messages it streamed, the output tokens are a lower bound, and its cost is unknown.", e.Index, e.Action))
		}
		if e.Error != "" {
			notes = append(notes, fmt.Sprintf("#%d %s failed: %s", e.Index, e.Action, failureLine(e.Error)))
		}
	}
	if len(notes) == 0 {
		return
	}
	b.WriteString("\n## Notes\n\n")
	for _, n := range notes {
		b.WriteString("- " + oneLine(n) + "\n")
	}
}

func diffLine(d domain.DiffStat) string {
	s := fmt.Sprintf("%s, +%d −%d", plural(d.Files, "file"), d.Insertions, d.Deletions)
	if d.Binary > 0 {
		s += fmt.Sprintf(" (%d binary)", d.Binary)
	}
	return s
}

func pathList(paths []string) string {
	if len(paths) == 0 {
		return noValue
	}
	return strings.Join(paths, ", ")
}

// failureLine is the line of an error that says what went wrong: a provider error's
// "failure:" line when it has one, otherwise its first line, at most 200 characters.
func failureLine(err string) string {
	line := firstLine(err)
	for _, l := range strings.Split(err, "\n") {
		if strings.HasPrefix(l, "failure: ") {
			line = l
			break
		}
	}
	return excerpt(line, 200)
}

// cell makes s safe inside a Markdown table cell: one line, no column separator.
func cell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", `\|`)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// duration renders milliseconds the way a person reads a run: 0.4s, 38.8s, 7m 12s, 1h 39m.
func duration(ms int64) string {
	switch {
	case ms <= 0:
		return noValue
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	case ms < 3_600_000:
		return fmt.Sprintf("%dm %ds", ms/60_000, ms%60_000/1000)
	}
	return fmt.Sprintf("%dh %dm", ms/3_600_000, ms%3_600_000/60_000)
}

func tokens(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
}

func size(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}
