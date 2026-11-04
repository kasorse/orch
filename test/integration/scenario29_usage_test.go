package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/testutil/fakeexec"
)

// Scenario 29 (specs/benchmark/tasks/TASK-BM01): what each provider call cost is recorded
// - every step's call in run.json, every decision call in decision.json - whether the call
// succeeded or not.

// streamResult wraps an answer the way claude --output-format stream-json ends a call: a
// result event carrying the text, the cost and the token counts.
func streamResult(t *testing.T, text string, cost float64) string {
	t.Helper()
	ev := map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": text,
		"total_cost_usd": cost, "num_turns": 1,
		"usage": map[string]int{"input_tokens": 10, "output_tokens": 20, "cache_read_input_tokens": 300},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return `{"type":"rate_limit_event","rate_limit_info":{"rateLimitType":"five_hour","utilization":0.5,"resetsAt":1999999999}}` + "\n" + string(data) + "\n"
}

type recordedUsage struct {
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
	CostUSD         float64 `json:"cost_usd"`
	Turns           int     `json:"turns"`
}

type usageRunJSON struct {
	Executions []struct {
		Action string         `json:"action"`
		Status string         `json:"status"`
		Usage  *recordedUsage `json:"usage"`
	} `json:"executions"`
}

func readUsageRun(t *testing.T, repoDir string) usageRunJSON {
	t.Helper()
	data, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json missing")
	}
	var run usageRunJSON
	if err := json.Unmarshal([]byte(data), &run); err != nil {
		t.Fatalf("run.json: %v", err)
	}
	return run
}

func usageEnv(t *testing.T, repoDir string) {
	t.Helper()
	decisionEnv(t, repoDir)
	dir := os.Getenv("FAKE_RESPONSES_DIR")
	t.Setenv("JEV_API_KEY", "")
	for name, r := range map[string]struct {
		text string
		cost float64
	}{
		"translate":      {"Fix Add() in fixture.go so that it adds instead of subtracting", 0.01},
		"decide-context": {`{"context_depth": {"none": 0.9, "project": 0.1}, "clarity": 0.9}`, 0.02},
		"decide-classify": {`{"complexity": {"simple": 0.1, "medium": 0.7, "complex": 0.2},
 "complexity_score": {"0": 0.1, "1": 0.7, "2": 0.2},
 "stages": {"research": 0.05, "development": 0.95},
 "scope": 0.2}`, 0.03},
		"investigate": {readOnlyResponse, 0.11},
		"implement":   {implementResponse, 0.33},
		"review":      {reviewPassResponse, 0.44},
	} {
		writeResponse(t, dir, name, streamResult(t, r.text, r.cost))
	}
}

func TestScenario29_UsageRecorded(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	stdout, stderr, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts),
		"run", "исправь Add() в fixture.go, чтобы она складывала, а не вычитала")
	mustSucceed(t, stdout, stderr, err)

	wantCost := map[string]float64{"investigate": 0.11, "implement": 0.33, "review": 0.44}
	run := readUsageRun(t, repoDir)
	if len(run.Executions) != 4 {
		t.Fatalf("executions = %+v", run.Executions)
	}
	for _, e := range run.Executions {
		if e.Action == "test" {
			if e.Usage != nil {
				t.Errorf("the test step makes no provider call, usage = %+v", e.Usage)
			}
			continue
		}
		if e.Usage == nil || e.Usage.CostUSD != wantCost[e.Action] || e.Usage.InputTokens != 10 ||
			e.Usage.OutputTokens != 20 || e.Usage.CacheReadTokens != 300 || e.Usage.Turns != 1 {
			t.Errorf("%s usage = %+v, want cost %v", e.Action, e.Usage, wantCost[e.Action])
		}
	}

	data, _ := runFile(t, repoDir, "decision.json")
	var trace struct {
		Usage map[string]recordedUsage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &trace); err != nil {
		t.Fatalf("decision.json: %v", err)
	}
	for name, cost := range map[string]float64{"translation": 0.01, "context_depth": 0.02, "classification": 0.03} {
		if u, ok := trace.Usage[name]; !ok || u.CostUSD != cost || u.InputTokens != 10 {
			t.Errorf("decision usage %s = %+v (present %v), want cost %v", name, u, ok, cost)
		}
	}

	// The account's rate-limit state is shown, never recorded.
	for _, f := range []string{"run.json", "decision.json"} {
		body, _ := runFile(t, repoDir, f)
		if strings.Contains(body, "limits") || strings.Contains(body, "five_hour") || strings.Contains(body, "utilization") {
			t.Errorf("%s records rate limits:\n%s", f, body)
		}
	}

	// What was written is read back by the strict loader (resume, decisions report).
	stdout, stderr, err = runOrchestrator(t, repoDir, "/nonexistent/orchestrator.yaml", "decisions", "report")
	mustSucceed(t, stdout, stderr, err)
	if strings.Contains(stdout, "unreadable") || !strings.Contains(stdout, "traces: 1") {
		t.Errorf("the strict loader must read the recorded trace back:\n%s", stdout)
	}
}

func TestScenario29_FailedCallUsageRecorded(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDOUT", streamResult(t, "API Error: overloaded", 0.07))

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	_, _, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts), "run", "fix Add in fixture.go")
	if err == nil {
		t.Fatal("the run must fail when implement fails")
	}

	run := readUsageRun(t, repoDir)
	last := run.Executions[len(run.Executions)-1]
	if last.Action != "implement" || last.Status != "failed" {
		t.Fatalf("last execution = %+v", last)
	}
	if last.Usage == nil || last.Usage.CostUSD != 0.07 {
		t.Errorf("a failed call's usage = %+v, want cost 0.07", last.Usage)
	}
}
