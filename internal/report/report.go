package report

import (
	"fmt"
	"slices"
	"strings"

	"orchestrator/internal/domain"
)

// maxReportBytes bounds report.md (specs/delivery/SPEC.md §5, A8): GitLab's note limit is
// 1 MiB, and a merge request description should be read.
const maxReportBytes = 60_000

// reportLimits are how much of each list report.md shows and how many lines of the task it
// quotes.
type reportLimits struct {
	files, issues, findings, failedTests, rules, taskLines int
}

// reportCuts are the limits RunReport tries in turn until the report fits: the lists shrink
// first, then the task quote (A8: 30 task lines, 50 files, 30 issues).
var reportCuts = []reportLimits{
	{files: 50, issues: 30, findings: 20, failedTests: 10, rules: 20, taskLines: 30},
	{files: 10, issues: 10, findings: 10, failedTests: 5, rules: 10, taskLines: 30},
	{files: 0, issues: 0, findings: 3, failedTests: 0, rules: 3, taskLines: 30},
	{files: 0, issues: 0, findings: 3, failedTests: 0, rules: 3, taskLines: 10},
	{files: 0, issues: 0, findings: 3, failedTests: 0, rules: 3, taskLines: 3},
	{files: 0, issues: 0, findings: 3, failedTests: 0, rules: 3, taskLines: 0},
}

// maxQuotedLine bounds one quoted line of the task, in runes.
const maxQuotedLine = 1000

// RunReport renders report.md: one run's report for the reviewer of its change - what the
// task was, why these stages, what changed, whether the tests and the review passed and what
// needs a person's eye (specs/delivery/SPEC.md §5). It reads only the run's own record and
// decision.json (trace, nil when there is none) and makes no call; the same record always
// renders the same text. Everything the task, the ticket or a model wrote is escaped
// (mdText), so the text can go into a merge request as it is. The result is at most
// maxReportBytes: lists are cut first, then the task quote.
func RunReport(run domain.Run, trace *domain.DecisionTrace) string {
	out := domain.ComputeOutcome(run, trace)
	if run.Outcome != nil {
		out = *run.Outcome
	}
	var text string
	for _, lim := range reportCuts {
		if text = renderReport(run, trace, out, lim); len(text) <= maxReportBytes {
			return text
		}
	}
	return cutAtLine(text, maxReportBytes)
}

// RunReportFit is RunReport for a text that must also satisfy fits, such as a merge request
// description that has to fit one push option once its line breaks are encoded (specs/delivery/
// SPEC.md §7.3). The report is rendered again with the next smaller lists, then a shorter task
// quote (the same cut order as RunReport), until the text fits; when even the smallest does not,
// it is cut at a line with a closing mark. report.md on disk stays RunReport's version.
func RunReportFit(run domain.Run, trace *domain.DecisionTrace, fits func(string) bool) string {
	out := domain.ComputeOutcome(run, trace)
	if run.Outcome != nil {
		out = *run.Outcome
	}
	var text string
	for _, lim := range reportCuts {
		if text = renderReport(run, trace, out, lim); len(text) <= maxReportBytes && fits(text) {
			return text
		}
	}
	const mark = "\n…(cut)\n"
	for max := min(len(text), maxReportBytes); max > len(mark); max -= max/10 + 1 {
		if cut := cutAtLine(text, max); fits(cut) {
			return cut
		}
	}
	return strings.TrimSpace(mark)
}

// cutAtLine is s cut at a line boundary to fit max bytes with a closing mark.
func cutAtLine(s string, max int) string {
	const mark = "\n…(cut)\n"
	s = s[:max-len(mark)]
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s + mark
}

func renderReport(run domain.Run, trace *domain.DecisionTrace, out domain.Outcome, lim reportLimits) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", mdText(reportTitle(run)))
	b.WriteString(statusLine(run, out) + "\n")
	writeTaskSection(&b, run, lim)
	writeStagesSection(&b, run, trace)
	writeChangedSection(&b, run, lim)
	writeTestsSection(&b, run, lim)
	writeReviewSection(&b, run, lim)
	writeAttentionSection(&b, run, lim)
	writeFooter(&b, run, out)
	return b.String()
}

