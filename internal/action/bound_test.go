package action

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
)

// BoundTestOutput itself is tested in package testexec; the tests here cover how the
// prompts use it.

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s line %04d\n", prefix, i)
	}
	return b.String()
}

func TestBoundTestOutput_ReachableFromAction(t *testing.T) {
	out, errText, omitted := BoundTestOutput("ok\n", "warn\n", 64)
	if out != "ok\n" || errText != "warn\n" || omitted.Any() {
		t.Errorf("got %q, %q, %+v", out, errText, omitted)
	}
	var o Omitted
	if o.Any() {
		t.Error("a zero Omitted reports a cut")
	}
}

func TestExecuteImplement_BoundsTestOutputToTheConfiguredLimit(t *testing.T) {
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	d := newDispatcher(client)
	limit := config.MinMaxTestOutputBytes
	d.Config.Limits.Prompt.MaxTestOutputBytes = &limit
	stdout := lines("noise", 2000) + "--- FAIL: TestFoo\n" + lines("noise", 2000)
	d.pendingTestFailure = &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: stdout}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompt := client.lastReq.Prompt
	if c := outcome.Call; c == nil || c.PromptBytes != len(prompt) || !slices.Equal(c.Truncated, []domain.PromptPart{domain.PromptTestOutput}) {
		t.Errorf("call = %+v, want the prompt's size and the test output marked cut", outcome.Call)
	}
	if !strings.Contains(prompt, "--- FAIL: TestFoo") {
		t.Error("the failure line was cut from the prompt")
	}
	if !strings.Contains(prompt, "The test output above was shortened") {
		t.Error("the prompt does not say the output was shortened")
	}
	if len(prompt) > 2*limit+4096 {
		t.Errorf("prompt is %d bytes for a %d-byte test output limit", len(prompt), limit)
	}
}

func TestExecuteImplement_ShortTestOutputIsNotAnnotated(t *testing.T) {
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	d := newDispatcher(client)
	d.pendingTestFailure = &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "FAIL: TestFoo"}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	outcome, err := d.Execute(context.Background(), domain.ActionImplement, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(client.lastReq.Prompt, "was shortened") {
		t.Error("an output within the default limit was annotated as shortened")
	}
	if outcome.Call == nil || len(outcome.Call.Truncated) != 0 {
		t.Errorf("call = %+v, want nothing marked cut", outcome.Call)
	}
}

// nonceRE matches the random part of fence and task block delimiters, which differs
// between any two prompts by design.
var nonceRE = regexp.MustCompile(`(UNTRUSTED-[A-Z]+-|TASK-)[A-Za-z0-9]+`)

func TestRestore_ResumedImplementPromptBoundsTheSameWay(t *testing.T) {
	stdout := lines("noise", 3000) + "--- FAIL: TestFoo\n" + lines("noise", 3000)
	fail := domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: stdout, Stderr: "exit status 1\n"}

	live := newDispatcher(&fakeClient{})
	live.applyImplementation(domain.ImplementationArtifact{Summary: "i1"})
	live.applyTest(fail)

	// What a resume reads back: the test result as run.json holds it.
	data, err := json.Marshal(fail)
	if err != nil {
		t.Fatal(err)
	}
	var stored domain.TestResult
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	run := &domain.Run{ID: "r", Executions: []domain.StepExecution{
		{Index: 0, Action: domain.ActionImplement, Status: domain.StepCompleted},
		{Index: 1, Action: domain.ActionTest, Status: domain.StepCompleted, TestResult: &stored},
	}}
	resumed := newDispatcher(&fakeClient{})
	if err := resumed.Restore(run, memReader{memKey(0, domain.ActionImplement): `{"summary":"i1","changed_files":null,"tests_added":null}`}); err != nil {
		t.Fatal(err)
	}

	prompt := func(d *Dispatcher) string {
		p := BuildImplementPrompt(ImplementPromptInput{
			Task: "fix it", PreviousImplementation: d.lastImpl, LastTestResult: d.pendingTestFailure,
			MaxTestOutput: d.Config.Limits.Prompt.TestOutputBytes(),
		})
		return nonceRE.ReplaceAllString(p, "${1}N")
	}
	a, b := prompt(live), prompt(resumed)
	if a != b {
		t.Error("the resumed implement prompt differs from the live one")
	}
	if !strings.Contains(a, "was shortened") || len(a) > config.DefaultMaxTestOutputBytes+4096 {
		t.Errorf("the test output was not bounded (prompt %d bytes)", len(a))
	}
}

