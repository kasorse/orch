package logging

import (
	"fmt"
	"strings"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// The pretty renderer: what ConsoleReporter prints when Pretty is set (an interactive
// terminal). It shows the same information as the plain renderer, laid out the way
// agentic CLIs like Claude Code do: a boxed header, "⏺" blocks with "⎿" details, a
// spinner while a model or the test command works, and a boxed summary at the end.

func (r *ConsoleReporter) st() style { return style{on: r.Pretty} }

var actionVerbs = map[domain.ActionType]string{
	domain.ActionResearch:  "Researching",
	domain.ActionAnalyze:   "Analyzing",
	domain.ActionPlan:      "Planning",
	domain.ActionImplement: "Implementing",
	domain.ActionTest:      "Running tests",
	domain.ActionReview:    "Reviewing",
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// bullet starts a block: "⏺ Title  meta".
func (r *ConsoleReporter) bullet(color func(string) string, title, meta string) {
	st := r.st()
	line := color("⏺") + " " + st.bold(title)
	if meta != "" {
		line += "  " + st.gray(meta)
	}
	fmt.Fprintln(r.Out, line)
}

// detail prints a "  ⎿ " line under a block.
func (r *ConsoleReporter) detail(text string) {
	fmt.Fprintf(r.Out, "  %s  %s\n", r.st().gray("⎿"), text)
}

// startSpin starts the spinner unless --verbose (where the provider's live output owns the
// terminal) or one is already running.
func (r *ConsoleReporter) startSpin(label, hint string) {
	if r.Verbose {
		return
	}
	if r.spin != nil {
		r.spin.SetLabel(label, hint)
		return
	}
	r.spin = startSpinner(r.Out, r.st(), label, hint)
}

func (r *ConsoleReporter) stopSpin() {
	if r.spin != nil {
		r.spin.Stop()
		r.spin = nil
	}
}

func (r *ConsoleReporter) prettyRunHeader(runID, dir string) {
	st := r.st()
	title := st.accent("✻") + " " + st.bold("AI Orchestrator")
	if r.Version != "" {
		title += " " + st.gray("v"+r.Version)
	}
	lines := []string{title, ""}
	if r.Cwd != "" {
		lines = append(lines, st.gray("repo  ")+shortenHome(r.Cwd))
	}
	lines = append(lines, st.gray("run   ")+runID, st.gray("dir   ")+shortenHome(dir))
	fmt.Fprint(r.Out, box(st, st.accent, lines, 44))
	// The sidebar opens under the header: from here on, context lives on the right.
	r.OpenDock()
	if r.dock != nil {
		r.refreshDock()
		return
	}
	if info := r.repo; info != nil {
		fmt.Fprintln(r.Out, r.repoLine(*info))
	}
}

func (r *ConsoleReporter) prettyProviderCallStarted(providerID, model string) {
	label := actionVerbs[r.action]
	if label == "" {
		label = "Working"
	}
	hint := providerID + " · " + model
	if r.Verbose {
		r.detail(r.st().gray(hint))
		return
	}
	r.startSpin(label, hint)
}

func (r *ConsoleReporter) prettyStepStarted(index int, action domain.ActionType) {
	st := r.st()
	meta := fmt.Sprintf("step %d", index+1)
	if action != domain.ActionTest {
		meta += " · " + string(domain.AuthorityFor(action)) + " access"
	}
	fmt.Fprintln(r.Out)
	r.bullet(st.accent, titleCase(string(action)), meta)
	r.stepUsage = domain.Usage{}
	r.inStep = true
	r.refreshDock()
	if action == domain.ActionTest {
		r.startSpin(actionVerbs[action], r.TestCommand)
	}
}

func (r *ConsoleReporter) prettyStepFinished(run *domain.Run, exec domain.StepExecution, outcome workflow.StepOutcome) {
	r.stopSpin()
	r.inStep = false
	r.run = run
	st := r.st()
	took := formatDuration(exec.FinishedAt.Sub(exec.StartedAt))
	who := ""
	if exec.Provider != nil {
		who = exec.Provider.Provider + " · " + exec.Provider.Model + " · "
	}
	switch outcome.Outcome {
	case workflow.OutcomeSuccess:
		switch exec.Action {
		case domain.ActionTest:
			cmd := ""
			if exec.TestResult != nil {
				cmd = exec.TestResult.Command + " · "
			}
			r.detail(st.green("✓ Tests passed") + " " + st.gray("("+cmd+took+")"))
		case domain.ActionReview:
			r.detail(st.green("✓ Review passed") + " " + st.gray("("+who+took+")"))
		default:
			r.detail(st.green("✓ Done") + " " + st.gray("("+who+took+")"))
		}
		r.stepUsageLine()
		r.printResult(exec, outcome)
		if r.stepDiff != "" {
			if card := r.renderDiff(r.stepDiff); card != "" {
				fmt.Fprintf(r.Out, "\n%s", card)
			}
			r.stepDiff = ""
		}
	case workflow.OutcomeFixable:
		// A passing review whose medium issues send the run back to implement once
		// (review.fix_medium).
		meta := took
		if counts := issueCounts(exec.ReviewResult); counts != "" {
			meta = counts + " · " + took
		}
		r.detail(st.green("✓ Review passed") + " " + st.gray("("+meta+")"))
		r.printResult(exec, outcome)
		r.prettyFixNotice(run)
	case workflow.OutcomeFailure:
		switch exec.Action {
		case domain.ActionTest:
			code := ""
			if exec.TestResult != nil {
				code = fmt.Sprintf("exit %d · ", exec.TestResult.ExitCode)
			}
			r.detail(st.red("✗ Tests failed") + " " + st.gray("("+code+took+")"))
			r.printResult(exec, outcome)
			r.prettyFixNotice(run)
		case domain.ActionReview:
			counts := issueCounts(exec.ReviewResult)
			meta := took
			if counts != "" {
				meta = counts + " · " + took
			}
			r.detail(st.red("✗ Review failed") + " " + st.gray("("+meta+")"))
			r.printResult(exec, outcome)
			r.prettyFixNotice(run)
		default:
			r.detail(st.red("✗ Failed") + " " + st.gray("("+took+")"))
		}
	}
	// Where the run is now: the sidebar, or one dim line where there is no room for it.
	if r.dock != nil {
		r.refreshDock()
	} else if s := r.compactStatus(run); s != "" {
		fmt.Fprintf(r.Out, "\n%s\n", s)
	}
}

// stepUsageLine prints what the step's model calls cost, when the provider reported it.
func (r *ConsoleReporter) stepUsageLine() {
	u := r.stepUsage
	if u.PromptTokens() == 0 && u.OutputTokens == 0 {
		return
	}
	fmt.Fprintf(r.Out, "     %s\n", r.usageLine(u))
}

func (r *ConsoleReporter) prettyFixNotice(run *domain.Run) {
	st := r.st()
	fmt.Fprintf(r.Out, "\n%s\n", st.yellow(fmt.Sprintf("↻ Returning to implement (fix %d/%d)", run.FixIterations+1, r.MaxFixIterations)))
}

func issueCounts(review *domain.ReviewResult) string {
	if review == nil {
		return ""
	}
	counts := map[domain.Severity]int{}
	for _, issue := range review.Issues {
		counts[issue.Severity]++
	}
	var parts []string
	for _, sev := range []domain.Severity{domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium, domain.SeverityLow} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	return strings.Join(parts, ", ")
}

func (r *ConsoleReporter) prettyRunFinished(run *domain.Run) {
	r.stopSpin()
	r.inStep = false
	r.run = run
	// The summary is the transcript's last word and must survive in the scrollback, so
	// the sidebar closes first and its cards are printed inline, side by side if they fit.
	r.CloseDock()
	st := r.st()
	var head string
	border := st.green
	switch run.Status {
	case domain.RunCompleted:
		head = st.green(st.bold("✓ Run completed"))
	case domain.RunFailed:
		head = st.red(st.bold("✗ Run failed"))
		border = st.red
	case domain.RunStopped:
		head = st.yellow(st.bold("■ Run stopped"))
		border = st.yellow
	default:
		return
	}
	lines := []string{head}
	if run.FailureReason != "" {
		lines = append(lines, st.gray("reason ")+firstLine(run.FailureReason))
	}
	if chain := executionChain(st, run.Executions); chain != "" {
		lines = append(lines, "", chain)
	}
	stats := fmt.Sprintf("%s · fixes %d", countOf(run.TotalSteps, "step"), run.FixIterations)
	if !run.CreatedAt.IsZero() {
		stats += " · " + formatElapsed(time.Since(run.CreatedAt))
	}
	lines = append(lines, st.gray(stats))
	fmt.Fprintln(r.Out)
	// The result box on the left, the usage and limits cards stacked on the right of it -
	// the same place the sidebar kept them during the run. A terminal too narrow for both
	// columns gets them stacked below instead.
	summary := strings.Split(strings.TrimRight(box(st, border, lines, 44), "\n"), "\n")
	var side []string
	for _, c := range [][]string{r.usageCard(0), r.limitsCard(0)} {
		side = append(side, c...)
	}
	fmt.Fprint(r.Out, besideRight(fullWidth(), summary, side))
}

// executionChain renders the steps that ran, in order, each marked by how it ended:
// "analyze ✓ → implement ✓ → test ✗ → implement ✓ → test ✓".
func executionChain(st style, execs []domain.StepExecution) string {
	parts := make([]string, 0, len(execs))
	for _, exec := range execs {
		ok, _ := executionResult(exec)
		mark := st.green("✓")
		if !ok {
			mark = st.red("✗")
		}
		parts = append(parts, string(exec.Action)+" "+mark)
	}
	return strings.Join(parts, st.gray(" → "))
}

func (r *ConsoleReporter) prettyDecided(t domain.DecisionTrace) {
	st := r.st()
	defer r.refreshDock()
	meta := ""
	if t.Source == domain.DecisionSourceManual {
		meta = "manual (--stage)"
	} else if t.Source == domain.DecisionSourceUser {
		meta = "chosen by you"
	} else {
		model := t.Model
		if model == "" {
			model = "?"
		}
		meta = t.Provider + " · " + model
	}
	r.bullet(st.blue, "Decision", meta)

	rows := [][2]string{}
	tr := t.Translation
	switch {
	case tr.Translated:
		rows = append(rows, [2]string{"language", fmt.Sprintf("%s → en %s", tr.Detected,
			st.gray(fmt.Sprintf("(%s/%s, %s)", tr.Provider, tr.Model, formatDuration(time.Duration(tr.LatencyMS)*time.Millisecond))))})
	case tr.Failed:
		rows = append(rows, [2]string{"language", st.yellow("translation failed: " + tr.Reason)})
	}
	if d := t.ContextDepth; d != nil {
		cmp := "<"
		if d.Probability >= d.Threshold {
			cmp = "≥"
		}
		v := fmt.Sprintf("%s %s", st.bold(string(d.Selected)), st.gray(fmt.Sprintf("p=%.2f %s %.2f", d.Probability, cmp, d.Threshold)))
		if t.ContextLimited {
			v += " " + st.yellow("limited: "+firstLine(t.ContextError))
		}
		rows = append(rows, [2]string{"context", v})
	}
	rows = append(rows, [2]string{"complexity", r.prettyComplexity(t)})

	names := make([]string, len(t.Workflow))
	for i, a := range t.Workflow {
		names[i] = st.cyan(string(a))
	}
	rows = append(rows, [2]string{"workflow", strings.Join(names, st.gray(" → "))})
	if t.Source == domain.DecisionSourceUser && t.DecisionError != "" {
		rows = append(rows, [2]string{"why", st.gray(firstLine(t.DecisionError))})
	}
	r.rows(rows)

	if len(t.Stages) > 0 && !r.ReviewForm {
		fmt.Fprintln(r.Out)
		for _, s := range t.Stages {
			r.stageRow(s)
		}
	}
}

// rows prints aligned "key  value" lines under a block, the first one hooked with "⎿".
func (r *ConsoleReporter) rows(rows [][2]string) {
	st := r.st()
	w := 0
	for _, row := range rows {
		w = max(w, len(row[0]))
	}
	for i, row := range rows {
		hook := " "
		if i == 0 {
			hook = st.gray("⎿")
		}
		fmt.Fprintf(r.Out, "  %s  %s  %s\n", hook, st.gray(fmt.Sprintf("%-*s", w, row[0])), row[1])
	}
}

func (r *ConsoleReporter) prettyComplexity(t domain.DecisionTrace) string {
	st := r.st()
	c := t.Complexity
	if t.Source == domain.DecisionSourceManual {
		return st.gray("—")
	}
	sel := st.bold(string(c.Selected))
	if t.Source == domain.DecisionSourceUser {
		return sel
	}
	parts := []string{sourceSummary(c.Primary, c.Sources[c.Primary], c.Selected)}
	for _, src := range []domain.ComplexitySource{domain.ComplexitySourceChoice, domain.ComplexitySourceScore} {
		out, ok := c.Sources[src]
		if src == c.Primary || !ok {
			continue
		}
		if out.Selected != c.Selected {
			parts = append(parts, st.yellow(fmt.Sprintf("%s → %s ✗", sourceSummary(src, out, out.Selected), out.Selected)))
		} else {
			parts = append(parts, fmt.Sprintf("%s ✓", sourceSummary(src, out, out.Selected)))
		}
	}
	return sel + " " + st.gray(strings.Join(parts, " · "))
}

const barWidth = 12

// stageRow renders one stage as a probability bar with its threshold marked:
// "     analysis     ████████░░│░  0.92  on".
func (r *ConsoleReporter) stageRow(s domain.StageOutcome) {
	st := r.st()
	name := fmt.Sprintf("%-12s", s.Stage)
	var bar, num string
	if s.Probability != nil {
		p := *s.Probability
		filled := int(p*barWidth + 0.5)
		th := -1
		if s.Threshold != nil {
			th = min(int(*s.Threshold*barWidth+0.5), barWidth-1)
		}
		var b strings.Builder
		for i := 0; i < barWidth; i++ {
			switch {
			case i == th:
				b.WriteString(st.gray("│"))
			case i < filled && s.Enabled:
				b.WriteString(st.cyan("█"))
			case i < filled:
				b.WriteString(st.gray("█"))
			default:
				b.WriteString(st.gray("░"))
			}
		}
		bar = b.String()
		num = fmt.Sprintf("%.2f", p)
	} else {
		bar = st.gray(strings.Repeat("·", barWidth))
		num = "  - "
	}
	state := st.gray("off")
	if s.Enabled {
		state = st.green("on")
		if s.Forced {
			state = st.green("on") + st.gray(" (forced)")
		}
		name = st.bold(name)
	} else {
		name = st.gray(name)
	}
	fmt.Fprintf(r.Out, "     %s %s  %s  %s\n", name, bar, st.gray(num), state)
}

func (r *ConsoleReporter) prettyAuthority(wf domain.Workflow, testCommand string, sandboxed bool) {
	st := r.st()
	var parts []string
	for _, s := range wf.Steps {
		if s.Action == domain.ActionTest {
			cmd := st.gray("$ " + testCommand + " (sandboxed)")
			if !sandboxed {
				cmd = st.gray("$ "+testCommand) + " " + st.yellow(notSandboxed)
			}
			parts = append(parts, "test "+cmd)
			continue
		}
		parts = append(parts, string(s.Action)+" "+st.gray(string(domain.AuthorityFor(s.Action))))
	}
	fmt.Fprintf(r.Out, "     %s %s\n", st.gray(fmt.Sprintf("%-12s", "access")), strings.Join(parts, st.gray(" · ")))
}