// reportTitle is the task's first line, led by the Jira key when the line does not name it.
func reportTitle(run domain.Run) string {
	title := cutRunes(oneLine(firstLine(strings.TrimSpace(run.Task.Description))), 120)
	if title == "" {
		return "Run " + run.ID
	}
	if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceJira && src.Key != "" && !strings.Contains(title, src.Key) {
		title = src.Key + ": " + title
	}
	return title
}

// lastExecution is the last execution of action, nil without one.
func lastExecution(run domain.Run, action domain.ActionType) *domain.StepExecution {
	for i := len(run.Executions) - 1; i >= 0; i-- {
		if run.Executions[i].Action == action {
			return &run.Executions[i]
		}
	}
	return nil
}

func statusLine(run domain.Run, out domain.Outcome) string {
	status := string(out.Status)
	if out.FailureReason != "" {
		status += ": " + failureLine(out.FailureReason)
	}
	parts := []string{"**Status:** " + mdText(status)}
	if out.ReviewVerdict != "" {
		review := string(out.ReviewVerdict)
		if e := lastExecution(run, domain.ActionReview); e != nil && e.ReviewResult != nil {
			if counts := severityCounts(e.ReviewResult.Issues); counts != "" {
				review += " (" + counts + ")"
			}
		}
		parts = append(parts, "**Review:** "+review)
	}
	if p := out.TestsPassedFinal; p != nil {
		parts = append(parts, "**Tests:** "+map[bool]string{true: "passed", false: "failed"}[*p])
	}
	if e := lastExecution(run, domain.ActionTest); e != nil && e.TestResult != nil && len(e.TestResult.Checks) > 0 {
		var checks []string
		for _, c := range e.TestResult.Checks {
			checks = append(checks, mdText(c.Name)+" "+map[bool]string{true: "✓", false: "✗"}[c.Success])
		}
		parts = append(parts, "**Checks:** "+strings.Join(checks, " "))
	}
	return strings.Join(parts, " · ")
}

var severityOrder = []domain.Severity{domain.SeverityCritical, domain.SeverityHigh, domain.SeverityMedium, domain.SeverityLow}

// severityCounts is "1 high, 2 low", most severe first; "" for no issue.
func severityCounts(issues []domain.ReviewIssue) string {
	var parts []string
	for _, sev := range severityOrder {
		n := 0
		for _, i := range issues {
			if i.Severity == sev {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	return strings.Join(parts, ", ")
}

func writeTaskSection(b *strings.Builder, run domain.Run, lim reportLimits) {
	b.WriteString("\n### Task\n\n")
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(run.Task.Description, "\r", "")), "\n")
	shown := min(len(lines), lim.taskLines)
	for _, l := range lines[:shown] {
		b.WriteString(strings.TrimRight("> "+mdText(cutRunes(strings.TrimRight(l, " \t"), maxQuotedLine)), " ") + "\n")
	}
	switch {
	case lim.taskLines == 0 && len(lines) > 0 && strings.Join(lines, "") != "":
		b.WriteString("> …(task text left out: the report would be too long)\n")
	case shown < len(lines):
		fmt.Fprintf(b, "> …(%s)\n", plural(len(lines)-shown, "more line"))
	}
	if n := len(run.Task.Acceptance); n > 0 {
		b.WriteString("\nAcceptance criteria:\n")
		for i, c := range run.Task.Acceptance {
			if i == 10 {
				fmt.Fprintf(b, "- and %d more\n", n-i)
				break
			}
			b.WriteString("- " + mdText(cutRunes(oneLine(c), 300)) + "\n")
		}
	}
	if src := sourceLine(run.Task.Source); src != "" {
		b.WriteString("\nSource: " + src + "\n")
	}
}

// sourceLine says where the task came from; "" for a task typed on the command line.
func sourceLine(src *domain.TaskSource) string {
	if src == nil {
		return ""
	}
	if src.Kind == domain.TaskSourceStory {
		s := fmt.Sprintf("story %s (%s)", src.StoryRef(), src.StoryDetail())
		if src.Branch != "" {
			s += " on " + src.Branch
		}
		return mdText(s)
	}
	if src.URL != "" && linkable(src.URL) {
		return fmt.Sprintf("[%s](%s)", mdText(src.Key), src.URL)
	}
	if src.URL != "" {
		return mdCode(src.URL)
	}
	return mdText(src.Kind + " " + src.Key)
}

// linkable reports whether url is a plain http(s) address that cannot end a Markdown link
// early or carry markup.
func linkable(url string) bool {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") || len(url) > 500 {
		return false
	}
	return !strings.ContainsFunc(url, func(r rune) bool {
		return r <= ' ' || r >= 0x7f || strings.ContainsRune("()<>[]\\`\"", r)
	})
}

