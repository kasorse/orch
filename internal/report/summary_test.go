package report

import (
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

func summaryRun() domain.Run {
	t0 := time.Date(2026, 10, 7, 22, 14, 15, 0, time.FixedZone("CEST", 2*3600))
	pass := true
	return domain.Run{
		ID:        "f95f569b-04f3-4a0a-a7df-5211d42e7380",
		Task:      domain.Task{Description: "Fix the retry | loop\nsecond line"},
		Status:    domain.RunCompleted,
		CreatedAt: t0,
		Workflow:  &domain.Workflow{Steps: []domain.Step{{Action: domain.ActionImplement}, {Action: domain.ActionTest}, {Action: domain.ActionReview}}},
		Decision:  &domain.Decision{Source: domain.DecisionSourceLLM, Complexity: domain.ComplexityOutcome{Selected: domain.ComplexityMedium}},
		Commit:    "f350cf6904486bb4f28085f45169d78cc420c54c",
		Executions: []domain.StepExecution{
			{Index: 0, Action: domain.ActionImplement, Status: domain.StepFailed, StartedAt: t0, FinishedAt: t0.Add(10 * time.Minute),
				Provider: &domain.ProviderRef{Provider: "claude", Model: "sonnet"},
				Usage:    &domain.Usage{InputTokens: 18, CacheReadTokens: 529331, OutputTokens: 85, Turns: 9, Partial: true},
				Call:     &domain.CallReport{PromptBytes: 16623, Models: []domain.ModelUse{{Model: "claude-sonnet-5-5"}}, Tools: map[string]int{"Read": 17, "Write": 1, "Grep": 3, "Glob": 3}},
				Error:    "provider execution failed\n\nprovider: claude\nfailure: timeout (exceeded actions.implement.timeout = 10m0s)\ncommand: claude ..."},
			{Index: 1, Action: domain.ActionImplement, Status: domain.StepCompleted, Attempt: 1, StartedAt: t0.Add(70 * time.Minute), FinishedAt: t0.Add(75 * time.Minute),
				Provider: &domain.ProviderRef{Provider: "claude", Model: "sonnet"},
				Usage:    &domain.Usage{InputTokens: 38, OutputTokens: 40315, CacheReadTokens: 1374052, CacheCreationTokens: 110786, CostUSD: 1.1211804, Turns: 41},
				Call: &domain.CallReport{PromptBytes: 20480, Truncated: []domain.PromptPart{domain.PromptTestOutput}, APIMS: 183_000,
					Models: []domain.ModelUse{{Model: "claude-sonnet-5-5", CostUSD: 1.1}}, Tools: map[string]int{"Edit": 7}, MCPFailed: []string{"graphify"}},
				Diff: &domain.StepDiff{Step: domain.DiffStat{Files: 3, Insertions: 120, Deletions: 4}, Run: &domain.DiffStat{Files: 6, Insertions: 420, Deletions: 35, Binary: 1},
					NotReported: []string{"internal/x.go"}, ReportedUnchanged: []string{"ghost.go"}}},
			{Index: 2, Action: domain.ActionTest, Status: domain.StepCompleted, Attempt: 1, StartedAt: t0.Add(75 * time.Minute), FinishedAt: t0.Add(75*time.Minute + 3200*time.Millisecond),
				TestResult: &domain.TestResult{Success: pass}},
			{Index: 3, Action: domain.ActionReview, Status: domain.StepCompleted, Attempt: 1, StartedAt: t0.Add(76 * time.Minute), FinishedAt: t0.Add(77 * time.Minute),
				Usage:        &domain.Usage{InputTokens: 14, OutputTokens: 3588, CostUSD: 0.72, Turns: 13},
				ReviewResult: &domain.ReviewResult{Status: domain.ReviewPass, Issues: []domain.ReviewIssue{{Severity: domain.SeverityLow}}}},
		},
	}
}

func TestRunSummary(t *testing.T) {
	trace := &domain.DecisionTrace{Source: domain.DecisionSourceLLM, Provider: "claude", Model: "opus",
		Latency: domain.DecisionLatency{ClassificationMS: 7470},
		Usage:   &domain.DecisionUsage{Classification: &domain.Usage{InputTokens: 10, CostUSD: 0.065}}}
	got := RunSummary(summaryRun(), trace)
	for _, want := range []string{
		"# Run f95f569b-04f3-4a0a-a7df-5211d42e7380\n",
		`| Task | Fix the retry \| loop |`,
		"| Started | 2026-10-07 22:14:15 +0200 |",
		"| Status | completed |",
		"| Workflow | implement → test → review |",
		"| Decision | llm, claude opus, complexity medium |",
		"| Steps | 4 (fix iterations 0) |",
		"| Wall time | 1h 17m |",
		"| Active time | 16m 3s |",
		"| Decision time | 7.5s |",
		"| Cost | $1.9062 or more: 1 call ended without a report, their cost is unknown |",
		"| Decision cost | $0.0650 (1 call) |",
		"| Provider calls | 4 |",
		"| Tests (last run) | passed |",
		"| Review verdict | pass |",
		"| Code change | 6 files, +420 −35 (1 binary) |",
		"| Commit | f350cf6904486bb4f28085f45169d78cc420c54c |",
		"| 0 | implement | failed | 10m 0s | — | unknown | 529.3k | ≥ 85 | 9 | claude-sonnet-5-5 | 16.2 KiB |",
		"| 1 | implement (resume 1) | completed | 5m 0s | 3m 3s | $1.1212 | 1.48M | 40.3k | 41 | claude-sonnet-5-5 | 20.0 KiB, cut: test_output |",
		"| 2 | test (resume 1) | passed | 3.2s | — | — | — | — | — | — | — |",
		"| 3 | review (resume 1) | pass (1 issue) | 1m 0s | — | $0.7200 | 14 | 3.6k | 13 | — | — |",
		"| 0 | implement | 24 | Read 17, Glob 3, Grep 3, Write 1 |",
		"| 1 | 3 files, +120 −4 | 6 files, +420 −35 (1 binary) | internal/x.go | ghost.go |",
		"- #0 implement ended without the CLI's final report",
		"- #1 implement ran without MCP server(s) graphify: the CLI could not start them",
		"- #0 implement failed: failure: timeout (exceeded actions.implement.timeout = 10m0s)\n",
		"MVP.md §43",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "second line") {
		t.Error("the task excerpt must be its first line only")
	}
}

func TestRunSummary_ReviewCriteria(t *testing.T) {
	run := summaryRun()
	run.Executions[3].ReviewResult = &domain.ReviewResult{Status: domain.ReviewPass, Criteria: []domain.CriterionVerdict{
		{Index: 1, Met: true, Evidence: "a"}, {Index: 2, Met: false, Evidence: "b"}, {Index: 3, Met: true, Evidence: "c"},
	}}
	got := RunSummary(run, nil)
	if want := "| fail (0 issues, 2/3 criteria met) |"; !strings.Contains(got, want) {
		t.Errorf("summary lacks %q\n%s", want, got)
	}
}

func TestRunSummary_Reviewers(t *testing.T) {
	if got := RunSummary(summaryRun(), nil); strings.Contains(got, "Reviewers") {
		t.Errorf("a single review has no reviewers row\n%s", got)
	}

	run := summaryRun()
	r := run.Executions[3].ReviewResult
	r.Reviewers = []domain.ReviewerAnswer{{Model: "sonnet", Result: *r}, {Model: "opus", Result: *r}}
	run.Executions[3].Call = &domain.CallReport{ExtraCalls: []domain.ExtraCall{{Model: "opus"}}}
	got := RunSummary(run, nil)
	for _, want := range []string{"| Reviewers | sonnet, opus |", "| Provider calls | 4 |"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q\n%s", want, got)
		}
	}
}

