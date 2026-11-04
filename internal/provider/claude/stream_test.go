package claude

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
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
	res, err := newLogin(fake).Execute(context.Background(), provider.Request{
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
	res, err := newLogin(fake).Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
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
	_, err := newLogin(fake).Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
	if err == nil || !strings.Contains(err.Error(), "overloaded_error") {
		t.Errorf("expected the API error text in %v", err)
	}
	var pe *apperr.ProviderError
	if !errors.As(err, &pe) || pe.APIStatus != 529 {
		t.Errorf("APIStatus = %+v, want 529", pe)
	}
}

// The account's usage limit is its own failure kind, with the reset time; any other
// rate_limit refusal (a throttle, an org policy) carries no api_error and stays an exit.
func TestExecute_UsageLimitIsARateLimitFailure(t *testing.T) {
	limited := `{"type":"assistant","message":{"id":"m1","content":[]},"error":"rate_limit","api_error":"usage_limit_reached","api_error_params":{"rate_limit_info":{"status":"rejected","resetsAt":1999999999,"rateLimitType":"five_hour"}}}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your limit","num_turns":1}`
	throttled := `{"type":"assistant","message":{"id":"m1","content":[]},"error":"rate_limit"}
{"type":"result","subtype":"success","is_error":true,"result":"Request rejected (429)","num_turns":1}`
	for name, tc := range map[string]struct {
		out    string
		kind   apperr.FailureKind
		resets time.Time
	}{
		"usage limit": {limited, apperr.FailureRateLimit, time.Unix(1999999999, 0)},
		"throttle":    {throttled, apperr.FailureExit, time.Time{}},
	} {
		fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: tc.out, ExitCode: 1}}}
		_, err := newLogin(fake).Execute(context.Background(), provider.Request{Prompt: "p", Model: "m", Authority: domain.AuthorityNone})
		var pe *apperr.ProviderError
		if !errors.As(err, &pe) || pe.Kind != tc.kind || !pe.ResetsAt.Equal(tc.resets) {
			t.Errorf("%s: got %+v, want kind %s resets %v", name, pe, tc.kind, tc.resets)
		}
	}
}

func TestAPIErrorStatus(t *testing.T) {
	for text, want := range map[string]int{
		"API Error: 400 litellm.BadRequestError: You passed in model=x": 400,
		"API Error: 529 overloaded_error":                               529,
		"API Error: 401":                                                401,
		"  API Error: 429 rate\n":                                       429,
		"API Error: 4000 x":                                             0,
		"API Error: abc":                                                0,
		"The answer mentions API Error: 400":                            0,
		"":                                                              0,
	} {
		if got := apiErrorStatus(text); got != want {
			t.Errorf("apiErrorStatus(%q) = %d, want %d", text, got, want)
		}
	}
	// Only an error result carries a status: an answer that starts with the phrase is an answer.
	if rep := parseStream(`{"type":"result","is_error":false,"result":"API Error: 400 x"}`); rep.apiStatus != 0 {
		t.Errorf("a successful result must not carry an API status: %d", rep.apiStatus)
	}
}

