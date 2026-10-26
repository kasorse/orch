package integration

import (
	"encoding/json"
	"math"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 30 (specs/benchmark/tasks/TASK-BM02): a run that reached a terminal status says
// in run.json's outcome how it ended and what it cost; a resume recomputes it over the
// whole history.

type outcomeJSON struct {
	Status           string `json:"status"`
	FailureReason    string `json:"failure_reason"`
	FixIterations    int    `json:"fix_iterations"`
	TestsPassedFinal *bool  `json:"tests_passed_final"`
	ReviewVerdict    string `json:"review_verdict"`
	DurationMS       int64  `json:"duration_ms"`
	Usage            struct {
		recordedUsage
		Calls int `json:"calls"`
	} `json:"usage"`
	DecisionOverhead struct {
		CostUSD float64 `json:"cost_usd"`
		MS      int64   `json:"ms"`
		Calls   int     `json:"calls"`
	} `json:"decision_overhead"`
}

func readOutcome(t *testing.T, repoDir string) (status string, o outcomeJSON) {
	t.Helper()
	data, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json missing")
	}
	var run struct {
		ID      string       `json:"id"`
		Status  string       `json:"status"`
		Outcome *outcomeJSON `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(data), &run); err != nil {
		t.Fatalf("run.json: %v", err)
	}
	if run.Outcome == nil {
		t.Fatalf("run.json has no outcome:\n%s", data)
	}
	return run.Status, *run.Outcome
}

// russianRequest is translated, so all three decision rounds (translation, context depth,
// classification) make a provider call.
const russianRequest = "исправь Add() в fixture.go, чтобы она складывала, а не вычитала"

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestScenario30_OutcomeOfCompletedRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	stdout, stderr, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts),
		"run", russianRequest)
	mustSucceed(t, stdout, stderr, err)

	status, o := readOutcome(t, repoDir)
	if status != "completed" || o.Status != "completed" || o.FailureReason != "" || o.FixIterations != 0 {
		t.Errorf("outcome = %+v, run status %s", o, status)
	}
	if o.TestsPassedFinal == nil || !*o.TestsPassedFinal || o.ReviewVerdict != "pass" {
		t.Errorf("tests_passed_final = %v, review_verdict = %q", o.TestsPassedFinal, o.ReviewVerdict)
	}
	// plan + implement + review, and the three decision calls.
	if o.Usage.Calls != 6 || !near(o.Usage.CostUSD, 0.11+0.33+0.44+0.01+0.02+0.03) ||
		o.Usage.InputTokens != 60 || o.Usage.OutputTokens != 120 || o.Usage.CacheReadTokens != 1800 {
		t.Errorf("usage = %+v", o.Usage)
	}
	if o.DecisionOverhead.Calls != 3 || !near(o.DecisionOverhead.CostUSD, 0.06) || o.DecisionOverhead.MS < 0 {
		t.Errorf("decision_overhead = %+v", o.DecisionOverhead)
	}
	if o.DurationMS < 0 {
		t.Errorf("duration_ms = %d", o.DurationMS)
	}

	// The strict loader reads what was written back.
	stdout, stderr, err = runOrchestrator(t, repoDir, "/nonexistent/orchestrator.yaml", "decisions", "report")
	mustSucceed(t, stdout, stderr, err)
}

func TestScenario30_OutcomeOfFailedRun(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDOUT", streamResult(t, "API Error: overloaded", 0.07))

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	if _, _, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts), "run", russianRequest); err == nil {
		t.Fatal("the run must fail when implement fails")
	}

	status, o := readOutcome(t, repoDir)
	if status != "failed" || o.Status != "failed" || o.FailureReason == "" {
		t.Errorf("outcome = %+v, run status %s", o, status)
	}
	if o.TestsPassedFinal != nil || o.ReviewVerdict != "" {
		t.Errorf("no test or review ran: tests_passed_final = %v, review_verdict = %q", o.TestsPassedFinal, o.ReviewVerdict)
	}
	// plan, the failed implement, and the three decision calls.
	if o.Usage.Calls != 5 || !near(o.Usage.CostUSD, 0.11+0.07+0.06) {
		t.Errorf("usage = %+v", o.Usage)
	}
}

func TestScenario30_ResumedOutcomeCoversBothAttempts(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDOUT", streamResult(t, "API Error: overloaded", 0.07))

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	cfg := writeConfig(t, t.TempDir(), opts)
	if _, _, err := runOrchestrator(t, repoDir, cfg, "run", russianRequest); err == nil {
		t.Fatal("the run must fail when implement fails")
	}
	if status, o := readOutcome(t, repoDir); status != "failed" || o.Status != "failed" {
		t.Fatalf("first attempt: outcome = %+v, run status %s", o, status)
	}

	t.Setenv("FAKE_FAIL_ACTION", "")
	run := loadResumedRun(t, repoDir)
	stdout, stderr, err := resume(t, repoDir, cfg, run.ID)
	mustSucceed(t, stdout, stderr, err)

	status, o := readOutcome(t, repoDir)
	if status != "completed" || o.Status != "completed" || o.FailureReason != "" {
		t.Errorf("outcome = %+v, run status %s", o, status)
	}
	if o.TestsPassedFinal == nil || !*o.TestsPassedFinal || o.ReviewVerdict != "pass" {
		t.Errorf("tests_passed_final = %v, review_verdict = %q", o.TestsPassedFinal, o.ReviewVerdict)
	}
	// plan, the failed implement, the retried implement, review, and the decision calls:
	// the money spent before the resume is still spent.
	if o.Usage.Calls != 7 || !near(o.Usage.CostUSD, 0.11+0.07+0.33+0.44+0.06) {
		t.Errorf("usage = %+v", o.Usage)
	}
	if o.DecisionOverhead.Calls != 3 || !near(o.DecisionOverhead.CostUSD, 0.06) {
		t.Errorf("decision_overhead = %+v", o.DecisionOverhead)
	}
}
