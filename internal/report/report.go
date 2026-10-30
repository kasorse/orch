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

// reportLimits are how much of each list report.md shows.
type reportLimits struct {
	issues, findings, rules int
}

// reportCuts are the limits RunReport tries in turn until the report fits (A8: 30 issues).
var reportCuts = []reportLimits{
	{issues: 30, findings: 20, rules: 20},
	{issues: 10, findings: 10, rules: 10},
	{issues: 0, findings: 3, rules: 3},
}

// RunReport renders report.md: one run's report for the reviewer of its change - the stages
// that passed, whether the tests and the review passed and what needs a person's eye; the task
// is in its ticket and the change in the merge request (specs/delivery/SPEC.md §5). It reads only the run's own record and
// decision.json (trace, nil when there is none) and makes no call; the same record always
// renders the same text. Everything the task, the ticket or a model wrote is escaped
// (mdText), so the text can go into a merge request as it is. The result is at most
// maxReportBytes: lists are cut first.
func RunReport(run domain.Run, trace *domain.DecisionTrace) string {
	out := domain.ComputeOutcome(run, trace)
	if run.Outcome != nil {
		out = *run.Outcome
	}
	var text string
	for _, lim := range reportCuts {
		if text = renderReport(run, out, lim); len(text) <= maxReportBytes {
			return text
		}
	}
	return cutAtLine(text, maxReportBytes)
}

// RunReportFit is RunReport for a text that must also satisfy fits, such as a merge request
// description that has to fit one push option once its line breaks are encoded (specs/delivery/
// SPEC.md §7.3). The report is rendered again with the next smaller lists (the same cut order
// as RunReport), until the text fits; when even the smallest does not,
// it is cut at a line with a closing mark. report.md on disk stays RunReport's version.
func RunReportFit(run domain.Run, trace *domain.DecisionTrace, fits func(string) bool) string {
	out := domain.ComputeOutcome(run, trace)
	if run.Outcome != nil {
		out = *run.Outcome
	}
	var text string
	for _, lim := range reportCuts {
		if text = renderReport(run, out, lim); len(text) <= maxReportBytes && fits(text) {
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

func renderReport(run domain.Run, out domain.Outcome, lim reportLimits) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", mdText(reportTitle(run)))
	b.WriteString(statusLine(run, out) + "\n")
	if src := sourceLine(run.Task.Source); src != "" {
		b.WriteString("\nSource: " + src + "\n")
	}
	if run.ReviewTarget != nil {
		// What a review run reviewed is the refs, not a change it made (specs/delivery/SPEC.md §5).
		writeReviewedSection(&b, run)
	}
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
	if stages := passedStages(run); len(stages) > 0 {
		parts = append(parts, "**Passed stages:** "+stageNames(stages))
	}
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

// passedStages are the workflow's stages, in its order, whose last step completed: tests that
// passed, a review whose verdict is pass.
func passedStages(run domain.Run) []domain.Stage {
	if run.Workflow == nil {
		return nil
	}
	var stages []domain.Stage
	for _, st := range run.Workflow.Steps {
		e := lastExecution(run, st.Action)
		switch {
		case e == nil || e.Status != domain.StepCompleted:
		case e.TestResult != nil && !e.TestResult.Success:
		case e.ReviewResult != nil && e.ReviewResult.Decide() != domain.ReviewPass:
		default:
			stages = append(stages, st.Stage)
		}
	}
	return stages
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

// writeReviewedSection says what a review run reviewed: base...head, and the commits they name.
func writeReviewedSection(b *strings.Builder, run domain.Run) {
	t := run.ReviewTarget
	b.WriteString("\n### Reviewed\n\n")
	fmt.Fprintf(b, "%s · merge base %s · head %s\n", mdCode(t.BaseRefName+"..."+t.HeadRefName), mdCode(shortCommit(t.MergeBase)), mdCode(shortCommit(t.Head)))
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
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