// The result event of claude 2.1.293 also says which models answered, what each cost, and
// how long the call waited on the API; tool calls are counted from the assistant messages.
func TestParseStream_ReportsModelsAPITimeAndTools(t *testing.T) {
	stdout := `{"type":"system","subtype":"init","model":"claude-sonnet-5-5","mcp_servers":[{"name":"graphify","status":"failed"},{"name":"mem","status":"connected"}]}
{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":2,"output_tokens":3},"content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}
{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":2,"output_tokens":9},"content":[{"type":"tool_use","id":"t2","name":"Read","input":{}}]}}
{"type":"assistant","message":{"id":"m2","model":"claude-sonnet-5-5","content":[{"type":"tool_use","id":"t3","name":"Edit","input":{}},{"type":"tool_use","id":"t3","name":"Edit","input":{}}]}}
{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.5,"num_turns":3,"duration_api_ms":4200,"usage":{"input_tokens":10,"output_tokens":50},"modelUsage":{"claude-sonnet-5-5":{"costUSD":0.45},"claude-haiku-5-5":{"costUSD":0.05}}}
`
	rep := parseStream(stdout)
	if !rep.ok || rep.text != "done" || rep.usage == nil || rep.usage.Partial || rep.usage.OutputTokens != 50 || rep.usage.CostUSD != 0.5 {
		t.Fatalf("report = %+v, usage %+v", rep, rep.usage)
	}
	c := rep.call
	if c == nil || c.APIMS != 4200 {
		t.Fatalf("call = %+v", c)
	}
	wantModels := []domain.ModelUse{{Model: "claude-haiku-5-5", CostUSD: 0.05}, {Model: "claude-sonnet-5-5", CostUSD: 0.45}}
	if !reflect.DeepEqual(c.Models, wantModels) {
		t.Errorf("models = %+v, want %+v", c.Models, wantModels)
	}
	// A tool_use block repeated under the same id is one call.
	if want := map[string]int{"Read": 2, "Edit": 1}; !reflect.DeepEqual(c.Tools, want) {
		t.Errorf("tools = %v, want %v", c.Tools, want)
	}
	// A server that did not start is recorded; a connected one is not.
	if !reflect.DeepEqual(c.MCPFailed, []string{"graphify"}) {
		t.Errorf("mcp_failed = %v, want [graphify]", c.MCPFailed)
	}
}

// A call killed before its result event (a timeout) still reports what its streamed
// messages add up to, marked partial: each message once, at its largest counters.
func TestParseStream_PartialUsageWithoutResult(t *testing.T) {
	stdout := `{"type":"system","subtype":"init","model":"claude-sonnet-5-5"}
{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":100,"cache_creation_input_tokens":40},"content":[{"type":"thinking","thinking":""}]}}
{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":2,"output_tokens":7,"cache_read_input_tokens":100,"cache_creation_input_tokens":40},"content":[{"type":"tool_use","id":"t1","name":"Write","input":{}}]}}
{"type":"user","message":{"content":[{"type":"tool_result"}]}}
{"type":"assistant","message":{"id":"m2","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":200,"cache_creation_input_tokens":5},"content":[{"type":"text","text":"half"}]}}
{"type":"assistant","message":{"id":"m3","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_to`
	rep := parseStream(stdout)
	if rep.ok {
		t.Fatal("a stream without a result event must not give an answer")
	}
	want := domain.Usage{InputTokens: 3, OutputTokens: 9, CacheReadTokens: 300, CacheCreationTokens: 45, Turns: 2, Partial: true}
	if rep.usage == nil || *rep.usage != want {
		t.Fatalf("usage = %+v, want %+v", rep.usage, want)
	}
	if c := rep.call; c == nil || !reflect.DeepEqual(c.Models, []domain.ModelUse{{Model: "claude-sonnet-5-5"}}) || c.Tools["Write"] != 1 || c.APIMS != 0 {
		t.Errorf("call = %+v", rep.call)
	}
}

// A timeout keeps the raw stream as the output (for the logs) and still reports the
// partial usage with the error.
func TestExecute_TimeoutReportsPartialUsage(t *testing.T) {
	stream := `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":4,"output_tokens":1},"content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}` + "\n"
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: stream, ExitCode: -1}},
		Errs:    []error{context.DeadlineExceeded},
	}
	res, err := newLogin(fake).Execute(context.Background(), provider.Request{Prompt: "p", Model: "sonnet", Authority: domain.AuthorityRead})
	if err == nil {
		t.Fatal("expected the timeout")
	}
	if res.Output != stream {
		t.Errorf("Output = %q, want the raw stream", res.Output)
	}
	if res.Usage == nil || !res.Usage.Partial || res.Usage.InputTokens != 4 || res.Call == nil || res.Call.Tools["Read"] != 1 {
		t.Errorf("usage = %+v, call = %+v", res.Usage, res.Call)
	}
}
