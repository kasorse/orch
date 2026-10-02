package claude

import (
	"context"
	"strings"
	"testing"

	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

// A trimmed real transcript from claude 2.1.287 (--output-format stream-json --verbose).
const transcript = `{"type":"system","subtype":"init","cwd":"/repo","session_id":"s"}
{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/repo/package.json"}}]}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.01,"resetsAt":1790990400},"seven_day":{"utilization":0.42,"resetsAt":1791342000}}}}
{"type":"user","message":{"content":[{"type":"tool_result"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"The name is **demo**."}]}}
{"type":"result","subtype":"success","is_error":false,"result":"The name is **demo**.","total_cost_usd":0.0057,"num_turns":2,"usage":{"input_tokens":10,"cache_creation_input_tokens":200,"cache_read_input_tokens":4000,"output_tokens":96}}
`

func TestExecute_ParsesStreamJSON(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: transcript}}}
	var activity []string
	res, err := New(fake).Execute(context.Background(), provider.Request{
		Prompt: "p", Model: "haiku", WorkingDir: "/repo", Authority: domain.AuthorityRead,
		Activity: func(s string) { activity = append(activity, s) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "The name is **demo**." {
		t.Errorf("Output = %q, want the result text only", res.Output)
	}
	u := res.Usage
	if u == nil || u.OutputTokens != 96 || u.CacheReadTokens != 4000 || u.PromptTokens() != 4210 || u.CostUSD != 0.0057 || u.Turns != 2 {
		t.Fatalf("usage = %+v", u)
	}
	if u.Limits == nil || u.Limits.FiveHour.Utilization != 0.01 || u.Limits.Weekly.Utilization != 0.42 || u.Limits.Weekly.ResetsAt.Unix() != 1791342000 {
		t.Errorf("limits = %+v", u.Limits)
	}
	if got := strings.Join(activity, "|"); got != "Thinking|Read package.json|Writing" {
		t.Errorf("activity = %q", got)
	}
}

// A CLI or test double that prints plain text is taken as the answer, unchanged.
func TestExecute_PlainOutputPassesThrough(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: `{"summary":"x"}`}}}
	res, err := New(fake).Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != `{"summary":"x"}` || res.Usage != nil {
		t.Errorf("got %q, %+v", res.Output, res.Usage)
	}
}

// An API error arrives as a failing result event; its text must reach the ProviderError.
func TestExecute_ErrorResultTextIsTheStdout(t *testing.T) {
	out := `{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 overloaded_error","num_turns":1}`
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: out, ExitCode: 1}}}
	_, err := New(fake).Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	if err == nil || !strings.Contains(err.Error(), "overloaded_error") {
		t.Errorf("expected the API error text in %v", err)
	}
}
