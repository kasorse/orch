package logging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

const resumeRunID = "3f2a9c1e-8d4b-4c6a-9e21-5b7f0a3c9d10"

func resumeWorkflow() *domain.Workflow {
	return &domain.Workflow{Steps: []domain.Step{
		{Action: domain.ActionPlan, Stage: domain.StagePlanning},
		{Action: domain.ActionImplement, Stage: domain.StageDevelopment},
		{Action: domain.ActionTest, Stage: domain.StageTesting},
		{Action: domain.ActionReview, Stage: domain.StageReview},
	}}
}

func aiStep(index int, action domain.ActionType, attempt int) domain.StepExecution {
	return domain.StepExecution{
		Index: index, Action: action, Status: domain.StepCompleted, Attempt: attempt,
		Provider: &domain.ProviderRef{Provider: "claude", Model: "sonnet", Authority: domain.AuthorityFor(action)},
	}
}

func testStep(index, exitCode, attempt int) domain.StepExecution {
	return domain.StepExecution{
		Index: index, Action: domain.ActionTest, Status: domain.StepCompleted, Attempt: attempt,
		TestResult: &domain.TestResult{Command: "go test ./...", ExitCode: exitCode, Success: exitCode == 0},
	}
}

const providerFailure = "provider execution failed\n\nprovider: claude\naction: implement\nmodel: sonnet\nfailure: timeout"

func TestResumeOverview_Golden(t *testing.T) {
	failedImplement := aiStep(1, domain.ActionImplement, 0)
	failedImplement.Status, failedImplement.Error = domain.StepFailed, providerFailure

	review := aiStep(3, domain.ActionReview, 0)
	review.ReviewResult = &domain.ReviewResult{Status: domain.ReviewPass, Summary: "two problems", Issues: []domain.ReviewIssue{
		{Severity: domain.SeverityHigh, File: "retry.go", Description: "no backoff cap"},
		{Severity: domain.SeverityLow, File: "retry.go", Description: "naming"},
	}}

	cases := []struct {
		name      string
		run       domain.Run
		tree      string
		treeErr   error
		entry     domain.ActionType
		why       string
		remaining []domain.ActionType
	}{
		{
			name: "resume_interrupted",
			run: domain.Run{Status: domain.RunRunning, Executions: []domain.StepExecution{
				aiStep(0, domain.ActionPlan, 0), aiStep(1, domain.ActionImplement, 0),
			}},
			tree:  " M internal/retry/retry.go\n?? internal/retry/retry_404_test.go",
			entry: domain.ActionTest, why: "continue after implement",
			remaining: []domain.ActionType{domain.ActionTest, domain.ActionReview},
		},
		{
			name: "resume_failed_implement",
			run: domain.Run{Status: domain.RunFailed, FailureReason: providerFailure, Executions: []domain.StepExecution{
				aiStep(0, domain.ActionPlan, 0), failedImplement,
			}},
			treeErr: errors.New("git status failed: fatal: index file corrupt\nmore"),
			entry:   domain.ActionImplement, why: "retry implement (failed: provider execution failed)",
			remaining: []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview},
		},
		{
			name: "resume_limit_raised",
			run: domain.Run{Status: domain.RunStopped, FailureReason: "maximum fix iterations exceeded", Executions: []domain.StepExecution{
				aiStep(0, domain.ActionPlan, 0), aiStep(1, domain.ActionImplement, 0),
				testStep(2, 0, 0), review,
				aiStep(4, domain.ActionImplement, 1), testStep(5, 1, 1),
			}},
			tree:  " M internal/retry/retry.go",
			entry: domain.ActionImplement, why: "tests failed, retrying implement",
			remaining: []domain.ActionType{domain.ActionImplement, domain.ActionTest, domain.ActionReview},
		},
		{
			name: "resume_jira_source",
			run: domain.Run{Status: domain.RunPending, Task: domain.Task{Description: "PMC-2789: ...", Source: &domain.TaskSource{
				Kind: domain.TaskSourceJira, Key: "PMC-2789", URL: "https://example.atlassian.net/browse/PMC-2789",
				// Local, so the golden file reads 10:12 in any time zone.
				FetchedAt: time.Date(2026, 9, 30, 10, 12, 30, 0, time.Local),
			}}},
			entry: domain.ActionPlan, why: "start",
			remaining: []domain.ActionType{domain.ActionPlan, domain.ActionImplement, domain.ActionTest, domain.ActionReview},
		},
		{
			name: "resume_story_source",
			run: domain.Run{Status: domain.RunPending, Task: domain.Task{Description: "RF02: ...", Acceptance: []string{"one"}, Source: &domain.TaskSource{
				Kind: domain.TaskSourceStory, Story: "refunds", Task: "RF02", Attempt: 1, Jira: "PAY-124",
				Branch: "story/refunds/RF02", BaseRef: strings.Repeat("a", 40), BaseBranch: "main",
			}}},
			entry: domain.ActionPlan, why: "start",
			remaining: []domain.ActionType{domain.ActionPlan, domain.ActionImplement, domain.ActionTest, domain.ActionReview},
		},
		{
			name:  "resume_plan_mode",
			run:   domain.Run{Status: domain.RunPending, Mode: domain.ModePlan},
			entry: domain.ActionPlan, why: "start",
			remaining: []domain.ActionType{domain.ActionPlan, domain.ActionImplement, domain.ActionTest, domain.ActionReview},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := tc.run
			run.ID, run.Workflow = resumeRunID, resumeWorkflow()

			// The order resumeTask prints in; --verbose must not change any of it.
			render := func(verbose bool) string {
				var buf bytes.Buffer
				r := NewConsoleReporter(&buf)
				r.Verbose = verbose
				r.ResumeHeader(&run, ".orchestrator/runs/"+resumeRunID)
				r.History(&run)
				r.WorkingTree(tc.tree, tc.treeErr)
				r.NextStep(tc.entry, tc.why, tc.remaining)
				return buf.String()
			}
			got := render(false)
			if verbose := render(true); verbose != got {
				t.Errorf("--verbose changed the overview:\n%s", verbose)
			}

			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (go test ./internal/logging -update): %v", err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s:\n%s", path, got)
			}
		})
	}
}

func TestAlreadyCompleted(t *testing.T) {
	var buf bytes.Buffer
	NewConsoleReporter(&buf).AlreadyCompleted(resumeRunID)
	if want := "Run " + resumeRunID + " already completed. Nothing to resume.\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
