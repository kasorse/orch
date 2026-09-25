package logging

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func TestStepStarted_PrintsNumberedUppercaseHeader(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.StepStarted(&domain.Run{}, 0, domain.ActionAnalyze)

	want := "\n[1] ANALYZE\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestProviderCallStarted_PrintsProviderModelAndRunningLine(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.ProviderCallStarted("claude", "sonnet")

	want := "Provider: claude\nModel: sonnet\n→ running claude...\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_AnalyzeSuccess_PrintsCompletedWithDuration(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	start := time.Now()
	exec := domain.StepExecution{
		Action:    domain.ActionAnalyze,
		Provider:  &domain.ProviderRef{Provider: "claude", Model: "sonnet"},
		StartedAt: start,
		// 12.8s, exactly representable after Round(100ms) so the rendered text is exact.
		FinishedAt: start.Add(12*time.Second + 800*time.Millisecond),
	}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeSuccess})

	want := "✓ completed (12.8s)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestProviderCallHeartbeat_SilentByDefault_PrintsInVerbose(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.ProviderCallHeartbeat(30 * time.Second)
	if buf.String() != "" {
		t.Errorf("expected no heartbeat output outside --verbose, got %q", buf.String())
	}

	r.Verbose = true
	r.ProviderCallHeartbeat(30 * time.Second)

	want := "   … still running (30s)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestProviderCallFinished_SilentByDefault_PrintsInVerboseOnSuccessOnly(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.ProviderCallFinished("claude", "sonnet", 72400*time.Millisecond, nil)
	if buf.String() != "" {
		t.Errorf("expected no completion output outside --verbose, got %q", buf.String())
	}

	r.Verbose = true
	r.ProviderCallFinished("claude", "sonnet", 72400*time.Millisecond, errors.New("boom"))
	if buf.String() != "" {
		t.Errorf("expected no completion output on failure, got %q", buf.String())
	}

	r.ProviderCallFinished("claude", "sonnet", 72400*time.Millisecond, nil)
	want := "✓ completed (72.4s)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRunHeader_PrintsIDAndDir(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.RunHeader("2f3c8b41-abc", "/home/user/src/orch/.orchestrator/runs/2f3c8b41-abc")

	want := "Run: 2f3c8b41-abc  (/home/user/src/orch/.orchestrator/runs/2f3c8b41-abc)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestCallReporter_ProviderCallStarted_PrintsShortLine(t *testing.T) {
	var buf bytes.Buffer
	c := CallReporter{ConsoleReporter: NewConsoleReporter(&buf)}

	c.ProviderCallStarted("claude", "sonnet")

	want := "→ claude (model sonnet)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_TestSuccess_PrintsCommandAndPassed(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	exec := domain.StepExecution{
		Action:     domain.ActionTest,
		TestResult: &domain.TestResult{Command: "go test ./...", Success: true},
	}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeSuccess})

	want := "Command: go test ./...\n✓ passed\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_ReviewSuccess_PrintsPass(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	exec := domain.StepExecution{Action: domain.ActionReview}
	r.StepFinished(&domain.Run{}, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeSuccess})

	want := "✓ PASS\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_TestFailure_PrintsFixIterationNotice(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.MaxFixIterations = 3

	run := &domain.Run{FixIterations: 1}
	exec := domain.StepExecution{
		Action:     domain.ActionTest,
		TestResult: &domain.TestResult{Command: "go test ./...", Success: false},
	}
	r.StepFinished(run, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeFailure})

	want := "Command: go test ./...\n✗ test failed\n\nReturning to implementation (iteration 2/3).\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepFinished_ReviewFailure_PrintsIssueCountsAndFixIterationNotice(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.MaxFixIterations = 5

	run := &domain.Run{FixIterations: 0}
	exec := domain.StepExecution{
		Action: domain.ActionReview,
		ReviewResult: &domain.ReviewResult{Issues: []domain.ReviewIssue{
			{Severity: domain.SeverityHigh},
			{Severity: domain.SeverityHigh},
			{Severity: domain.SeverityLow},
		}},
	}
	r.StepFinished(run, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeFailure})

	want := "✗ review failed\n\n2 high severity issues found.\n1 low severity issue found.\nReturning to implementation (iteration 1/5).\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRunFinished_Completed(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.RunFinished(&domain.Run{Status: domain.RunCompleted})

	want := "\nRun completed successfully.\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRunFinished_Failed(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.RunFinished(&domain.Run{Status: domain.RunFailed, FailureReason: "provider execution failed"})

	want := "\nRun failed.\nReason: provider execution failed\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRunFinished_Stopped(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.RunFinished(&domain.Run{Status: domain.RunStopped, FailureReason: "maximum steps exceeded"})

	want := "\nRun stopped.\nReason: maximum steps exceeded\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestAnnouncementHelpers(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.ValidatingConfig()
	r.ConfigValid()
	r.Deciding()

	want := "Validating configuration...\n" +
		"✓ configuration valid\n" +
		"\nDeciding the workflow...\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestDirtyRepoWarning_PrintsExactText(t *testing.T) {
	var buf bytes.Buffer
	DirtyRepoWarning(&buf)

	want := "Warning: repository contains uncommitted changes.\nExisting changes will not be reverted.\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
