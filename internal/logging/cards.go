package logging

import (
	"fmt"
	"strings"
	"time"

	"orchestrator/internal/domain"
)

// Cards for the dock: fixed width (so the sidebar is one straight column), title in the
// top border. They reuse the inline panels' content where it fits.

// card frames lines at exactly dockWidth cells: "╭─ Title ───╮ │ line │ ╰───╯".
func card(st style, title, footer string, lines []string) []string {
	inner := dockWidth - 4
	fit := func(l string) string {
		if visibleWidth(l) > inner {
			l = truncateVisible(ansiRE.ReplaceAllString(l, ""), inner)
		}
		return l + strings.Repeat(" ", inner-visibleWidth(l))
	}
	t := truncateVisible(ansiRE.ReplaceAllString(title, ""), inner-2)
	titleStyled := title
	if visibleWidth(title) > inner-2 {
		titleStyled = st.bold(t)
	}
	top := st.gray("╭─ ") + titleStyled + st.gray(" "+strings.Repeat("─", max(dockWidth-5-visibleWidth(titleStyled), 0))+"╮")
	out := []string{top}
	for _, l := range lines {
		out = append(out, st.gray("│")+" "+fit(l)+" "+st.gray("│"))
	}
	if footer == "" {
		out = append(out, st.gray("╰"+strings.Repeat("─", dockWidth-2)+"╯"))
	} else {
		f := truncateVisible(ansiRE.ReplaceAllString(footer, ""), inner-2)
		if visibleWidth(footer) <= inner-2 {
			f = footer
		}
		out = append(out, st.gray("╰"+strings.Repeat("─", max(dockWidth-5-visibleWidth(f), 0))+" ")+f+st.gray(" ─╯"))
	}
	return out
}

// workflowCard is the plan checklist with the step in flight marked by the spinner glyph.
func (r *ConsoleReporter) workflowCard(int) []string {
	run := r.run
	if run == nil || run.Workflow == nil || len(run.Workflow.Steps) == 0 {
		return nil
	}
	st := r.st()
	last := map[domain.ActionType]domain.StepExecution{}
	for _, e := range run.Executions {
		last[e.Action] = e
	}
	finished := run.Status == domain.RunCompleted || run.Status == domain.RunFailed || run.Status == domain.RunStopped
	done, current := 0, false
	var lines []string
	dots := ""
	for _, s := range run.Workflow.Steps {
		e, ran := last[s.Action]
		var mark, name, meta string
		switch {
		case s.Action == r.action && r.inStep && !finished:
			mark, name, meta = st.accent("✻"), st.bold(string(s.Action)), st.accent("running")
			dots += st.accent("●")
			current = true
		case ran:
			ok, result := executionResult(e)
			name, meta = string(s.Action), st.gray(result)
			if ok {
				mark, dots = st.green("✓"), dots+st.green("●")
				done++
			} else {
				mark, dots = st.red("✗"), dots+st.red("●")
			}
		case !current && !finished:
			mark, name, meta = st.accent("▸"), string(s.Action), st.gray("next")
			dots += st.gray("●")
			current = true
		default:
			mark, name, dots = st.gray("○"), st.gray(string(s.Action)), dots+st.gray("●")
		}
		line := mark + " " + name
		if meta != "" {
			line += strings.Repeat(" ", max(11-visibleWidth(name), 1)) + meta
		}
		lines = append(lines, line)
	}
	if run.FixIterations > 0 {
		lines = append(lines, st.yellow(fmt.Sprintf("↻ fixes %d/%d", run.FixIterations, r.MaxFixIterations)))
	}
	footer := ""
	if !run.CreatedAt.IsZero() {
		footer = st.gray(formatElapsed(time.Since(run.CreatedAt)))
	}
	return card(st, st.bold(fmt.Sprintf("Workflow %d/%d", done, len(run.Workflow.Steps)))+" "+dots, footer, lines)
}

