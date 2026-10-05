package action

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"orchestrator/internal/config"
	"orchestrator/internal/domain"
)

// markerRE matches the in-place notes boundStream writes for a cut.
var markerRE = regexp.MustCompile(`\[\.\.\. (\d+ lines, )?\d+ bytes omitted \.\.\.\]\n`)

// withoutMarkers is the kept output alone, for byte accounting.
func withoutMarkers(s string) string { return markerRE.ReplaceAllString(s, "") }

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s line %04d\n", prefix, i)
	}
	return b.String()
}

func TestBoundTestOutput_UnderBudgetIsUnchanged(t *testing.T) {
	out, errText, omitted := BoundTestOutput("ok\n", "warn\n", 64)
	if out != "ok\n" || errText != "warn\n" || omitted.Any() {
		t.Errorf("got %q, %q, %+v", out, errText, omitted)
	}
}

func TestBoundTestOutput_ExactBudgetIsUnchanged(t *testing.T) {
	stdout, stderr := strings.Repeat("a", 32), strings.Repeat("b", 32)
	out, errText, omitted := BoundTestOutput(stdout, stderr, 64)
	if out != stdout || errText != stderr || omitted.Any() {
		t.Errorf("an exact fit was cut: %+v", omitted)
	}
}

func TestBoundTestOutput_EmptyStreamLendsItsShare(t *testing.T) {
	stdout := lines("out", 100) // 1500 bytes
	out, errText, omitted := BoundTestOutput(stdout, "", 2000)
	if out != stdout || errText != "" || omitted.Any() {
		t.Errorf("stdout fits the whole budget once stderr is empty, but was cut: %+v", omitted)
	}
	out, errText, _ = BoundTestOutput("", stdout, 2000)
	if out != "" || errText != stdout {
		t.Error("stderr did not get stdout's unused share")
	}
}

func TestBoundTestOutput_ShortStreamKeptWhole(t *testing.T) {
	stderr := "exit status 1\n"
	stdout := lines("out", 1000)
	out, errText, omitted := BoundTestOutput(stdout, stderr, 4096)
	if errText != stderr {
		t.Errorf("short stderr was cut: %q", errText)
	}
	if got := len(withoutMarkers(out)); got > 4096-len(stderr) {
		t.Errorf("stdout keeps %d bytes, more than its share %d", got, 4096-len(stderr))
	}
	if !omitted.Any() || omitted.Lines == 0 {
		t.Errorf("omitted = %+v, want lines and bytes", omitted)
	}
}

func TestBoundTestOutput_KeepsHeadFailuresAndTail(t *testing.T) {
	stdout := lines("noise", 400) + "--- FAIL: TestAdd (0.00s)\n    add_test.go:12: want 3, got 4\n" + lines("noise", 400)
	out, _, omitted := BoundTestOutput(stdout, "", 4096)

	for _, want := range []string{"noise line 0000\n", "--- FAIL: TestAdd (0.00s)\n", "add_test.go:12: want 3, got 4\n", "noise line 0399\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("bounded output lacks %q", want)
		}
	}
	if markers := markerRE.FindAllString(out, -1); len(markers) != 2 {
		t.Errorf("want a marker before and after the failure lines, got %d:\n%s", len(markers), out)
	}
	if got := len(withoutMarkers(out)); got > 4096 {
		t.Errorf("kept %d bytes, budget 4096", got)
	}
	if omitted.Bytes != len(stdout)-len(withoutMarkers(out)) {
		t.Errorf("omitted.Bytes = %d, want %d", omitted.Bytes, len(stdout)-len(withoutMarkers(out)))
	}
	// Every kept piece is whole lines.
	for _, l := range strings.Split(strings.TrimSuffix(withoutMarkers(out), "\n"), "\n") {
		if !strings.HasPrefix(l, "noise line ") && !failureLine(l) {
			t.Errorf("partial line kept: %q", l)
		}
	}
}

func TestBoundTestOutput_UnusedFailureShareGoesToTail(t *testing.T) {
	stdout := lines("noise", 1000)
	out, _, _ := BoundTestOutput(stdout, "", 4096)
	kept := len(withoutMarkers(out))
	// No failure lines: head (up to 20%) plus a tail that took the failure share too.
	if kept < 4096*90/100 {
		t.Errorf("kept only %d of 4096 bytes; the failure share should go to the tail", kept)
	}
	if markers := markerRE.FindAllString(out, -1); len(markers) != 1 {
		t.Errorf("want one cut between head and tail, got %d", len(markers))
	}
}

