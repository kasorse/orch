package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/domain"
)

func f64(v float64) *float64 { return &v }

func reportRun() domain.Run {
	t0 := time.Date(2026, 10, 23, 10, 15, 0, 0, time.UTC)
	sz := 0.84
	return domain.Run{
		ID:     "20261023-101500-ab12cd",
		Task:   domain.Task{Description: "PAY-12: Add returns the difference\nIt must add.", Source: &domain.TaskSource{Kind: domain.TaskSourceJira, Key: "PAY-12", URL: "https://jira.example.com/browse/PAY-12"}},
		Status: domain.RunCompleted, FixIterations: 1, Commit: "f350cf6904486bb4f28085f45169d78cc420c54c", PreRunRef: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d",
		Worktree: &domain.RunWorktree{Branch: "orchestrator/20261023-101500-ab12cd", BaseRef: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d", BaseBranch: "main"},
		Decision: &domain.Decision{
			Source: domain.DecisionSourceLLM,
			Complexity: domain.ComplexityOutcome{Primary: domain.ComplexitySourceChoice, Selected: domain.ComplexityMedium,
				Sources: map[domain.ComplexitySource]domain.ComplexitySourceOutcome{domain.ComplexitySourceChoice: {Size: &sz, Selected: domain.ComplexityMedium}},
				Cuts:    &domain.ComplexityCuts{Medium: 0.6, Complex: 1.3}},
			Stages: domain.StageSelection{Stages: []domain.StageOutcome{
				{Stage: domain.StageResearch, Enabled: true, Probability: f64(0.91), Threshold: f64(0.81), Reason: "0.91 ≥ 0.81"},
				{Stage: domain.StageDevelopment, Enabled: true, Reason: "forced | by medium"},
				{Stage: domain.StageReview, Enabled: false, Reason: "0.10 < 0.81"},
			}},
		},
		Executions: []domain.StepExecution{
			{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted, StartedAt: t0, FinishedAt: t0.Add(time.Minute),
				Usage: &domain.Usage{CostUSD: 0.5},
				Diff: &domain.StepDiff{Step: domain.DiffStat{Files: 1}, Run: &domain.DiffStat{Files: 3, Insertions: 42, Deletions: 7, Binary: 1},
					RunFiles: []domain.FileStat{{Path: "calc/add.go", Insertions: 3, Deletions: 1}, {Path: "calc/add_test.go", Insertions: 39, Deletions: 6}, {Path: "docs/logo.png", Binary: true}}}},
			{Index: 1, Action: domain.ActionTest, Status: domain.StepCompleted, StartedAt: t0.Add(time.Minute), FinishedAt: t0.Add(2 * time.Minute),
				TestResult: &domain.TestResult{Command: "go test ./...", ExitCode: 1, FailedTests: []string{"TestAdd"}, Checks: []domain.CheckResult{{Name: "vet", Success: true}}}},
			{Index: 2, Action: domain.ActionImplement, Status: domain.StepCompleted, StartedAt: t0.Add(2 * time.Minute), FinishedAt: t0.Add(3 * time.Minute),
				Usage: &domain.Usage{CostUSD: 0.2},
				Diff: &domain.StepDiff{Step: domain.DiffStat{Files: 1}, Run: &domain.DiffStat{Files: 3, Insertions: 42, Deletions: 7, Binary: 1},
					RunFiles: []domain.FileStat{{Path: "calc/add.go", Insertions: 3, Deletions: 1}, {Path: "calc/add_test.go", Insertions: 39, Deletions: 6}, {Path: "docs/logo.png", Binary: true}}}},
			{Index: 3, Action: domain.ActionTest, Status: domain.StepCompleted, StartedAt: t0.Add(3 * time.Minute), FinishedAt: t0.Add(4 * time.Minute),
				TestResult: &domain.TestResult{Command: "go test ./...", Success: true, Checks: []domain.CheckResult{{Name: "vet", Success: true}, {Name: "lint", Success: true}}}},
			{Index: 4, Action: domain.ActionReview, Status: domain.StepCompleted, StartedAt: t0.Add(4 * time.Minute), FinishedAt: t0.Add(5 * time.Minute),
				Usage: &domain.Usage{CostUSD: 0.14},
				ReviewResult: &domain.ReviewResult{Status: domain.ReviewPass,
					Issues: []domain.ReviewIssue{
						{Severity: domain.SeverityLow, File: "calc/add.go:12", Description: "Comment is stale"},
						{Severity: domain.SeverityMedium, File: "calc/add.go:20", Description: "No error path"},
						{Severity: domain.SeverityLow, Description: "Name is odd"}},
					Criteria: []domain.CriterionVerdict{{Index: 1, Met: true, Evidence: "a"}, {Index: 2, Met: true, Evidence: "b"}, {Index: 3, Met: true, Evidence: "c"}}}},
		},
	}
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q\n%s", want, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Errorf("report has %q\n%s", u, got)
		}
	}
}