func TestRunSummary_FailedCheck(t *testing.T) {
	run := summaryRun()
	run.Executions[2].TestResult = &domain.TestResult{Command: "go vet ./...", ExitCode: 1, FailedCheck: "vet",
		Checks: []domain.CheckResult{{Name: "build", Success: true}, {Name: "vet", ExitCode: 1}}}
	got := RunSummary(run, nil)
	for _, want := range []string{"| Tests (last run) | failed (check vet) |", "| test (resume 1) | failed (check vet, exit 1) |"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q\n%s", want, got)
		}
	}
}

func TestRunSummary_BaselineSplit(t *testing.T) {
	run := summaryRun()
	if got := RunSummary(run, nil); strings.Contains(got, "| Failing tests |") {
		t.Errorf("a run without a red pre-run test has no Failing tests row\n%s", got)
	}

	run.PreRunTest = &domain.TestResult{ExitCode: 1, FailedTests: []string{"p.TestA", "p.TestB"}, ReportFormat: "gotext"}
	run.Executions[2].TestResult = &domain.TestResult{ExitCode: 1, FailedTests: []string{"p.TestA", "p.TestC", "p.TestD"}, ReportFormat: "gotext"}
	got := RunSummary(run, nil)
	if want := "| Failing tests | 1 failing before the run, 2 new |"; !strings.Contains(got, want) {
		t.Errorf("summary lacks %q\n%s", want, got)
	}

	run.Executions[2].TestResult.ReportFormat = "pytest"
	if got := RunSummary(run, nil); strings.Contains(got, "| Failing tests |") {
		t.Errorf("names in different formats must not be split\n%s", got)
	}

	run.Executions[2].TestResult = &domain.TestResult{Success: true}
	if got := RunSummary(run, nil); strings.Contains(got, "| Failing tests |") {
		t.Errorf("a green last test has no split\n%s", got)
	}
}

