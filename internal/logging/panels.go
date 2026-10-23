package logging

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"orchestrator/internal/domain"
)

// Panels: titled, bordered side cards in the pretty renderer (repository state, usage,
// rate limits), the way agent UIs show context next to the transcript. They are printed
// at moments where the information is fresh - the repository card under the header, the
// usage and limits cards after every model step and in the final summary.

// RepoInfo is what the repository card shows; app fills it from git.
type RepoInfo struct {
	Branch  string
	Commits []CommitInfo
	// MoreCommits is how many older commits exist beyond Commits (0 = unknown or none).
	MoreCommits int
	Changed     int
}

type CommitInfo struct {
	Hash    string
	Subject string
	When    time.Time
}

// panel draws a card: "╭ Title ─────╮" with the title in the top border, an optional
// right-aligned footer in the bottom border.
func panel(st style, title, footer string, lines []string) string {
	inner := visibleWidth(title) + 4
	for _, l := range lines {
		inner = max(inner, visibleWidth(l))
	}
	inner = min(max(inner, visibleWidth(footer)+4), termWidth()-6)
	var b strings.Builder
	top := "╭─ " + title + " "
	b.WriteString("  " + st.gray("╭─ ") + st.bold(st.gray(title)) + st.gray(" "+strings.Repeat("─", max(inner+2-visibleWidth(top)+1, 0))+"╮") + "\n")
	for _, l := range lines {
		if visibleWidth(l) > inner {
			l = truncateVisible(ansiRE.ReplaceAllString(l, ""), inner)
		}
		b.WriteString("  " + st.gray("│") + " " + l + strings.Repeat(" ", inner-visibleWidth(l)) + " " + st.gray("│") + "\n")
	}
	if footer == "" {
		b.WriteString("  " + st.gray("╰"+strings.Repeat("─", inner+2)+"╯") + "\n")
	} else {
		dashes := max(inner+2-visibleWidth(footer)-3, 0)
		b.WriteString("  " + st.gray("╰"+strings.Repeat("─", dashes)+" ") + footer + st.gray(" ─╯") + "\n")
	}
	return b.String()
}

// SetRepoInfo records the repository card's content; RunHeader prints it.
func (r *ConsoleReporter) SetRepoInfo(info RepoInfo) {
	r.repo = &info
}

func (r *ConsoleReporter) repoPanel() string {
	info := r.repo
	if info == nil {
		return ""
	}
	st := r.st()
	title := "Repo"
	if info.Branch != "" {
		title = "Repo · " + info.Branch
	}
	var lines []string
	subjW := 40
	for _, c := range info.Commits {
		subj := truncateVisible(c.Subject, subjW)
		when := ""
		if !c.When.IsZero() {
			when = st.gray(ago(time.Since(c.When)))
		}
		lines = append(lines, fmt.Sprintf("%s %-*s %s", st.yellow(c.Hash), subjW, subj, when))
	}
	if info.MoreCommits > 0 {
		lines = append(lines, st.gray(fmt.Sprintf("+%d more", info.MoreCommits)))
	}
	if len(lines) == 0 {
		lines = append(lines, st.gray("no commits yet"))
	}
	footer := st.green("clean")
	if info.Changed > 0 {
		footer = st.yellow(countOf(info.Changed, "uncommitted change"))
	}
	return panel(st, title, footer, lines)
}

// ProviderActivity implements provider.ActivityObserver: the spinner names what the model
// is doing right now ("Read src/app.ts"). In --verbose the stream writer prints it.
func (r *ConsoleReporter) ProviderActivity(what string) {
	if !r.Pretty || r.spin == nil {
		return
	}
	r.spin.SetDetail(what)
}

// ProviderUsage implements provider.UsageObserver: it accumulates the call into the step
// and the run totals.
func (r *ConsoleReporter) ProviderUsage(u domain.Usage) {
	r.stepUsage.Add(u)
	r.runUsage.Add(u)
	r.hasUsage = true
	r.refreshDock()
}

