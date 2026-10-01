package logging

import (
	"fmt"
	"io"
	"strings"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// testOutputTailLines is how much of a failed test command's output the step result shows;
// the full output is in the step's log files.
const testOutputTailLines = 20

// printResult renders what a completed step produced - the same content as its artifact
// file, as labelled plain text instead of JSON. Empty fields are left out; a step with
// nothing to show prints nothing.
func (r *ConsoleReporter) printResult(exec domain.StepExecution, outcome workflow.StepOutcome) {
	if exec.Status != domain.StepCompleted {
		return
	}
	var b strings.Builder
	switch a := outcome.Artifact.(type) {
	case domain.ResearchArtifact:
		writeText(&b, "Summary", a.Summary)
		writeList(&b, "Findings", a.Findings)
		writeList(&b, "Options", a.Options)
		writeText(&b, "Recommendation", a.Recommendation)
		writeList(&b, "References", a.References)
		writeText(&b, "Answer", a.Answer)
	case domain.AnalysisArtifact:
		writeText(&b, "Summary", a.Summary)
		writeText(&b, "Root cause", a.RootCause)
		writeList(&b, "Affected files", a.AffectedFiles)
		writeText(&b, "Recommendation", a.Recommendation)
		writeList(&b, "Risks", a.Risks)
		writeText(&b, "Answer", a.Answer)
	case domain.PlanArtifact:
		writeText(&b, "Summary", a.Summary)
		writeNumbered(&b, "Steps", a.Steps)
		writeList(&b, "Affected files", a.AffectedFiles)
		writeList(&b, "Risks", a.Risks)
		writeText(&b, "Answer", a.Answer)
	case domain.ImplementationArtifact:
		writeText(&b, "Summary", a.Summary)
		writeList(&b, "Changed files", a.ChangedFiles)
		writeList(&b, "Tests added", a.TestsAdded)
	}
	if review := exec.ReviewResult; review != nil {
		writeText(&b, "Summary", review.Summary)
		issues := make([]string, 0, len(review.Issues))
		for _, issue := range review.Issues {
			line := fmt.Sprintf("[%s] %s", issue.Severity, issue.Description)
			if issue.File != "" {
				line = fmt.Sprintf("[%s] %s: %s", issue.Severity, issue.File, issue.Description)
			}
			issues = append(issues, line)
		}
		writeList(&b, "Issues", issues)
	}
	if test := exec.TestResult; test != nil && !test.Success {
		fmt.Fprintf(&b, "  Exit code: %d (%s)\n", test.ExitCode, formatDuration(test.Duration))
		output := strings.TrimRight(strings.Join(nonEmpty(test.Stdout, test.Stderr), "\n"), "\n")
		if output != "" {
			lines := strings.Split(output, "\n")
			label := "Output"
			if len(lines) > testOutputTailLines {
				lines = lines[len(lines)-testOutputTailLines:]
				label = fmt.Sprintf("Output (last %d lines)", testOutputTailLines)
			}
			writeText(&b, label, strings.Join(lines, "\n"))
		}
	}
	if b.Len() == 0 {
		return
	}
	fmt.Fprintf(r.Out, "\nResult:\n%s", b.String())
}

// writeText renders a single-line value after its label and a multi-line one indented
// below it.
func writeText(w io.Writer, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if !strings.Contains(value, "\n") {
		fmt.Fprintf(w, "  %s: %s\n", label, value)
		return
	}
	fmt.Fprintf(w, "  %s:\n", label)
	for _, line := range strings.Split(value, "\n") {
		fmt.Fprintf(w, "    %s\n", strings.TrimRight(line, " \t\r"))
	}
}

func writeList(w io.Writer, label string, items []string) {
	writeItems(w, label, items, func(int) string { return "-" })
}

func writeNumbered(w io.Writer, label string, items []string) {
	writeItems(w, label, items, func(i int) string { return fmt.Sprintf("%d.", i+1) })
}

// writeItems renders one item per line under its label, continuation lines of a
// multi-line item aligned with the item's text.
func writeItems(w io.Writer, label string, items []string, marker func(int) string) {
	items = nonEmpty(items...)
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s:\n", label)
	for i, item := range items {
		m := marker(i)
		lines := strings.Split(item, "\n")
		fmt.Fprintf(w, "    %s %s\n", m, strings.TrimRight(lines[0], " \t\r"))
		for _, line := range lines[1:] {
			fmt.Fprintf(w, "    %s %s\n", strings.Repeat(" ", len(m)), strings.TrimRight(line, " \t\r"))
		}
	}
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
