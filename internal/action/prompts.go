package action

import (
	"fmt"
	"strings"

	"orchestrator/internal/domain"
)

// Every Build*Prompt function starts with a literal "ACTION: <name>" first line, which
// doubles as the routing marker fake executables grep on in integration tests.

// readOnlyRule is shared by every read-only stage prompt: research, analysis and
// planning inspect, they never change the repository.
const readOnlyRule = "This is a read-only step: do not create, modify or delete any files in the repository."

// finalAnswerRule is appended when the step is the workflow's last read-only step and
// no development follows (TASKS.md В4): its answer is what the user gets back.
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
	b.WriteString("{\n  \"summary\": \"...\",\n  \"findings\": [\"...\"],\n  \"options\": [\"...\"],\n  \"recommendation\": \"...\",\n  \"references\": [\"...\"]" + answerField(in.FinalAnswer) + "\n}\n\n")
	writeFinalAnswerRule(&b, in.FinalAnswer)
	fmt.Fprintf(&b, "Task:\n%s\n", in.Task)
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
	b.WriteString("{\n  \"summary\": \"...\",\n  \"root_cause\": \"...\",\n  \"affected_files\": [\"...\"],\n  \"recommendation\": \"...\",\n  \"risks\": [\"...\"]" + answerField(in.FinalAnswer) + "\n}\n\n")
	writeFinalAnswerRule(&b, in.FinalAnswer)
	fmt.Fprintf(&b, "Task:\n%s\n", in.Task)
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
	b.WriteString("{\n  \"summary\": \"...\",\n  \"steps\": [\"...\"],\n  \"affected_files\": [\"...\"],\n  \"risks\": [\"...\"]" + answerField(in.FinalAnswer) + "\n}\n\n")
	writeFinalAnswerRule(&b, in.FinalAnswer)
	fmt.Fprintf(&b, "Task:\n%s\n", in.Task)
	writeResearch(&b, in.Research)
	if in.Analysis != nil {
		fmt.Fprintf(&b, "\nAnalysis:\n%s\nRoot cause: %s\nRecommendation: %s\n",
			in.Analysis.Summary, in.Analysis.RootCause, in.Analysis.Recommendation)
	}
	return b.String()
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
// review failure, ReviewIssues carries the structured issues.
type ImplementPromptInput struct {
	Task                   string
	Analysis               *domain.AnalysisArtifact
	Plan                   *domain.PlanArtifact
	PreviousImplementation *domain.ImplementationArtifact
	LastTestResult         *domain.TestResult
	ReviewIssues           []domain.ReviewIssue
}

func BuildImplementPrompt(in ImplementPromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: implement\n\n")
	b.WriteString("Implement the following software engineering task by modifying the code in the current repository directly. Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"summary\": \"...\",\n  \"changed_files\": [\"...\"],\n  \"tests_added\": [\"...\"]\n}\n\n")

	fmt.Fprintf(&b, "Task:\n%s\n\n", in.Task)

	if in.Analysis != nil {
		fmt.Fprintf(&b, "Analysis:\n%s\nRoot cause: %s\nRecommendation: %s\n\n",
			in.Analysis.Summary, in.Analysis.RootCause, in.Analysis.Recommendation)
	}
	if in.Plan != nil {
		fmt.Fprintf(&b, "Plan:\n%s\nSteps:\n- %s\n\n", in.Plan.Summary, strings.Join(in.Plan.Steps, "\n- "))
	}
	if in.PreviousImplementation != nil {
		fmt.Fprintf(&b, "Previous implementation attempt:\n%s\n\n", in.PreviousImplementation.Summary)
	}

	if in.LastTestResult != nil {
		b.WriteString("The previous implementation FAILED the test command. Fix the implementation so the tests pass.\n\n")
		fmt.Fprintf(&b, "Failing test command: %s\nExit code: %d\n\nstdout:\n%s\n\nstderr:\n%s\n\n",
			in.LastTestResult.Command, in.LastTestResult.ExitCode, in.LastTestResult.Stdout, in.LastTestResult.Stderr)
	}

	if len(in.ReviewIssues) > 0 {
		b.WriteString("A code review FAILED on the previous implementation. Address every issue below.\n\n")
		for _, issue := range in.ReviewIssues {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", issue.Severity, issue.File, issue.Description)
		}
		b.WriteString("\n")
	}

	return b.String()
}

type ReviewPromptInput struct {
	Task           string
	Analysis       *domain.AnalysisArtifact
	Plan           *domain.PlanArtifact
	Implementation *domain.ImplementationArtifact
	TestResult     *domain.TestResult
	GitDiff        string
}

func BuildReviewPrompt(in ReviewPromptInput) string {
	var b strings.Builder
	b.WriteString("ACTION: review\n\n")
	b.WriteString("Review the current repository changes against the original task. Inspect the Git diff below - it is the actual repository state, not a summary. Respond with a single JSON object, no markdown fences, no prose before or after it, matching exactly this shape:\n\n")
	b.WriteString("{\n  \"status\": \"pass\",\n  \"summary\": \"...\",\n  \"issues\": [\n    {\"severity\": \"high\", \"file\": \"...\", \"description\": \"...\"}\n  ]\n}\n\n")
	b.WriteString("\"severity\" must be exactly one of: critical, high, medium, low.\n\n")

	fmt.Fprintf(&b, "Original task (expected outcome):\n%s\n\n", in.Task)

	if in.Analysis != nil {
		fmt.Fprintf(&b, "Analysis:\n%s\n\n", in.Analysis.Summary)
	}
	if in.Plan != nil {
		fmt.Fprintf(&b, "Plan:\n%s\n\n", in.Plan.Summary)
	}
	if in.Implementation != nil {
		fmt.Fprintf(&b, "Implementation summary:\n%s\n\n", in.Implementation.Summary)
	}
	if in.TestResult != nil {
		fmt.Fprintf(&b, "Latest test result:\ncommand: %s\nexit code: %d\nsuccess: %t\n\n",
			in.TestResult.Command, in.TestResult.ExitCode, in.TestResult.Success)
	}

	fmt.Fprintf(&b, "Git diff:\n%s\n", in.GitDiff)

	return b.String()
}