func writeStagesSection(b *strings.Builder, run domain.Run, trace *domain.DecisionTrace) {
	d := run.Decision
	if d == nil {
		return
	}
	b.WriteString("\n### Why these stages\n\n")
	b.WriteString(decisionSummary(*d, trace) + "\n")
	if len(d.Stages.Stages) == 0 {
		return
	}
	b.WriteString("\n| stage | on | why |\n|---|---|---|\n")
	for _, st := range d.Stages.Stages {
		on := "—"
		switch {
		case st.Enabled && st.Explored:
			on = "✓ (explored)"
		case st.Enabled:
			on = "✓"
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", mdCell(string(st.Stage)), on, mdCell(st.Reason))
	}
}

// decisionSummary is the complexity, its size and cuts, and who decided.
func decisionSummary(d domain.Decision, trace *domain.DecisionTrace) string {
	if d.Source == domain.DecisionSourceManual {
		return "stages chosen by the operator (`run --stage`)"
	}
	var parts []string
	if c := d.Complexity; c.Selected != "" {
		s := "complexity **" + mdText(string(c.Selected)) + "**"
		var detail []string
		if src, ok := c.Sources[c.Primary]; ok {
			sz := src.Decision.Size()
			if src.Size != nil {
				sz = *src.Size
			}
			if len(src.Decision.Probabilities) > 0 || src.Size != nil {
				detail = append(detail, fmt.Sprintf("size %.2f", sz))
			}
		}
		if c.Cuts != nil {
			detail = append(detail, fmt.Sprintf("cuts %.2f / %.2f", c.Cuts.Medium, c.Cuts.Complex))
		}
		if len(detail) > 0 {
			s += " (" + strings.Join(detail, "; ") + ")"
		}
		parts = append(parts, s)
	}
	by := "decided by " + mdText(string(d.Source))
	if trace != nil {
		if m := firstNonEmpty(trace.Model, trace.Provider); m != "" && d.Source != domain.DecisionSourceUser {
			by += " (" + mdText(m) + ")"
		}
	}
	parts = append(parts, by)
	if d.Edit != nil {
		parts = append(parts, "edited by the operator")
	}
	return strings.Join(parts, " · ")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func writeChangedSection(b *strings.Builder, run domain.Run, lim reportLimits) {
	var diff *domain.StepDiff
	for i := range run.Executions {
		if d := run.Executions[i].Diff; d != nil && d.Run != nil {
			diff = d
		}
	}
	if diff == nil {
		return
	}
	b.WriteString("\n### What changed\n\n")
	line := diffLine(*diff.Run)
	if base := changeBase(run); base != "" {
		line += " against " + mdCode(base)
	}
	b.WriteString(line + "\n")
	if len(diff.RunFiles) == 0 {
		return
	}
	shown := min(len(diff.RunFiles), lim.files)
	if shown == 0 {
		b.WriteString("\n(the file list is left out: the report would be too long)\n")
		return
	}
	b.WriteString("\n| file | + | − |\n|---|---|---|\n")
	for _, f := range diff.RunFiles[:shown] {
		if f.Binary {
			fmt.Fprintf(b, "| %s | binary | |\n", mdCode(f.Path))
			continue
		}
		fmt.Fprintf(b, "| %s | %d | %d |\n", mdCode(f.Path), f.Insertions, f.Deletions)
	}
	if more := diff.Run.Files - shown; more > 0 {
		fmt.Fprintf(b, "\nand %d more\n", more)
	}
}

// changeBase is what the run's change is counted against: the base of its worktree, else its
// restore point.
func changeBase(run domain.Run) string {
	if w := run.Worktree; w != nil && w.BaseRef != "" {
		if w.BaseBranch != "" {
			return w.BaseBranch + "@" + shortCommit(w.BaseRef)
		}
		return shortCommit(w.BaseRef)
	}
	if run.PreRunRef != "" {
		return "the restore point " + shortCommit(run.PreRunRef)
	}
	return ""
}

func writeTestsSection(b *strings.Builder, run domain.Run, lim reportLimits) {
	last := lastExecution(run, domain.ActionTest)
	if last == nil {
		return
	}
	b.WriteString("\n### Tests and checks\n\n")
	b.WriteString("last test step: " + testLine(*last) + "\n")
	if t := last.TestResult; t != nil && !t.Success && len(t.FailedTests) > 0 && lim.failedTests > 0 {
		b.WriteString("\nFailing tests: " + nameList(t.FailedTests, lim.failedTests) + "\n")
	}
	if run.FixIterations > 0 {
		line := fmt.Sprintf("\nFix iterations: %d", run.FixIterations)
		if first := firstExecution(run, domain.ActionTest); first != nil && first.TestResult != nil && !first.TestResult.Success {
			line += " (the first test step failed: " + firstFailure(*first.TestResult) + ")"
		}
		b.WriteString(line + "\n")
	}
}

func firstExecution(run domain.Run, action domain.ActionType) *domain.StepExecution {
	for i := range run.Executions {
		if run.Executions[i].Action == action {
			return &run.Executions[i]
		}
	}
	return nil
}

// testLine is how one test step came out, with its command and checks.
func testLine(e domain.StepExecution) string {
	t := e.TestResult
	if t == nil {
		return mdText(string(e.Status))
	}
	cmd := ""
	if t.Command != "" {
		cmd = mdCode(t.Command)
	}
	var s string
	switch {
	case t.Success:
		s = "passed"
		if cmd != "" {
			s += " (" + cmd + ")"
		}
		if len(t.Checks) > 0 {
			names := make([]string, len(t.Checks))
			for i, c := range t.Checks {
				names[i] = mdText(c.Name)
			}
			s += ", checks " + strings.Join(names, ", ") + " passed"
		}
	case t.FailedCheck != "":
		s = fmt.Sprintf("failed at check %s (exit %d)", mdCode(t.FailedCheck), t.ExitCode)
		if cmd != "" {
			s = fmt.Sprintf("failed at check %s (%s, exit %d)", mdCode(t.FailedCheck), cmd, t.ExitCode)
		}
		if passed := passedChecks(t.Checks); len(passed) > 0 {
			s += ", checks " + strings.Join(passed, ", ") + " passed"
		}
	default:
		s = fmt.Sprintf("failed (exit %d)", t.ExitCode)
		if cmd != "" {
			s = fmt.Sprintf("failed (%s, exit %d)", cmd, t.ExitCode)
		}
	}
	return s
}

func passedChecks(checks []domain.CheckResult) []string {
	var names []string
	for _, c := range checks {
		if c.Success {
			names = append(names, mdText(c.Name))
		}
	}
	return names
}

// firstFailure is what the first failed test step failed on: its failing tests, else its check.
func firstFailure(t domain.TestResult) string {
	if len(t.FailedTests) > 0 {
		return nameList(t.FailedTests, 3)
	}
	if t.FailedCheck != "" {
		return "check " + mdCode(t.FailedCheck)
	}
	return fmt.Sprintf("exit %d", t.ExitCode)
}

// nameList is at most max names as code, then "and N more".
func nameList(names []string, max int) string {
	shown := min(len(names), max)
	out := make([]string, shown)
	for i, n := range names[:shown] {
		out[i] = mdCode(cutRunes(n, 200))
	}
	s := strings.Join(out, ", ")
	if more := len(names) - shown; more > 0 {
		s += fmt.Sprintf(" and %d more", more)
	}
	return s
}

func writeReviewSection(b *strings.Builder, run domain.Run, lim reportLimits) {
	e := lastExecution(run, domain.ActionReview)
	if e == nil || e.ReviewResult == nil {
		return
	}
	r := *e.ReviewResult
	b.WriteString("\n### Review\n\n")
	line := "verdict **" + string(r.Decide()) + "**"
	if n := len(r.Criteria); n > 0 {
		line += fmt.Sprintf(" · criteria %d/%d met", n-len(r.UnmetCriteria()), n)
	}
	if names := reviewerNames(run); names != "" {
		line += " · reviewers " + mdText(names)
	}
	b.WriteString(line + "\n")

	issues := slices.Clone(r.Issues)
	slices.SortStableFunc(issues, func(a, b domain.ReviewIssue) int {
		return slices.Index(severityOrder, a.Severity) - slices.Index(severityOrder, b.Severity)
	})
	if lim.issues == 0 && len(issues) > 0 {
		fmt.Fprintf(b, "\n(%s: the list is left out, the report would be too long)\n", plural(len(issues), "issue"))
		return
	}
	var rows []string
	for _, c := range r.UnmetCriteria() {
		rows = append(rows, fmt.Sprintf("- criterion %d not met · %s · %s", c.Index,
			mdText(cutRunes(oneLine(c.Criterion), 300)), mdText(cutRunes(oneLine(c.Evidence), 300))))
	}
	for _, i := range issues {
		row := "- " + mdText(string(i.Severity))
		if i.File != "" {
			row += " · " + mdCode(cutRunes(i.File, 200))
		}
		rows = append(rows, row+" · "+mdText(cutRunes(oneLine(i.Description), 500)))
	}
	if len(rows) > 0 {
		b.WriteString("\n")
	}
	for i, row := range rows {
		if i == lim.issues {
			fmt.Fprintf(b, "- and %d more\n", len(rows)-i)
			break
		}
		b.WriteString(row + "\n")
	}
}

func writeAttentionSection(b *strings.Builder, run domain.Run, lim reportLimits) {
	var items []string
	rules := domain.LastImplementPathRules(run)
	for _, m := range domain.ManualApprovalMatches(rules) {
		items = append(items, fmt.Sprintf("policy.paths requires manual approval: %s (%s)", mdCode(m.Match), plural(len(m.Files), "file")))
	}
	for _, m := range rules {
		if len(m.Missing) > 0 {
			items = append(items, fmt.Sprintf("policy.paths %s requires %s, which the workflow does not run", mdCode(m.Match), stageNames(m.Missing)))
		}
	}
	if len(items) > lim.rules {
		items = append(items[:lim.rules], fmt.Sprintf("and %d more path rules", len(items)-lim.rules))
	}
	for i, f := range run.SecretFindings {
		if i == lim.findings {
			items = append(items, fmt.Sprintf("and %d more likely secrets", len(run.SecretFindings)-i))
			break
		}
		items = append(items, fmt.Sprintf("a likely secret was added at %s (%s)", mdCode(fmt.Sprintf("%s:%d", f.File, f.Line)), mdText(f.Rule)))
	}
	if w := run.Worktree; w != nil && len(w.Conflicts) > 0 {
		base := firstNonEmpty(w.BaseBranch, "its base")
		items = append(items, fmt.Sprintf("a merge into %s would conflict in: %s", mdCode(base), nameList(w.Conflicts, 10)))
	}
	if len(items) == 0 {
		return
	}
	b.WriteString("\n### Needs attention\n\n")
	for _, it := range items {
		b.WriteString("- " + it + "\n")
	}
}

func writeFooter(b *strings.Builder, run domain.Run, out domain.Outcome) {
	parts := []string{"Run " + mdCode(run.ID)}
	if w := run.Worktree; w != nil && w.Branch != "" {
		parts = append(parts, "branch "+mdCode(w.Branch))
	}
	if run.Commit != "" {
		parts = append(parts, "commit "+mdCode(shortCommit(run.Commit)))
	}
	if mr := run.MR; mr != nil && mr.State == domain.MRCreated {
		link := mdCode(mr.URL)
		if linkable(mr.URL) {
			link = fmt.Sprintf("[!%d](%s)", mr.IID, mr.URL)
		}
		parts = append(parts, "merge request "+link)
	}
	if run.PreRunRef != "" {
		parts = append(parts, "restore point "+mdCode(shortCommit(run.PreRunRef)))
	}
	c := money(out.Usage.CostUSD, true)
	if out.Usage.PartialCalls > 0 {
		c += " or more"
	}
	parts = append(parts, "cost "+c)
	if out.ActiveMS > 0 {
		parts = append(parts, duration(out.ActiveMS)+" active")
	}
	b.WriteString("\n---\n" + strings.Join(parts, " · ") + "\n")
}
