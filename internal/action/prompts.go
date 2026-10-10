package action

import (
	"fmt"
	"strings"

	"orchestrator/internal/domain"
)

// Every Build*Prompt function starts with a literal "ACTION: <name>" first line, which
// doubles as the routing marker fake executables grep on in integration tests. The task is
// always embedded through taskBlock, repository data through fence.

// readOnlyRule is shared by every read-only stage prompt: research, analysis and
// planning inspect, they never change the repository.
const readOnlyRule = "This is a read-only step: do not create, modify or delete any files in the repository."

// finalAnswerRule is appended when the step is the workflow's last read-only step and
// no development follows (TASKS.md В4): its answer is what the user gets back.
// summaryShortRule asks every stage for a one-line "summary_short" next to "summary", for
// an operator skimming the run.
const summaryShortRule = `"summary_short" is "summary" in one line of at most 72 characters, without a trailing period.`

// acceptanceCriteriaRule tells plan what a criterion is (specs/quality/SPEC.md §4.2). parsePlan
// enforces the same cap.
const acceptanceCriteriaRule = `"acceptance_criteria" lists what must be true of the result for the task to be done. ` +
	`Each criterion is one observable, checkable statement about the result, for example ` +
	`"function X returns ErrEmpty for an empty input" or "` + "`go test ./internal/store`" + ` passes". ` +
	`Give at most 8; write none rather than vague ones.`

// implementSummaryShortRule is implement's variant: its "summary_short" becomes the
// commit message offered at the end of the run (MVP.md §42.2).
const implementSummaryShortRule = `"summary_short" is one line of at most 72 characters, without a trailing period, ` +
	`saying what the change does in the imperative mood (for example "Exclude HTTP 404 from retries"): ` +
	`it becomes the commit message.`

// conventionsFiles are where a repository writes down how a change is finished. The task
// rarely says it; implement is told to follow them and review to check them, so a missed
// update goes back to implement instead of needing a stage of its own.
const conventionsFiles = `the repository's own instructions (CLAUDE.md, AGENTS.md, CONTRIBUTING.md, ` +
	`and the README of the specification or task list the task comes from)`

// implementConventionsRule asks implement to finish the change the way the repository says.
const implementConventionsRule = `Finish the change the way ` + conventionsFiles + ` say a change is finished: ` +
	`update the documentation the change makes out of date, and the task tracking they require ` +
	`(for example marking the task done), as part of this change.`

// reviewConventionsRule asks review to check the same, and says what a miss weighs.
const reviewConventionsRule = `Also check the change against ` + conventionsFiles + `. ` +
	`A documentation or task-tracking update they require for this change and the diff lacks is a missed requirement of the task (high). ` +
	`Other documentation the change leaves out of date is medium.

`

// implementOperatorRule tells implement what it cannot do: its authority (edit) has no
// shell, and the test command is the only thing the run executes (MVP.md §17, §38). A step
// that needs anything else is the operator's, reported rather than claimed done.
const implementOperatorRule = `You can read and edit files; you cannot run commands, use installed tools or reach the network. ` +
	`After you, the orchestrator runs the configured test command from the repository root, and nothing else. ` +
	`A step the task or the repository's instructions require that needs running something else (a check against an ` +
	`installed tool, a build, lint or format gate the test command does not run) is left to the operator: never claim ` +
	`it was done, name it in "summary" as left to the operator, and leave the task tracking that waits on it open.`

// reviewOperatorRule keeps review from failing on what another implement attempt cannot do:
// sent back as high, such a step loops until max_fix_iterations. It also keeps the reviewer
// from reading more into the test result than the command covers.
const reviewOperatorRule = `Implement can only read and edit files: it cannot run commands, use installed tools or reach the network. ` +
	`The run executes nothing but the test command shown under "Latest test result", from the repository root; ` +
	`do not assume it covers what it does not run (another module, a linter, a formatter, a live check). ` +
	`A requirement that can only be met by running something else cannot be fixed by another implementation attempt: ` +
	`report it as medium with "operator: " at the start of its description, never as critical or high. ` +
	`The same holds for task tracking left open because it waits on such a step. ` +
	`A claim in the diff or the implementation summary that such a step was done is not evidence that it was.

`