func (r *ConsoleReporter) usageCard(int) []string {
	if !r.hasUsage {
		return nil
	}
	st := r.st()
	u := r.runUsage
	lines := []string{
		fmt.Sprintf("%s in · %s out", tokens(u.PromptTokens()), tokens(u.OutputTokens)),
	}
	if p := u.PromptTokens(); p > 0 {
		lines = append(lines, st.gray("cache ")+pctColor(st, float64(u.CacheReadTokens)/float64(p), true)+
			st.gray(fmt.Sprintf(" · %d turns", u.Turns)))
	}
	footer := ""
	if u.CostUSD > 0 {
		footer = st.green(fmt.Sprintf("$%.3f", u.CostUSD))
	}
	return card(st, st.bold("Usage"), footer, lines)
}

func (r *ConsoleReporter) limitsCard(int) []string {
	l := r.runUsage.Limits
	if l == nil || (l.FiveHour == nil && l.Weekly == nil) {
		return nil
	}
	st := r.st()
	var lines []string
	row := func(name string, w *domain.LimitWindow) {
		if w == nil {
			return
		}
		reset := ""
		if !w.ResetsAt.IsZero() {
			reset = st.gray(" " + until(time.Until(w.ResetsAt)))
		}
		lines = append(lines, fmt.Sprintf("%-6s %s %s%s", name, meter(st, w.Utilization, 10), pctColor(st, 1-w.Utilization, true), reset))
	}
	row("5-hour", l.FiveHour)
	row("Weekly", l.Weekly)
	return card(st, st.bold("Limits")+st.gray(" · left"), "", lines)
}

func (r *ConsoleReporter) repoCard(int) []string {
	info := r.repo
	if info == nil {
		return nil
	}
	st := r.st()
	var lines []string
	for _, c := range info.Commits {
		when := ""
		if !c.When.IsZero() {
			when = ago(time.Since(c.When))
		}
		subjW := dockWidth - 4 - 8 - 1 - len(when) - 1
		subj := truncateVisible(c.Subject, subjW)
		lines = append(lines, st.yellow(c.Hash)+" "+subj+strings.Repeat(" ", max(subjW-visibleWidth(subj), 0))+" "+st.gray(when))
	}
	if info.MoreCommits > 0 {
		lines = append(lines, st.gray(fmt.Sprintf("+%d more", info.MoreCommits)))
	}
	footer := st.green("clean")
	if info.Changed > 0 {
		footer = st.yellow(countOf(info.Changed, "change"))
	}
	title := st.bold("Repo")
	if info.Branch != "" {
		title += st.gray(" · ") + st.cyan(info.Branch)
	}
	return card(st, title, footer, lines)
}

// compactStatus is the one-line summary used where the sidebar does not fit: printed
// between steps instead of the inline workflow card. "▸ test · 1/2 · 18k tok · $0.02 · 5h 98%"
func (r *ConsoleReporter) compactStatus(run *domain.Run) string {
	if run == nil || run.Workflow == nil {
		return ""
	}
	st := r.st()
	done := 0
	for _, e := range run.Executions {
		if ok, _ := executionResult(e); ok {
			done++
		}
	}
	next := ""
	for _, s := range run.Workflow.Steps {
		ran := false
		for _, e := range run.Executions {
			if e.Action == s.Action {
				ran = true
			}
		}
		if !ran {
			next = string(s.Action)
			break
		}
	}
	parts := []string{st.gray(fmt.Sprintf("%d/%d steps", min(done, len(run.Workflow.Steps)), len(run.Workflow.Steps)))}
	if next != "" {
		parts = append([]string{st.accent("▸ ") + next}, parts...)
	} else {
		return "" // nothing left: the run summary follows and says it all
	}
	if r.hasUsage {
		u := r.runUsage
		parts = append(parts, st.gray(tokens(u.PromptTokens()+u.OutputTokens)+" tok"))
		if u.CostUSD > 0 {
			parts = append(parts, st.green(fmt.Sprintf("$%.3f", u.CostUSD)))
		}
		if l := u.Limits; l != nil && l.FiveHour != nil {
			parts = append(parts, st.gray("5h ")+pctColor(st, 1-l.FiveHour.Utilization, true))
		}
	}
	return "  " + strings.Join(parts, st.gray(" · "))
}