func TestRunSummary_Verify(t *testing.T) {
	run := summaryRun()
	if got := RunSummary(run, nil); strings.Contains(got, "| Verify |") {
		t.Errorf("a run without verify calls has no Verify row\n%s", got)
	}
	run.Executions[1].VerifyCalls = []domain.VerifyCall{
		{Tool: domain.VerifyToolRunTests, Success: true},
		{Tool: domain.VerifyToolRunTests, ExitCode: 1},
		{Tool: domain.VerifyToolRunCheck, Check: "vet", Success: true},
		{Tool: domain.VerifyToolRunCheck, Check: "vet", Success: true},
		{Tool: domain.VerifyToolRunTests, Refused: "limit reached"},
	}
	got := RunSummary(run, nil)
	if want := "| Verify | 5 calls, 3 passed |"; !strings.Contains(got, want) {
		t.Errorf("summary lacks %q\n%s", want, got)
	}
}

// A run recorded before the call report existed still renders: the configured model
// stands in, and what is unknown is a dash.
func TestRunSummary_OldRun(t *testing.T) {
	run := summaryRun()
	for i := range run.Executions {
		run.Executions[i].Call, run.Executions[i].Diff = nil, nil
	}
	run.Decision = &domain.Decision{Source: domain.DecisionSourceManual}
	got := RunSummary(run, nil)
	for _, want := range []string{"| Decision | manual (run --stage) |", "| Code change | — |", "sonnet (configured)"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Tool calls") || strings.Contains(got, "## Code changes") {
		t.Errorf("empty sections must be left out\n%s", got)
	}
	if strings.Contains(got, "## Path rules") {
		t.Errorf("a run without path rules has no such section\n%s", got)
	}
}

// The path rules section is the last implement's matches; an earlier implement's are not shown.
func TestRunSummary_PathRules(t *testing.T) {
	run := summaryRun()
	run.Executions[0].PathRules = []domain.PathRuleMatch{{Match: "old/**", Files: []string{"old/a.go"}, Missing: []domain.Stage{}}}
	run.Executions[1].PathRules = []domain.PathRuleMatch{
		{Match: "migrations/**", Files: []string{"migrations/0042_users.sql", "migrations/0043_x.sql"},
			RequireStages: []domain.Stage{domain.StageReview, domain.StageTesting}, Missing: []domain.Stage{domain.StageTesting}, Approval: domain.PathApprovalManual},
		{Match: "docs/**", Files: []string{"docs/a.md"}, Missing: []domain.Stage{}},
	}
	got := RunSummary(run, nil)
	for _, want := range []string{
		"\n## Path rules\n\n| Rule | Files | Requires | Missing | Approval |\n| --- | --- | --- | --- | --- |\n",
		"| migrations/** | migrations/0042_users.sql, migrations/0043_x.sql | review, testing | testing | manual |\n",
		"| docs/** | docs/a.md | — | — | — |\n",
		"| Approval | Commit needs manual approval (policy.paths) |\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "old/**") {
		t.Errorf("an earlier implement's rules are shown\n%s", got)
	}

	// The last implement matched nothing: no section, even though an earlier one did.
	run.Executions[1].PathRules = nil
	if got := RunSummary(run, nil); strings.Contains(got, "## Path rules") || strings.Contains(got, "manual approval") {
		t.Errorf("the last implement matched nothing\n%s", got)
	}
}