// CallReporter forwards both to its ConsoleReporter.
func (c CallReporter) ProviderActivity(what string) { c.ConsoleReporter.ProviderActivity(what) }
func (c CallReporter) ProviderUsage(u domain.Usage) { c.ConsoleReporter.ProviderUsage(u) }

// usageLine is "12.4k in · 820 out · cache 91% · $0.042".
func (r *ConsoleReporter) usageLine(u domain.Usage) string {
	st := r.st()
	parts := []string{
		tokens(u.PromptTokens()) + st.gray(" in"),
		tokens(u.OutputTokens+u.ReasoningTokens) + st.gray(" out"),
	}
	if u.ReasoningTokens > 0 {
		parts = append(parts, tokens(u.ReasoningTokens)+st.gray(" reasoning"))
	}
	if p := u.PromptTokens(); p > 0 {
		hit := float64(u.CacheReadTokens) / float64(p)
		parts = append(parts, st.gray("cache ")+pctColor(st, hit, true))
	}
	if u.CostUSD > 0 {
		parts = append(parts, st.green(fmt.Sprintf("$%.3f", u.CostUSD)))
	}
	return strings.Join(parts, st.gray(" · "))
}

func (r *ConsoleReporter) limitsPanel(l *domain.RateLimits) string {
	if l == nil || (l.FiveHour == nil && l.Weekly == nil) {
		return ""
	}
	st := r.st()
	var lines []string
	row := func(name string, w *domain.LimitWindow) {
		if w == nil {
			return
		}
		left := 1 - w.Utilization
		reset := ""
		if !w.ResetsAt.IsZero() {
			reset = st.gray(" · " + until(time.Until(w.ResetsAt)))
		}
		lines = append(lines, fmt.Sprintf("%-7s %s %s%s", name, meter(st, w.Utilization, 16),
			pctColor(st, left, true)+st.gray(" left"), reset))
	}
	row("5-hour", l.FiveHour)
	row("Weekly", l.Weekly)
	return panel(st, "Anthropic limits", "", lines)
}

// usagePanel is the final summary's card: the whole run's tokens, cache and cost.
func (r *ConsoleReporter) usagePanel() string {
	if !r.hasUsage {
		return ""
	}
	st := r.st()
	u := r.runUsage
	lines := []string{r.usageLine(u)}
	if u.Turns > 0 {
		lines = append(lines, st.gray(fmt.Sprintf("%d model turns", u.Turns)))
	}
	return panel(st, "Usage", "", lines)
}

// meter is a "▰▰▰▱▱▱" bar of used capacity.
func meter(st style, used float64, width int) string {
	used = math.Max(0, math.Min(1, used))
	n := int(math.Round(used * float64(width)))
	color := st.green
	switch {
	case used >= 0.9:
		color = st.red
	case used >= 0.7:
		color = st.yellow
	}
	return color(strings.Repeat("▰", n)) + st.gray(strings.Repeat("▱", width-n))
}

// pctColor renders a fraction as a percentage, green when good (high if highGood).
func pctColor(st style, f float64, highGood bool) string {
	s := fmt.Sprintf("%.0f%%", f*100)
	good := f
	if !highGood {
		good = 1 - f
	}
	switch {
	case good >= 0.5:
		return st.bold(st.green(s))
	case good >= 0.2:
		return st.bold(st.yellow(s))
	default:
		return st.bold(st.red(s))
	}
}

func tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// ago is "5m", "6h", "3d".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
}