func TestBoundTestOutput_SingleLongLine(t *testing.T) {
	stdout := strings.Repeat("x", 100<<10) // one 100 KiB line, no newline
	out, _, omitted := BoundTestOutput(stdout, "", 8192)
	kept := withoutMarkers(out)
	if len(kept) == 0 || len(kept) > 8192 {
		t.Errorf("kept %d bytes of a single long line, budget 8192", len(kept))
	}
	if !strings.HasPrefix(out, "xxxx") || !strings.HasSuffix(out, "xxxx") {
		t.Error("a single long line keeps its beginning and its end")
	}
	if omitted.Lines != 0 || omitted.Bytes != len(stdout)-len(kept) {
		t.Errorf("omitted = %+v", omitted)
	}
	if !strings.Contains(out, "x[... ") {
		t.Error("a marker after a cut line continues that line")
	}
}

func TestBoundTestOutput_CutsOnRuneBoundaries(t *testing.T) {
	stdout := strings.Repeat("ошибка ✕ ", 5000) // multibyte, no newline
	for _, budget := range []int{4096, 4097, 4098, 4099, 5000} {
		out, errText, _ := BoundTestOutput(stdout, "", budget)
		if !utf8.ValidString(out) || !utf8.ValidString(errText) {
			t.Errorf("budget %d: output is not valid UTF-8", budget)
		}
	}
}

func TestBoundTestOutput_BothStreamsLong(t *testing.T) {
	stdout, stderr := lines("out", 1000), lines("err", 1000)
	out, errText, _ := BoundTestOutput(stdout, stderr, 4096)
	if a, b := len(withoutMarkers(out)), len(withoutMarkers(errText)); a > 2048 || b > 2048 || a == 0 || b == 0 {
		t.Errorf("kept %d + %d bytes, want each within its half of 4096", a, b)
	}
}

func TestFailureLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"--- FAIL: TestX (0.01s)", true},
		{"FAIL\torchestrator/internal/x\t0.01s", true},
		{"panic: runtime error: index out of range", true},
		{"    x_test.go:42: want 1, got 2", true},
		{"E       assert 1 == 2", true},
		{"  ● Suite › case", true},
		{"Traceback (most recent call last):", true},
		{"error: linker failed", true},
		{"ok  \torchestrator/internal/x\t0.01s", false},
		{"=== RUN   TestX", false},
		{"see x_test.go: for details", false},
		{"Everything fine", false},
	} {
		if got := failureLine(tc.line); got != tc.want {
			t.Errorf("failureLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func FuzzBoundTestOutput(f *testing.F) {
	f.Add("--- FAIL: x\nok\n", "panic: y\n", 64)
	f.Add(strings.Repeat("ab\n", 100), "", 16)
	f.Add("ошибка", "✕✕✕", 3)
	f.Fuzz(func(t *testing.T, stdout, stderr string, budget int) {
		if budget < 0 || budget > 1<<16 || !utf8.ValidString(stdout) || !utf8.ValidString(stderr) {
			t.Skip()
		}
		out, errText, omitted := BoundTestOutput(stdout, stderr, budget)
		if !utf8.ValidString(out) || !utf8.ValidString(errText) {
			t.Fatal("result is not valid UTF-8")
		}
		kept := len(withoutMarkers(out)) + len(withoutMarkers(errText))
		if len(stdout)+len(stderr) > budget && kept > budget {
			t.Fatalf("kept %d bytes, budget %d", kept, budget)
		}
		if omitted.Bytes != len(stdout)+len(stderr)-kept {
			t.Fatalf("omitted.Bytes = %d, want %d", omitted.Bytes, len(stdout)+len(stderr)-kept)
		}
		out2, errText2, omitted2 := BoundTestOutput(stdout, stderr, budget)
		if out != out2 || errText != errText2 || omitted != omitted2 {
			t.Fatal("not deterministic")
		}
	})
}

func TestExecuteImplement_BoundsTestOutputToTheConfiguredLimit(t *testing.T) {
	client := &fakeClient{output: `{"summary":"fixed","changed_files":[],"tests_added":[]}`}
	d := newDispatcher(client)
	limit := config.MinMaxTestOutputBytes
	d.Config.Limits.Prompt.MaxTestOutputBytes = &limit
	stdout := lines("noise", 2000) + "--- FAIL: TestFoo\n" + lines("noise", 2000)
	d.pendingTestFailure = &domain.TestResult{Command: "go test ./...", ExitCode: 1, Stdout: stdout}

	run := &domain.Run{ID: "run-1", Task: domain.Task{Description: "fix it"}}
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompt := client.lastReq.Prompt
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
	if _, err := d.Execute(context.Background(), domain.ActionImplement, run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(client.lastReq.Prompt, "was shortened") {
		t.Error("an output within the default limit was annotated as shortened")
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