func TestRunReport_Completed(t *testing.T) {
	trace := &domain.DecisionTrace{Source: domain.DecisionSourceLLM, Provider: "claude", Model: "haiku"}
	got := RunReport(reportRun(), trace)
	want := strings.Join([]string{
		"## PAY-12: Add returns the difference",
		"",
		"**Status:** completed · **Review:** pass (1 medium, 2 low) · **Tests:** passed · **Checks:** vet ✓ lint ✓",
		"",
		"### Task",
		"",
		"> PAY-12: Add returns the difference",
		"> It must add.",
		"",
		"Source: [PAY-12](https://jira.example.com/browse/PAY-12)",
		"",
		"### Why these stages",
		"",
		"complexity **medium** (size 0.84; cuts 0.60 / 1.30) · decided by llm (haiku)",
		"",
		"| stage | on | why |",
		"|---|---|---|",
		"| research | ✓ | 0.91 ≥ 0.81 |",
		"| development | ✓ | forced \\| by medium |",
		"| review | — | 0.10 \\< 0.81 |",
		"",
		"### What changed",
		"",
		"3 files, +42 −7 (1 binary) against `main@1a2b3c4`",
		"",
		"| file | + | − |",
		"|---|---|---|",
		"| `calc/add.go` | 3 | 1 |",
		"| `calc/add_test.go` | 39 | 6 |",
		"| `docs/logo.png` | binary | |",
		"",
		"### Tests and checks",
		"",
		"last test step: passed (`go test ./...`), checks vet, lint passed",
		"",
		"Fix iterations: 1 (the first test step failed: `TestAdd`)",
		"",
		"### Review",
		"",
		"verdict **pass** · criteria 3/3 met",
		"",
		"- medium · `calc/add.go:20` · No error path",
		"- low · `calc/add.go:12` · Comment is stale",
		"- low · Name is odd",
		"",
		"---",
		"Run `20261023-101500-ab12cd` · branch `orchestrator/20261023-101500-ab12cd` · commit `f350cf6` · restore point `1a2b3c4` · cost $0.8400 · 5m 0s active",
		"",
	}, "\n")
	if got != want {
		t.Errorf("report differs\n--- got\n%s\n--- want\n%s", got, want)
	}
	if again := RunReport(reportRun(), trace); again != got {
		t.Error("the same record rendered two different reports")
	}
}

func TestRunReport_FailedOnTests(t *testing.T) {
	run := reportRun()
	run.Status, run.FailureReason, run.Commit = domain.RunFailed, "max fix iterations reached\nsecond line", ""
	run.Executions = run.Executions[:2]
	run.Executions[1].TestResult.Checks = []domain.CheckResult{{Name: "vet", Success: true}, {Name: "lint", ExitCode: 2}}
	run.Executions[1].TestResult.FailedCheck = "lint"
	run.Executions[1].TestResult.ExitCode = 2
	run.Executions[1].TestResult.FailedTests = nil
	got := RunReport(run, nil)
	mustContain(t, got,
		"**Status:** failed: max fix iterations reached",
		"**Tests:** failed",
		"**Checks:** vet ✓ lint ✗",
		"last test step: failed at check `lint` (`go test ./...`, exit 2), checks vet passed",
		"Fix iterations: 1 (the first test step failed: check `lint`)",
	)
	mustNotContain(t, got, "### Review", "second line", "commit `")
}

func TestRunReport_PathRulesAndSecret(t *testing.T) {
	run := reportRun()
	run.Executions[2].PathRules = []domain.PathRuleMatch{
		{Match: "migrations/**", Files: []string{"migrations/1.sql", "migrations/2.sql"},
			RequireStages: []domain.Stage{domain.StageReview, domain.StageTesting}, Missing: []domain.Stage{domain.StageTesting}, Approval: domain.PathApprovalManual},
	}
	run.SecretFindings = []domain.SecretFinding{{StepIndex: 2, File: "cfg.go", Line: 3, Rule: "aws-access-key", Fingerprint: "0123456789abcdef-fingerprint"}}
	run.Worktree.Conflicts = []string{"calc/add.go"}
	got := RunReport(run, nil)
	mustContain(t, got,
		"### Needs attention",
		"- policy.paths requires manual approval: `migrations/**` (2 files)",
		"- policy.paths `migrations/**` requires testing, which the workflow does not run",
		"- a likely secret was added at `cfg.go:3` (aws-access-key)",
		"- a merge into `main` would conflict in: `calc/add.go`",
	)
	mustNotContain(t, got, "0123456789abcdef-fingerprint")
}