// until is "4h 12m", "4d 5h", "12m".
func until(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	h := int(d.Hours())
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	case h >= 1:
		return fmt.Sprintf("%dh %dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// printPanels writes each non-empty panel, separated by nothing (they stack).
func printPanels(w io.Writer, panels ...string) {
	for _, p := range panels {
		if p != "" {
			fmt.Fprint(w, p)
		}
	}
}

// sideBySide lays cards out in a row when they fit in width, else stacked; every line
// is indented by two columns.
func sideBySide(width int, cards ...[]string) string {
	var cs [][]string
	total := 0
	for _, c := range cards {
		if len(c) > 0 {
			cs = append(cs, c)
			total += dockWidth + 1
		}
	}
	var b strings.Builder
	if len(cs) == 0 {
		return ""
	}
	if total+2 > width {
		for _, c := range cs {
			for _, l := range c {
				b.WriteString("  " + l + "\n")
			}
		}
		return b.String()
	}
	rows := 0
	for _, c := range cs {
		rows = max(rows, len(c))
	}
	for i := 0; i < rows; i++ {
		line := " "
		for _, c := range cs {
			cell := strings.Repeat(" ", dockWidth)
			if i < len(c) {
				cell = c[i]
			}
			line += " " + cell
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// besideRight prints left as is and right aligned to the terminal's right edge on the
// same rows, like the sidebar during the run. When both do not fit side by side, right
// follows left, indented.
func besideRight(width int, left, right []string) string {
	lw := 0
	for _, l := range left {
		lw = max(lw, visibleWidth(l))
	}
	rw := 0
	for _, l := range right {
		rw = max(rw, visibleWidth(l))
	}
	var b strings.Builder
	if len(right) == 0 || lw+dockGap+rw > width-1 {
		for _, l := range left {
			b.WriteString(l + "\n")
		}
		for _, l := range right {
			b.WriteString("  " + l + "\n")
		}
		return b.String()
	}
	col := width - rw // right column starts here (0-based), flush with the right edge
	for i := 0; i < max(len(left), len(right)); i++ {
		l := ""
		if i < len(left) {
			l = left[i]
		}
		if i >= len(right) {
			b.WriteString(l + "\n")
			continue
		}
		b.WriteString(l + strings.Repeat(" ", max(col-visibleWidth(l), dockGap)) + right[i] + "\n")
	}
	return b.String()
}

// repoLine is the repository card squeezed into one dim line for narrow terminals:
// "  master · 2a1b3c4 Fix login · clean".
func (r *ConsoleReporter) repoLine(info RepoInfo) string {
	st := r.st()
	var parts []string
	if info.Branch != "" {
		parts = append(parts, st.cyan(info.Branch))
	}
	if len(info.Commits) > 0 {
		c := info.Commits[0]
		parts = append(parts, st.yellow(c.Hash)+" "+st.gray(truncateVisible(c.Subject, 40)))
	}
	if info.Changed > 0 {
		parts = append(parts, st.yellow(countOf(info.Changed, "uncommitted change")))
	} else {
		parts = append(parts, st.green("clean"))
	}
	return "  " + strings.Join(parts, st.gray(" · "))
}

// workflowPanel is the plan as a checklist, like an agent's todo list: what ran (✓/✗),
// what runs next (▸) and what is still to come (○), with a dot per step in the title.
func (r *ConsoleReporter) workflowPanel(run *domain.Run) string {
	if run == nil || run.Workflow == nil || len(run.Workflow.Steps) == 0 {
		return ""
	}
	st := r.st()
	last := map[domain.ActionType]domain.StepExecution{}
	for _, e := range run.Executions {
		last[e.Action] = e
	}
	done, dots := 0, ""
	var lines []string
	nextMarked := run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunStopped
	for _, s := range run.Workflow.Steps {
		e, ran := last[s.Action]
		var mark, name, meta string
		switch {
		case ran:
			ok, result := executionResult(e)
			if e.Provider != nil {
				result = e.Provider.Model + " · " + result
			}
			name, meta = string(s.Action), st.gray(result)
			if ok {
				mark = st.green("✓")
				dots += st.green("●")
				done++
			} else {
				mark = st.red("✗")
				dots += st.red("●")
			}
		case !nextMarked:
			mark, name, meta = st.accent("▸"), st.bold(string(s.Action)), st.accent("next")
			dots += st.accent("●")
			nextMarked = true
		default:
			mark, name = st.gray("○"), st.gray(string(s.Action))
			dots += st.gray("●")
		}
		line := mark + " " + name
		if meta != "" {
			line += strings.Repeat(" ", max(11-visibleWidth(name), 1)) + meta
		}
		lines = append(lines, line)
	}
	title := fmt.Sprintf("Workflow %d/%d  ", done, len(run.Workflow.Steps)) + dots
	return panel(st, title, "", lines)
}