func TestPreRunFailure_ShownToImplementUntilTheFirstTest(t *testing.T) {
	red := domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "--- FAIL: TestAdd\n"}
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	d := newDispatcher(client)
	d.applyPreRunTest(red)

	run := &domain.Run{ID: "r", Task: domain.Task{Description: "make TestAdd pass"}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.lastReq.Prompt, "The test command already FAILS before any change") ||
		!strings.Contains(client.lastReq.Prompt, "--- FAIL: TestAdd") {
		t.Errorf("first implement lacks the red pre-run test:\n%s", client.lastReq.Prompt)
	}

	// After a test step its own result speaks; the pre-run output is not repeated.
	d.applyTest(domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "--- FAIL: TestOther\n"})
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.lastReq.Prompt, "already FAILS before any change") {
		t.Error("the pre-run output was repeated after a test step")
	}

	// A green pre-run test shows nothing.
	d2 := newDispatcher(client)
	d2.applyPreRunTest(domain.TestResult{Command: "go test ./...", Success: true})
	if _, err := d2.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.lastReq.Prompt, "already FAILS") {
		t.Error("a green pre-run test was shown to implement")
	}
}

// A retry after a failed test still has the red pre-run names to split from the current ones,
// although the pre-run output itself is no longer shown (QL09). On a resume they come back
// from run.json, and a run recorded without names gets the plain line.
func TestImplementRetry_SplitsOldAndNewFailures(t *testing.T) {
	pre := domain.TestResult{Command: "go test ./...", ExitCode: 1, FailedTests: []string{"p.TestA", "p.TestB"}, ReportFormat: "gotext"}
	cur := domain.TestResult{Command: "go test ./...", ExitCode: 1, FailedTests: []string{"p.TestA", "p.TestC"}, ReportFormat: "gotext"}
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	run := &domain.Run{ID: "r", Task: domain.Task{Description: "t"}}

	d := newDispatcher(client)
	d.applyPreRunTest(pre)
	d.applyTest(cur)
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Failing before this run started (the task may be to fix them): p.TestA\n",
		"New failures since the run started: p.TestC\n",
	} {
		if !strings.Contains(client.lastReq.Prompt, want) {
			t.Errorf("the retry prompt lacks %q:\n%s", want, client.lastReq.Prompt)
		}
	}
	if strings.Contains(client.lastReq.Prompt, "already FAILS before any change") {
		t.Error("the pre-run output was repeated after a test step")
	}

	// A pre-run result recorded without names degrades to the plain line.
	old := newDispatcher(client)
	old.applyPreRunTest(domain.TestResult{Command: "go test ./...", ExitCode: 1})
	old.applyTest(cur)
	if _, err := old.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.lastReq.Prompt, "New failures") || !strings.Contains(client.lastReq.Prompt, "Failing tests (parsed from the output): p.TestA, p.TestC") {
		t.Errorf("a pre-run test without names must give the plain line:\n%s", client.lastReq.Prompt)
	}
}

func TestRestore_KeepsTheRedPreRunTest(t *testing.T) {
	red := &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: "FAIL"}
	run := &domain.Run{ID: "r", PreRunTest: red, AllowRedTests: true, Executions: []domain.StepExecution{
		{Index: 0, Action: domain.ActionInvestigate, Status: domain.StepCompleted},
	}}
	d := &Dispatcher{}
	if err := d.Restore(run, memReader{memKey(0, domain.ActionInvestigate): `{"summary":"a","steps":null,"affected_files":null,"risks":null}`}); err != nil {
		t.Fatal(err)
	}
	if d.preRunFailureForImplement() == nil {
		t.Error("a resumed run lost the red pre-run test its first implement needs")
	}
}