const finalAnswerRule = `This is the final step of the workflow and no code will be changed. In "answer", ` +
	`write the complete reply to the user's request, in the language the request is written in.`

type ResearchPromptInput struct {
	Task        string
	FinalAnswer bool
}

func BuildResearchPrompt(in ResearchPromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: research\n\n")
	b.WriteString("Research the external knowledge the following software engineering task needs: approaches, libraries, tools, alternatives and their trade-offs. " + readOnlyRule + " Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"summary_short\": \"...\",\n  \"findings\": [\"...\"],\n  \"options\": [\"...\"],\n  \"recommendation\": \"...\",\n  \"references\": [\"...\"]" + answerField(in.FinalAnswer) + "\n}\n\n")
	b.WriteString(summaryShortRule + "\n\n")
	writeFinalAnswerRule(&b, in.FinalAnswer)
	b.WriteString("Task:\n" + taskBlock(in.Task))
	return b.String()
}

type AnalyzePromptInput struct {
	Task        string
	Research    *domain.ResearchArtifact
	FinalAnswer bool
}

func BuildAnalyzePrompt(in AnalyzePromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: analyze\n\n")
	b.WriteString("Analyze the following software engineering task. " + readOnlyRule + " Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"summary_short\": \"...\",\n  \"root_cause\": \"...\",\n  \"affected_files\": [\"...\"],\n  \"recommendation\": \"...\",\n  \"risks\": [\"...\"]" + answerField(in.FinalAnswer) + "\n}\n\n")
	b.WriteString(summaryShortRule + "\n\n")
	writeFinalAnswerRule(&b, in.FinalAnswer)
	b.WriteString("Task:\n" + taskBlock(in.Task))
	writeResearch(&b, in.Research)
	return b.String()
}

type PlanPromptInput struct {
	Task        string
	Research    *domain.ResearchArtifact
	Analysis    *domain.AnalysisArtifact
	FinalAnswer bool
}

func BuildPlanPrompt(in PlanPromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: plan\n\n")
	b.WriteString("Create an implementation plan for the following software engineering task. " + readOnlyRule + " Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"summary_short\": \"...\",\n  \"steps\": [\"...\"],\n  \"affected_files\": [\"...\"],\n  \"risks\": [\"...\"],\n  \"acceptance_criteria\": [\"...\"]" + answerField(in.FinalAnswer) + "\n}\n\n")
	b.WriteString(summaryShortRule + "\n\n")
	b.WriteString(acceptanceCriteriaRule + "\n\n")
	writeFinalAnswerRule(&b, in.FinalAnswer)
	b.WriteString("Task:\n" + taskBlock(in.Task))
	writeResearch(&b, in.Research)
	if in.Analysis != nil {
		fmt.Fprintf(&b, "\nAnalysis:\n%s\nRoot cause: %s\nRecommendation: %s\n",
			in.Analysis.Summary, in.Analysis.RootCause, in.Analysis.Recommendation)
		writeList(&b, "Affected files (from analysis)", in.Analysis.AffectedFiles)
		writeList(&b, "Risks (from analysis)", in.Analysis.Risks)
	}
	return b.String()
}

// writeList writes heading and one "- item" line per non-blank item. Without one it writes
// nothing, so an empty list leaves no heading behind.
func writeList(b *strings.Builder, heading string, items []string) {
	writeItems(b, heading, items, func(int) string { return "- " })
}

// writeNumberedList is writeList with "1. item" lines, for a list the reader refers to by
// number.
func writeNumberedList(b *strings.Builder, heading string, items []string) {
	writeItems(b, heading, items, func(n int) string { return fmt.Sprintf("%d. ", n) })
}

