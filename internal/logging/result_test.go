package logging

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func TestStepFinished_PrintsArtifactAsText(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.Verbose = true // keeps the duration line out of the comparison

	exec := domain.StepExecution{Action: domain.ActionPlan, Status: domain.StepCompleted}
	artifact := domain.PlanArtifact{
		Summary:       "Stop retrying 404s.",
		SummaryShort:  "No 404 retries",
		Steps:         []string{"Classify the status", "Add a test\nfor 404"},
		AffectedFiles: []string{"client.go"},
		Answer:        "line one\nline two",
	}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeSuccess, Artifact: artifact})

	want := "\nResult:\n" +
		"  In short: No 404 retries\n" +
		"  Summary: Stop retrying 404s.\n" +
		"  Steps:\n" +
		"    1. Classify the status\n" +
		"    2. Add a test\n" +
		"       for 404\n" +
		"  Affected files:\n" +
		"    - client.go\n" +
		"  Answer:\n" +
		"    line one\n" +
		"    line two\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_FailedStepPrintsNoResult(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.Verbose = true

	exec := domain.StepExecution{Action: domain.ActionImplement, Status: domain.StepFailed}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{
		Outcome:  workflow.OutcomeSuccess,
		Artifact: domain.ImplementationArtifact{Summary: "wrote a hook"},
	})
	if strings.Contains(buf.String(), "Result:") {
		t.Errorf("a failed step has no result to show, got %q", buf.String())
	}
}

func TestStepFinished_ReviewFailureListsIssues(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.MaxFixIterations = 3

	exec := domain.StepExecution{
		Action: domain.ActionReview,
		Status: domain.StepCompleted,
		ReviewResult: &domain.ReviewResult{Summary: "Retries 404.", Issues: []domain.ReviewIssue{
			{Severity: domain.SeverityHigh, File: "client.go", Description: "404 is retried"},
			{Severity: domain.SeverityLow, Description: "naming"},
		}},
	}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeFailure})

	want := "✗ review failed\n" +
		"\nResult:\n" +
		"  Summary: Retries 404.\n" +
		"  Issues:\n" +
		"    - [high] client.go: 404 is retried\n" +
		"    - [low] naming\n" +
		"\n1 high severity issue found.\n1 low severity issue found.\nReturning to implementation (iteration 1/3).\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_TestFailureShowsOutputTail(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	var lines []string
	for i := 1; i <= 25; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	exec := domain.StepExecution{
		Action:     domain.ActionTest,
		Status:     domain.StepCompleted,
		TestResult: &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: strings.Join(lines, "\n") + "\n"},
	}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeFailure})

	got := buf.String()
	for _, want := range []string{"  Exit code: 1 (0s)\n", "  Output (last 20 lines):\n    line 6\n", "    line 25\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in %q", want, got)
		}
	}
	if strings.Contains(got, "line 5\n") {
		t.Errorf("expected only the last 20 lines, got %q", got)
	}
}
