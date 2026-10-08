package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"orchestrator/internal/testutil/fakeexec"
)

// Scenario 32 (MVP.md §43): a run that ended records each step's call report and an
// implement's diff from git in run.json, and renders summary.md from it.

type summaryRunJSON struct {
	Executions []struct {
		Action string `json:"action"`
		Usage  *struct {
			InputTokens int64 `json:"input_tokens"`
			Partial     bool  `json:"partial"`
		} `json:"usage"`
		Call *struct {
			PromptBytes int            `json:"prompt_bytes"`
			Models      []any          `json:"models"`
			Tools       map[string]int `json:"tools"`
		} `json:"call"`
		Diff *struct {
			Step struct {
				Files      int `json:"files"`
				Insertions int `json:"insertions"`
			} `json:"step"`
			Run *struct {
				Files int `json:"files"`
			} `json:"run"`
			NotReported []string `json:"not_reported"`
		} `json:"diff"`
	} `json:"executions"`
	Outcome struct {
		DurationMS int64 `json:"duration_ms"`
		ActiveMS   int64 `json:"active_ms"`
		Usage      struct {
			PartialCalls int `json:"partial_calls"`
		} `json:"usage"`
		Diff *struct {
			Files int `json:"files"`
		} `json:"diff"`
	} `json:"outcome"`
}

func readSummaryRun(t *testing.T, repoDir string) summaryRunJSON {
	t.Helper()
	data, ok := runFile(t, repoDir, "run.json")
	if !ok {
		t.Fatal("run.json missing")
	}
	var run summaryRunJSON
	if err := json.Unmarshal([]byte(data), &run); err != nil {
		t.Fatalf("run.json: %v", err)
	}
	return run
}

func TestScenario32_RunSummary(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	stdout, stderr, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts), "run", russianRequest)
	mustSucceed(t, stdout, stderr, err)

	run := readSummaryRun(t, repoDir)
	var sawImplement bool
	for _, e := range run.Executions {
		if e.Action == "test" {
			if e.Call != nil {
				t.Errorf("test makes no provider call, yet records %+v", e.Call)
			}
			continue
		}
		if e.Call == nil || e.Call.PromptBytes == 0 {
			t.Errorf("%s: call = %+v, want the prompt's size", e.Action, e.Call)
		}
		if e.Action == "implement" {
			sawImplement = true
			d := e.Diff
			if d == nil || d.Step.Files != 1 || d.Step.Insertions == 0 || d.Run == nil || d.Run.Files != 1 || len(d.NotReported) != 0 {
				t.Errorf("implement diff = %+v", d)
			}
		} else if e.Diff != nil {
			t.Errorf("%s records a diff", e.Action)
		}
	}
	if !sawImplement {
		t.Fatal("no implement step ran")
	}
	if o := run.Outcome; o.ActiveMS < 0 || o.ActiveMS > o.DurationMS || o.Diff == nil || o.Diff.Files != 1 {
		t.Errorf("outcome = %+v", o)
	}

	summary, ok := runFile(t, repoDir, "summary.md")
	if !ok {
		t.Fatal("summary.md missing")
	}
	for _, want := range []string{
		"| Status | completed |",
		"| Cost | $0.9400 |",
		"| Code change | 1 file, +",
		"## Steps",
		"| implement | completed |",
		"## Code changes",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary.md lacks %q\n%s", want, summary)
		}
	}
}

// A call that dies before its final report (here: exits mid-stream) still records the
// tokens its messages streamed, marked partial, and the summary says the cost is a lower
// bound.
func TestScenario32_PartialUsageOfAnInterruptedCall(t *testing.T) {
	repoDir := fakeexec.InitGitFixture(t)
	usageEnv(t, repoDir)
	t.Setenv("FAKE_FAIL_ACTION", "implement")
	t.Setenv("FAKE_FAIL_EXIT_CODE", "1")
	t.Setenv("FAKE_FAIL_STDOUT", `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":7,"output_tokens":1},"content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}`+"\n")

	opts := defaultConfigOptions()
	opts.providers.Decision = "claude"
	if _, _, err := runOrchestrator(t, repoDir, writeConfig(t, t.TempDir(), opts), "run", russianRequest); err == nil {
		t.Fatal("the run must fail when implement fails")
	}

	run := readSummaryRun(t, repoDir)
	last := run.Executions[len(run.Executions)-1]
	if last.Action != "implement" || last.Usage == nil || !last.Usage.Partial || last.Usage.InputTokens != 7 ||
		last.Call == nil || last.Call.Tools["Read"] != 1 || last.Diff != nil {
		t.Errorf("failed implement = %+v (usage %+v, call %+v)", last, last.Usage, last.Call)
	}
	if run.Outcome.Usage.PartialCalls != 1 {
		t.Errorf("partial_calls = %d, want 1", run.Outcome.Usage.PartialCalls)
	}
	summary, _ := runFile(t, repoDir, "summary.md")
	for _, want := range []string{"or more: 1 call ended without a report", "implement ended without the CLI's final report"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary.md lacks %q\n%s", want, summary)
		}
	}
}