func writeItems(b *strings.Builder, heading string, items []string, marker func(n int) string) {
	n := 0
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		if n == 0 {
			b.WriteString(heading + ":\n")
		}
		n++
		b.WriteString(marker(n) + item + "\n")
	}
}

func answerField(final bool) string {
	if !final {
		return ""
	}
	return ",\n  \"answer\": \"...\""
}

func writeFinalAnswerRule(b *strings.Builder, final bool) {
	if final {
		b.WriteString(finalAnswerRule + "\n\n")
	}
}

func writeResearch(b *strings.Builder, r *domain.ResearchArtifact) {
	if r == nil {
		return
	}
	fmt.Fprintf(b, "\nResearch:\n%s\n", r.Summary)
	if len(r.Findings) > 0 {
		fmt.Fprintf(b, "Findings:\n- %s\n", strings.Join(r.Findings, "\n- "))
	}
	if r.Recommendation != "" {
		fmt.Fprintf(b, "Recommendation: %s\n", r.Recommendation)
	}
}

// ImplementPromptInput carries whichever prior context is available. On the first
// attempt, Analysis/Plan (whichever the workflow produced) are set and
// PreviousImplementation/LastTestResult/ReviewIssues are nil/empty. On a retry after test
// failure, LastTestResult carries the failing command/stdout/stderr. On a retry after
// review failure, ReviewIssues carries the structured issues and UnmetCriteria the
// acceptance criteria the review found unmet, with their evidence.
type ImplementPromptInput struct {
	Task                   string
	Analysis               *domain.AnalysisArtifact
	Plan                   *domain.PlanArtifact
	PreviousImplementation *domain.ImplementationArtifact
	LastTestResult         *domain.TestResult
	ReviewIssues           []domain.ReviewIssue
	UnmetCriteria          []domain.CriterionVerdict
	// PreRunFailure is the test command's red result before any change, when the operator
	// started the run with --allow-red-tests (specs/hardening/SPEC.md §8.4).
	PreRunFailure *domain.TestResult
	// MaxTestOutput bounds LastTestResult's stdout and stderr together, in bytes
	// (limits.prompt.max_test_output_bytes; BoundTestOutput). 0 embeds them whole.
	MaxTestOutput int
	// RunDiffStat is the git diff --stat of what the run has changed so far, for a retry
	// (renderDiffStat, bounded by limits.prompt.max_diff_bytes). The dispatcher computes it,
	// so this builder stays pure; "" leaves the section out.
	RunDiffStat string
	// Verify is true when the session has the orchestrator's verify tools
	// (actions.implement.verify; specs/quality/SPEC.md §6.1), and VerifyChecks when it also
	// has run_check, that is when the config lists checks.
	Verify, VerifyChecks bool
}

// implementVerifyRule tells a session with the verify tools to use them. The tests it runs
// are the operator's test command, so the closing sentence keeps the final run authoritative.
func implementVerifyRule(withChecks bool) string {
	tools := "the tool `mcp__orch__run_tests`"
	if withChecks {
		tools += " (and its checks with `mcp__orch__run_check`)"
	}
	return "You can run the project's tests with " + tools + ". Use them to check your change before you answer. " +
		"The orchestrator runs the tests again after you finish, and only that run counts."
}