func TestRunReport_StageRun(t *testing.T) {
	run := reportRun()
	d, err := domain.ManualDecision([]domain.Stage{domain.StageTesting, domain.StageReview})
	if err != nil {
		t.Fatal(err)
	}
	run.Decision = &d
	run.Worktree, run.Task.Source, run.Executions = nil, nil, run.Executions[3:]
	run.Executions[0].Index, run.Executions[1].Index = 0, 1
	run.FixIterations = 0
	got := RunReport(run, &domain.DecisionTrace{Source: domain.DecisionSourceManual})
	mustContain(t, got, "stages chosen by the operator (`run --stage`)", "| testing | ✓ |", "| review | ✓ |", "### Review")
	mustNotContain(t, got, "complexity", "decided by", "### What changed", "Source:", "branch `", "Fix iterations")
}

// A review run has "Reviewed" in place of "What changed" and no "Why these stages", no branch
// and no restore point (specs/delivery/SPEC.md §5).
func TestRunReport_ReviewRun(t *testing.T) {
	run := reportRun()
	d, err := domain.ManualDecision([]domain.Stage{domain.StageTesting, domain.StageReview})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("b", 40)
	run.Decision = &d
	run.Task = domain.Task{Description: "Review the change from main to feature. Its commits:\n- Add the feature"}
	run.Commit = ""
	run.PreRunRef = head
	run.Worktree = &domain.RunWorktree{BaseRef: head, Removed: true}
	run.ReviewTarget = &domain.ReviewTarget{Base: strings.Repeat("a", 40), Head: head, MergeBase: strings.Repeat("c", 40),
		BaseRefName: "main", HeadRefName: "feature"}
	run.Executions = run.Executions[3:]
	run.Executions[0].Index, run.Executions[1].Index = 0, 1
	run.FixIterations = 0

	got := RunReport(run, &domain.DecisionTrace{Source: domain.DecisionSourceManual})
	mustContain(t, got,
		"### Reviewed",
		"`main...feature` · merge base `ccccccc` · head `bbbbbbb`",
		"### Review",
		"verdict **pass**",
		"Run `20261023-101500-ab12cd` · cost",
	)
	mustNotContain(t, got, "### Why these stages", "### What changed", "branch `", "restore point", "commit `")
}

func TestRunReport_ByteBound(t *testing.T) {
	run := reportRun()
	var task []string
	for i := 0; i < 100; i++ {
		task = append(task, fmt.Sprintf("line %d %s", i, strings.Repeat("x", 900)))
	}
	run.Task.Description = strings.Join(task, "\n")
	files := make([]domain.FileStat, domain.MaxRunFiles)
	for i := range files {
		files[i] = domain.FileStat{Path: fmt.Sprintf("dir/%03d/%s.go", i, strings.Repeat("p", 400)), Insertions: i, Deletions: 1}
	}
	run.Executions[2].Diff.RunFiles = files
	run.Executions[2].Diff.Run.Files = len(files)
	var issues []domain.ReviewIssue
	for i := 0; i < 100; i++ {
		issues = append(issues, domain.ReviewIssue{Severity: domain.SeverityLow, File: "a.go", Description: strings.Repeat("y", 600)})
	}
	run.Executions[4].ReviewResult.Issues = issues

	got := RunReport(run, nil)
	if len(got) > maxReportBytes {
		t.Fatalf("report is %d bytes, at most %d", len(got), maxReportBytes)
	}
	mustContain(t, got, "## PAY-12", "### Review", "---\nRun `20261023-101500-ab12cd`", "more lines)")
	if strings.Count(got, "dir/") >= len(files) {
		t.Error("the file list was not cut")
	}

	// Even a report whose fixed parts are too long is cut to the bound at a line.
	run.Executions[1].TestResult.Command = strings.Repeat("c", 70_000)
	run.Executions[3].TestResult.Command = strings.Repeat("c", 70_000)
	got = RunReport(run, nil)
	if len(got) > maxReportBytes || !strings.HasSuffix(got, "\n…(cut)\n") {
		t.Errorf("report is %d bytes, ends %q", len(got), got[max(0, len(got)-20):])
	}
}

func TestRunReport_Escapes(t *testing.T) {
	run := reportRun()
	run.Task.Description = "<img src=x> [link](http://x)\n/merge\n```\n@all #1 !2 ~bug %m1"
	run.Executions[4].ReviewResult.Issues = []domain.ReviewIssue{{Severity: domain.SeverityLow, File: "a`b.go", Description: "![i](x) @all\n/close"}}
	got := RunReport(run, nil)
	mustContain(t, got, `> \<img src=x\> \[link\](http://x)`, "\n>  /merge\n", "> \\`\\`\\`\n", `> \@all \#1 \!2 \~bug \%m1`, "`a'b.go`", `\!\[i\](x) \@all /close`)
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "/") || strings.HasPrefix(line, "```") {
			t.Errorf("line %q survives as Markdown", line)
		}
	}
}
