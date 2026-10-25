package logging

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func TestStepStarted_PrintsNumberedUppercaseHeader(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.StepStarted(&domain.Run{}, 0, domain.ActionPlan)

	want := "\n[1] PLAN\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestStepStarted_VerboseNamesAuthority(t *testing.T) {
	for action, want := range map[domain.ActionType]string{
		domain.ActionImplement: "\n[2] IMPLEMENT\nAuthority: edit\n",
		domain.ActionTest:      "\n[2] TEST\n",
	} {
		var buf bytes.Buffer
		r := NewConsoleReporter(&buf)
		r.Verbose = true
		r.StepStarted(&domain.Run{}, 1, action)
		if buf.String() != want {
			t.Errorf("%s: got %q, want %q", action, buf.String(), want)
		}
	}
}

func TestRenderAuthority(t *testing.T) {
	steps := func(actions ...domain.ActionType) domain.Workflow {
		var wf domain.Workflow
		for _, a := range actions {
			wf.Steps = append(wf.Steps, domain.Step{Action: a})
		}
		return wf
	}
	cases := []struct {
		wf   domain.Workflow
		want string
	}{
		{
			steps(domain.ActionResearch, domain.ActionPlan, domain.ActionImplement, domain.ActionTest, domain.ActionReview),
			"authority: research=web, plan=read, implement=edit, review=read; test runs: go test ./... " + notSandboxed + "\n",
		},
		{steps(domain.ActionPlan), "authority: plan=read\n"},
		{steps(domain.ActionTest), "authority: test runs: go test ./... — NOT sandboxed: network, $HOME and your files are reachable\n"},
	}
	var sandboxed bytes.Buffer
	RenderAuthority(&sandboxed, steps(domain.ActionImplement, domain.ActionTest), "go test ./...", true)
	if want := "authority: implement=edit; test runs (sandboxed): go test ./...\n"; sandboxed.String() != want {
		t.Errorf("got %q, want %q", sandboxed.String(), want)
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		RenderAuthority(&buf, tc.wf, "go test ./...", false)
		if buf.String() != tc.want {
			t.Errorf("got %q, want %q", buf.String(), tc.want)
		}
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

func TestStepFinished_PlanSuccess_PrintsCompletedWithDuration(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	start := time.Now()
	exec := domain.StepExecution{
		Action:    domain.ActionPlan,
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

func TestWorktree_PrintsPathBranchAndBase(t *testing.T) {
	const sha = "8d0e4b2a1b2c3d4e5f60718293a4b5c6d7e8f901"
	for _, tc := range []struct{ baseBranch, want string }{
		{"master", "Worktree: /state/wt/r1 (orchestrator/r1 from master@8d0e4b2)\n"},
		{"", "Worktree: /state/wt/r1 (orchestrator/r1 from 8d0e4b2)\n"},
	} {
		var buf bytes.Buffer
		NewConsoleReporter(&buf).Worktree("/state/wt/r1", "orchestrator/r1", tc.baseBranch, sha)
		if buf.String() != tc.want {
			t.Errorf("got %q, want %q", buf.String(), tc.want)
		}
	}
}

func TestWorktreeCommitted_PrintsBaseAndMerge(t *testing.T) {
	const sha = "8d0e4b2a1b2c3d4e5f60718293a4b5c6d7e8f901"
	for _, tc := range []struct{ baseBranch, want string }{
		{"master", "Based on master@8d0e4b2. To take it:\n  git merge orchestrator/r1\n"},
		{"", "Based on 8d0e4b2. To take it:\n  git merge orchestrator/r1\n"},
	} {
		var buf bytes.Buffer
		NewConsoleReporter(&buf).WorktreeCommitted("orchestrator/r1", tc.baseBranch, sha)
		if buf.String() != tc.want {
			t.Errorf("got %q, want %q", buf.String(), tc.want)
		}
	}
}

func TestWorktreeConflicts_ListsAtMostTwentyFiles(t *testing.T) {
	var buf bytes.Buffer
	NewConsoleReporter(&buf).WorktreeConflicts("orchestrator/r1", "master", []string{"a.go"})
	if want := "Warning: merging orchestrator/r1 into master will conflict in 1 file:\n  a.go\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}

	var files []string
	for i := range 23 {
		files = append(files, fmt.Sprintf("f%02d.go", i))
	}
	buf.Reset()
	NewConsoleReporter(&buf).WorktreeConflicts("orchestrator/r1", "master", files)
	got := buf.String()
	if !strings.HasPrefix(got, "Warning: merging orchestrator/r1 into master will conflict in 23 files:\n  f00.go\n") {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(got, "  f19.go\n  ... and 3 more\n") || strings.Contains(got, "f20.go") {
		t.Errorf("want 20 files and the rest counted, got %q", got)
	}
}

func TestWorktreeKept_AndNotes(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.WorktreeKept("/state/wt/r1", "")
	r.WorktreeKept("/state/wt/r1", "git worktree remove failed: untracked files\nmore")
	r.MergeTreeUnsupported("master")
	r.ConflictCheckSkipped("master", "the git config changed")
	want := "The worktree stays at /state/wt/r1\n" +
		"The worktree stays at /state/wt/r1 (git worktree remove failed: untracked files)\n" +
		"Note: git older than 2.38 cannot check for conflicts with master (merge-tree --write-tree); the check was skipped\n" +
		"Warning: no conflict check against master: the git config changed\n"
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

func TestStepFinished_ReviewFixable_ExplainsTheReturnToImplement(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)
	r.MaxFixIterations = 5

	run := &domain.Run{FixIterations: 0}
	exec := domain.StepExecution{
		Action: domain.ActionReview,
		ReviewResult: &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{
			{Severity: domain.SeverityMedium},
		}},
	}
	r.StepFinished(run, 0, exec, workflow.StepOutcome{Outcome: workflow.OutcomeFixable})

	want := "✓ PASS\n\n1 medium severity issue found.\nreview.fix_medium is on: fixing the medium issues.\nReturning to implementation (iteration 1/5).\n"
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

func TestRunFinished_PrintsRunID(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.RunFinished(&domain.Run{ID: "run-123", Status: domain.RunFailed, FailureReason: "boom"})

	want := "\nRun failed.\nReason: boom\nRun ID: run-123\n"
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

func TestOrgConfig_PrintsTheSourceOrNone(t *testing.T) {
	var buf bytes.Buffer
	r := NewConsoleReporter(&buf)

	r.OrgConfig("/etc/orchestrator/org.yaml")
	r.OrgConfig("")

	want := "✓ org config: /etc/orchestrator/org.yaml\n✓ org config: none\n"
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