func BuildImplementPrompt(in ImplementPromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: implement\n\n")
	b.WriteString("Implement the following software engineering task by modifying the code in the current repository directly. Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"summary_short\": \"...\",\n  \"changed_files\": [\"...\"],\n  \"tests_added\": [\"...\"]\n}\n\n")
	b.WriteString(implementSummaryShortRule + "\n\n")
	b.WriteString(implementConventionsRule + "\n\n")
	b.WriteString(implementOperatorRule + "\n\n")
	if in.Verify {
		b.WriteString(implementVerifyRule(in.VerifyChecks) + "\n\n")
	}

	b.WriteString("Task:\n" + taskBlock(in.Task) + "\n")

	if in.Analysis != nil {
		fmt.Fprintf(&b, "Analysis:\n%s\nRoot cause: %s\nRecommendation: %s\n",
			in.Analysis.Summary, in.Analysis.RootCause, in.Analysis.Recommendation)
		writeList(&b, "Affected files (from analysis)", in.Analysis.AffectedFiles)
		writeList(&b, "Risks (from analysis)", in.Analysis.Risks)
		b.WriteString("\n")
	}
	if in.Plan != nil {
		fmt.Fprintf(&b, "Plan:\n%s\n", in.Plan.Summary)
		writeList(&b, "Steps", in.Plan.Steps)
		writeList(&b, "Affected files (from plan)", in.Plan.AffectedFiles)
		writeList(&b, "Risks (from plan)", in.Plan.Risks)
		writeList(&b, "Acceptance criteria", in.Plan.AcceptanceCriteria)
		b.WriteString("\n")
	}
	if in.PreviousImplementation != nil {
		fmt.Fprintf(&b, "Previous implementation attempt:\n%s\n\n", in.PreviousImplementation.Summary)
		if in.RunDiffStat != "" {
			b.WriteString("What the run has changed so far (git diff --stat against the run's start):\n")
			b.WriteString(fence("diffstat", "the git diff --stat of the run so far", in.RunDiffStat))
			b.WriteString("\n")
		}
	}

	if r := in.PreRunFailure; r != nil && in.LastTestResult == nil {
		if r.FailedCheck != "" {
			fmt.Fprintf(&b, "The check %q (one of the operator's checks, run before the tests) already FAILS before any change, "+
				"and the operator expects that: the task is to make it pass. The tests did not run. "+
				"Its output from before the change:\n\n", r.FailedCheck)
			fmt.Fprintf(&b, "Check command: %s\nExit code: %d\n\n", r.Command, r.ExitCode)
			writeTestOutput(&b, *r, in.MaxTestOutput, checkOutputOwner(r.FailedCheck))
		} else {
			b.WriteString("The test command already FAILS before any change, and the operator expects that: the task is " +
				"to make it pass. Its output from before the change:\n\n")
			fmt.Fprintf(&b, "Test command: %s\nExit code: %d\n\n", r.Command, r.ExitCode)
			writeTestOutput(&b, *r, in.MaxTestOutput, testOutputOwner)
		}
	}

	if r := in.LastTestResult; r != nil {
		if r.FailedCheck != "" {
			// SPEC §5.4: the check is named, and the prompt says the tests did not run - it
			// never lets the model assume they pass.
			fmt.Fprintf(&b, "The previous implementation FAILED the check %q (one of the operator's checks, run before the tests). "+
				"Fix the implementation so the check passes.\nThe tests did not run: do not assume they pass.\n\n", r.FailedCheck)
			fmt.Fprintf(&b, "Failing command: %s\nExit code: %d\n\n", r.Command, r.ExitCode)
			writeTestOutput(&b, *r, in.MaxTestOutput, checkOutputOwner(r.FailedCheck))
		} else {
			b.WriteString("The previous implementation FAILED the test command. Fix the implementation so the tests pass.\n\n")
			fmt.Fprintf(&b, "Failing test command: %s\nExit code: %d\n\n", r.Command, r.ExitCode)
			writeTestOutput(&b, *r, in.MaxTestOutput, testOutputOwner)
		}
	}

	if len(in.UnmetCriteria) > 0 || len(in.ReviewIssues) > 0 {
		switch {
		case len(in.UnmetCriteria) == 0:
			b.WriteString("A code review FAILED on the previous implementation. Address every issue below.\n\n")
		case len(in.ReviewIssues) == 0:
			b.WriteString("A code review FAILED on the previous implementation. Address every unmet criterion below.\n\n")
		default:
			b.WriteString("A code review FAILED on the previous implementation. Address every unmet criterion and every issue below.\n\n")
		}
		if len(in.UnmetCriteria) > 0 {
			// The criterion comes from the plan and the evidence from the review: both are
			// model output, kept to one line each so they stay list items.
			b.WriteString("Unmet acceptance criteria:\n")
			for _, c := range in.UnmetCriteria {
				fmt.Fprintf(&b, "- %d. %s\n  evidence: %s\n", c.Index, oneLine(c.Criterion), oneLine(c.Evidence))
			}
			b.WriteString("\n")
			if len(in.ReviewIssues) > 0 {
				b.WriteString("Review issues:\n")
			}
		}
		for _, issue := range in.ReviewIssues {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", issue.Severity, issue.File, issue.Description)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// TestOutputCut reports whether BuildImplementPrompt(in) shortens the test output it
// embeds, so the step can record it.
func TestOutputCut(in ImplementPromptInput) bool {
	r := in.LastTestResult
	if r == nil {
		r = in.PreRunFailure
	}
	if r == nil || in.MaxTestOutput <= 0 {
		return false
	}
	_, _, omitted := BoundTestOutput(r.Stdout, r.Stderr, in.MaxTestOutput)
	return omitted.Any()
}

// testOutputOwner and checkOutputOwner say whose output a fence holds, in the fence's
// description: the test command's, or the failed check's.
const testOutputOwner = "the test command's"

func checkOutputOwner(check string) string { return fmt.Sprintf("the check %q's", check) }

// writeTestOutput embeds a test result's stdout and stderr as fenced repository data,
// bounded by maxBytes (BoundTestOutput; 0 embeds them whole), and says so when it cut.
// owner is whose output it is (testOutputOwner or checkOutputOwner).
func writeTestOutput(b *strings.Builder, r domain.TestResult, maxBytes int, owner string) {
	stdout, stderr := r.Stdout, r.Stderr
	var omitted Omitted
	if maxBytes > 0 {
		stdout, stderr, omitted = BoundTestOutput(stdout, stderr, maxBytes)
	}
	b.WriteString(fence("stdout", owner+" stdout", stdout))
	b.WriteString("\n")
	b.WriteString(fence("stderr", owner+" stderr", stderr))
	b.WriteString("\n")
	if omitted.Any() {
		fmt.Fprintf(b, "The test output above was shortened to fit this prompt: %d lines (%d bytes) were left out "+
			"where the \"omitted\" markers are. What is kept is the beginning, the lines that look like failures, "+
			"and the end.\n\n", omitted.Lines, omitted.Bytes)
	}
}

type ReviewPromptInput struct {
	Task           string
	Analysis       *domain.AnalysisArtifact
	Plan           *domain.PlanArtifact
	Implementation *domain.ImplementationArtifact
	TestResult     *domain.TestResult
	GitDiff        string
	// DiffPartial is true when some changed file's content is not in GitDiff (summarized,
	// withheld, binary, over the budget, or not listed).
	DiffPartial bool
}

// reviewSeverityRubric tells the reviewer what each severity means and what it causes,
// so the level it picks matches domain.ReviewResult.Decide: critical/high fail the review
// and go back to implement, medium/low pass with a warning (MVP.md §19).
const reviewSeverityRubric = `Severity rubric - choose by impact, not by how certain you are:
- critical: the change breaks the build, loses or corrupts data, or opens a security hole.
- high: incorrect behavior, a missed requirement of the task, or a regression in existing behavior.
- medium: a real but non-blocking problem - a missing edge-case test, fragile or unclear code.
- low: style, naming, comments, minor cleanups.
Any critical or high issue fails the review and the change is sent back for another implementation attempt with every issue you list; medium and low issues are reported as warnings and do not block. Set "status" to "fail" exactly when you report a critical or high issue or an acceptance criterion is not met. Base every issue on the change, one per distinct problem.

`

// reviewSurroundingsRule asks review to read beyond the diff (specs/quality/SPEC.md §7.2).
const reviewSurroundingsRule = `You have Read, Grep and Glob. Read the code around each changed function ` +
	`(callers, the interface it implements, the tests that cover it), not only the diff. ` +
	`An issue about code outside the diff is valid if the change causes it.`

// reviewCriteriaRule asks for one verdict per numbered acceptance criterion of the plan
// (specs/quality/SPEC.md §7.1). parseReviewStrict enforces the same.
const reviewCriteriaRule = `"criteria" has exactly one entry for each numbered acceptance criterion below: ` +
	`"index" is the criterion's number and every number appears exactly once. ` +
	`"met" is true only when the change satisfies the criterion. "evidence" is not empty and at most 500 bytes: ` +
	`the file:line that shows the criterion is met, or why it is not. ` +
	`A criterion that is not met fails the review like a high issue.`

// oneLine collapses s to a single line, so model text printed in a list stays one item.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// planCriteria are the plan's acceptance criteria as the prompts number them: the blank
// ones are skipped, as writeNumberedList does, so a verdict's index counts the same list.
func planCriteria(p *domain.PlanArtifact) []string {
	var out []string
	if p == nil {
		return out
	}
	for _, c := range p.AcceptanceCriteria {
		if strings.TrimSpace(c) != "" {
			out = append(out, c)
		}
	}
	return out
}

func BuildReviewPrompt(in ReviewPromptInput) string {
	hasCriteria := len(planCriteria(in.Plan)) > 0
	var b strings.Builder
	b.WriteString("ACTION: review\n\n")
	b.WriteString("Review the current repository changes against the original task. Inspect the Git diff below - it is the actual repository state, not a summary. Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	criteriaShape := ""
	if hasCriteria {
		criteriaShape = ",\n  \"criteria\": [\n    {\"index\": 1, \"met\": true, \"evidence\": \"file:line or why not\"}\n  ]"
	}
	b.WriteString("{\n  \"status\": \"pass\",\n  \"summary\": \"...\",\n  \"summary_short\": \"...\",\n  \"issues\": [\n    {\"severity\": \"high\", \"file\": \"...\", \"description\": \"...\"}\n  ]" + criteriaShape + "\n}\n\n")
	b.WriteString(summaryShortRule + "\n\n")
	if hasCriteria {
		b.WriteString(reviewCriteriaRule + "\n\n")
	}
	b.WriteString("\"severity\" must be exactly one of: critical, high, medium, low.\n\n")
	b.WriteString(reviewSeverityRubric)
	b.WriteString(reviewSurroundingsRule + "\n\n")
	b.WriteString(reviewConventionsRule)
	b.WriteString(reviewOperatorRule)

	b.WriteString("Original task (expected outcome):\n" + taskBlock(in.Task) + "\n")

	if in.Analysis != nil {
		fmt.Fprintf(&b, "Analysis:\n%s\n\n", in.Analysis.Summary)
	}
	if in.Plan != nil {
		fmt.Fprintf(&b, "Plan:\n%s\n", in.Plan.Summary)
		writeList(&b, "Steps", in.Plan.Steps)
		writeList(&b, "Risks (from plan)", in.Plan.Risks)
		writeNumberedList(&b, "Acceptance criteria", in.Plan.AcceptanceCriteria)
		b.WriteString("\n")
	}
	if in.Implementation != nil {
		fmt.Fprintf(&b, "Implementation summary:\n%s\n\n", in.Implementation.Summary)
	}
	if in.TestResult != nil {
		fmt.Fprintf(&b, "Latest test result:\ncommand: %s\nexit code: %d\nsuccess: %t\n\n",
			in.TestResult.Command, in.TestResult.ExitCode, in.TestResult.Success)
	}

	b.WriteString("Git diff:\n")
	if in.DiffPartial {
		b.WriteString("The diff starts with every changed file and how it is shown. Not every file's content is " +
			"in it: read a file that is summarized, binary or not shown for the diff budget in the repository " +
			"when its content matters to the review. A withheld file matches the secret deny list; you cannot " +
			"read it, so judge the change by the rest.\n")
	}
	b.WriteString(fence("diff", "the git diff of the working tree, new files included", in.GitDiff))

	return b.String()
}
