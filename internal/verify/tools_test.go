package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/guard"
	"orchestrator/internal/testexec"
)

func testState(t testing.TB) guard.State {
	t.Helper()
	var s guard.State
	if err := json.Unmarshal([]byte(`{"patterns":[".github/**"],"git_fingerprint":"fp","protected":{}}`), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func testSpec(t testing.TB) *Spec {
	t.Helper()
	return &Spec{
		Version:     SpecVersion,
		RepoRoot:    "/repo",
		TestCommand: `go test ./...${ORCH_TEST_FILTER:+ -run "$ORCH_TEST_FILTER"}`,
		Checks:      []Check{{Name: "vet", Command: "go vet ./..."}, {Name: "lint", Command: "golangci-lint run"}},
		PreRunRef:   "abc123",
		Sandbox:     &testexec.Sandbox{Home: "/home/u", RepoRoot: "/repo", GitDirs: []string{"/repo/.git"}},
		TestEnv:     []string{"PATH=/usr/bin", "HOME=/home/u"},
		Before:      testState(t),
		MaxCalls:    8,
		CallTimeout: time.Minute,
		OutputBytes: 4096,
	}
}

type runCall struct {
	command string
	dir     string
	env     []string
}

// fakeRunner records every run and answers with result and err. With block set it waits for
// its context, as a process that outlives the call timeout would.
type fakeRunner struct {
	calls  []runCall
	result domain.TestResult
	err    error
	block  bool
}

func (f *fakeRunner) Run(ctx context.Context, command, dir string, opts testexec.RunOptions) (domain.TestResult, error) {
	f.calls = append(f.calls, runCall{command: command, dir: dir, env: opts.Env})
	if f.block {
		<-ctx.Done()
		return domain.TestResult{}, &apperr.ExecutionError{Msg: "failed to run test command", Err: ctx.Err()}
	}
	res := f.result
	res.Command = command
	return res, f.err
}

type fakeGuard struct {
	changed []string
	err     error
	calls   int
}

func (g *fakeGuard) Changed(ctx context.Context, before guard.State) ([]string, error) {
	g.calls++
	return g.changed, g.err
}

func newTools(t testing.TB, spec *Spec, runner Runner, g Guard) (*Tools, string) {
	t.Helper()
	dir := t.TempDir()
	tools, err := NewTools(spec, dir, runner, g)
	if err != nil {
		t.Fatal(err)
	}
	return tools, dir
}

func args(t testing.TB, kv ...any) json.RawMessage {
	t.Helper()
	m := map[string]any{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// readCalls decodes calls.jsonl strictly, line by line, into domain.VerifyCall.
func readCalls(t testing.TB, dir string) []domain.VerifyCall {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, callsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []domain.VerifyCall
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var c domain.VerifyCall
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("calls.jsonl line %q: %v", line, err)
		}
		calls = append(calls, c)
	}
	return calls
}

func mode(t testing.TB, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestCall_RunTestsWithAFilter(t *testing.T) {
	runner := &fakeRunner{result: domain.TestResult{ExitCode: 1, Stdout: "--- FAIL: TestStore\n", Stderr: "exit status 1\n", Duration: 3200 * time.Millisecond}}
	tools, dir := newTools(t, testSpec(t), runner, &fakeGuard{})

	res := tools.Call(context.Background(), domain.VerifyToolRunTests, args(t, "filter", "TestStore"))
	if res.IsError {
		t.Fatalf("refused: %s", res.Text)
	}
	for _, want := range []string{
		"exit code: 1\n", "command: go test ./...", "filter: TestStore\n", "duration: 3.2s\n",
		"calls left in this step: 7\n", "--- output (bounded) ---\n--- FAIL: TestStore\nexit status 1\n",
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("answer lacks %q:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "check:") {
		t.Errorf("run_tests answer names a check:\n%s", res.Text)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner called %d times", len(runner.calls))
	}
	call := runner.calls[0]
	if call.dir != "/repo" || !slices.Equal(call.env, []string{"ORCH_BASE_REF=abc123", "ORCH_TEST_FILTER=TestStore"}) {
		t.Errorf("run = %+v", call)
	}
	// The filter reaches the command only through the environment.
	if strings.Contains(call.command, "TestStore") {
		t.Errorf("the filter was substituted into the command: %q", call.command)
	}

	got := readCalls(t, dir)
	want := domain.VerifyCall{Tool: "run_tests", Filter: "TestStore", ExitCode: 1, Duration: 3200 * time.Millisecond}
	if len(got) != 1 || got[0] != want {
		t.Errorf("calls.jsonl = %+v, want [%+v]", got, want)
	}
}

func TestCall_WithoutAFilterTheVariableIsUnset(t *testing.T) {
	runner := &fakeRunner{}
	tools, _ := newTools(t, testSpec(t), runner, &fakeGuard{})
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(` `)} {
		if res := tools.Call(context.Background(), domain.VerifyToolRunTests, raw); res.IsError {
			t.Fatalf("args %q refused: %s", raw, res.Text)
		}
	}
	if len(runner.calls) != 4 {
		t.Fatalf("runner called %d times, want 4", len(runner.calls))
	}
	for _, call := range runner.calls {
		if !slices.Equal(call.env, []string{"ORCH_BASE_REF=abc123"}) {
			t.Errorf("env = %q, want ORCH_BASE_REF only", call.env)
		}
	}
}

func TestCall_RunCheck(t *testing.T) {
	runner := &fakeRunner{result: domain.TestResult{ExitCode: 0, Stdout: "ok\n"}}
	tools, dir := newTools(t, testSpec(t), runner, &fakeGuard{})

	res := tools.Call(context.Background(), domain.VerifyToolRunCheck, args(t, "name", "lint"))
	if res.IsError {
		t.Fatalf("refused: %s", res.Text)
	}
	if !strings.Contains(res.Text, "command: golangci-lint run\n") || !strings.Contains(res.Text, "check: lint\n") ||
		strings.Contains(res.Text, "filter:") {
		t.Errorf("answer:\n%s", res.Text)
	}
	if runner.calls[0].command != "golangci-lint run" || !slices.Equal(runner.calls[0].env, []string{"ORCH_BASE_REF=abc123"}) {
		t.Errorf("run = %+v", runner.calls[0])
	}
	got := readCalls(t, dir)
	if len(got) != 1 || got[0].Tool != "run_check" || got[0].Check != "lint" || !got[0].Success {
		t.Errorf("calls.jsonl = %+v", got)
	}
}

func TestCall_RefusesBadArguments(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args json.RawMessage
		want string
	}{
		{"unknown argument", "run_tests", json.RawMessage(`{"filter":"x","extra":1}`), "unknown argument"},
		{"only an unknown argument", "run_tests", json.RawMessage(`{"verbose":true}`), "unknown argument"},
		{"array", "run_tests", json.RawMessage(`[]`), "must be an object"},
		{"string", "run_tests", json.RawMessage(`"x"`), "must be an object"},
		{"filter is a number", "run_tests", json.RawMessage(`{"filter":5}`), "filter must be a string"},
		{"filter is null", "run_tests", json.RawMessage(`{"filter":null}`), "filter must be a string"},
		{"empty filter", "run_tests", json.RawMessage(`{"filter":""}`), "filter must match"},
		{"filter with a semicolon", "run_tests", json.RawMessage(`{"filter":"x; touch /tmp/y"}`), "filter must match"},
		{"filter with a command substitution", "run_tests", json.RawMessage(`{"filter":"$(id)"}`), "filter must match"},
		{"check argument on run_tests", "run_tests", json.RawMessage(`{"name":"vet"}`), "unknown argument"},
		{"check without a name", "run_check", json.RawMessage(`{}`), "name is required"},
		{"check without arguments", "run_check", nil, "name is required"},
		{"check name is a number", "run_check", json.RawMessage(`{"name":1}`), "name must be a string"},
		{"unknown check", "run_check", json.RawMessage(`{"name":"rm -rf /"}`), "unknown check"},
		{"filter on run_check", "run_check", json.RawMessage(`{"name":"vet","filter":"x"}`), "unknown argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			g := &fakeGuard{}
			tools, dir := newTools(t, testSpec(t), runner, g)
			res := tools.Call(context.Background(), tc.tool, tc.args)
			if !res.IsError || !strings.HasPrefix(res.Text, "refused: ") || !strings.Contains(res.Text, tc.want) {
				t.Errorf("answer = %+v, want a refusal containing %q", res, tc.want)
			}
			if strings.Contains(res.Text, "\n") {
				t.Errorf("a refusal is one line: %q", res.Text)
			}
			if len(runner.calls) != 0 || g.calls != 0 {
				t.Errorf("a refused call ran something: runner %d, guard %d", len(runner.calls), g.calls)
			}
			calls := readCalls(t, dir)
			if len(calls) != 1 || calls[0].Refused == "" || calls[0].Tool != tc.tool {
				t.Errorf("calls.jsonl = %+v", calls)
			}
			// What the model sent is never copied into the record.
			if raw, _ := os.ReadFile(filepath.Join(dir, callsFile)); strings.Contains(string(raw), "touch") || strings.Contains(string(raw), "rm -rf") {
				t.Errorf("the record echoes the model's input: %s", raw)
			}
		})
	}
}

func TestCall_UnknownCheckListsTheNames(t *testing.T) {
	tools, _ := newTools(t, testSpec(t), &fakeRunner{}, &fakeGuard{})
	res := tools.Call(context.Background(), domain.VerifyToolRunCheck, args(t, "name", "nope"))
	if !strings.Contains(res.Text, "vet, lint") {
		t.Errorf("answer = %q", res.Text)
	}
}

// FuzzFilter: a filter is accepted exactly when it matches the documented pattern, and
// only then does anything run, with the filter as ORCH_TEST_FILTER alone.
func FuzzFilter(f *testing.F) {
	for _, seed := range []string{
		"TestStore", "pkg/sub.Test_X-1:y", ";", " ", "$", "`", `"`, "\n", "TestStore\n", "\nTestStore", "",
		"a b", "a;b", "$(id)", "a|b", "a&b", "a'b", "a\\b", "ошибка", "a\x00b",
		strings.Repeat("a", 200), strings.Repeat("a", 201),
	} {
		f.Add(seed)
	}
	pattern := regexp.MustCompile(domain.VerifyFilterPattern)
	f.Fuzz(func(t *testing.T, filter string) {
		if !utf8.ValidString(filter) {
			t.Skip() // JSON cannot carry it unchanged
		}
		runner := &fakeRunner{}
		tools, _ := newTools(t, testSpec(t), runner, &fakeGuard{})
		res := tools.Call(context.Background(), domain.VerifyToolRunTests, args(t, "filter", filter))

		accepted := pattern.MatchString(filter)
		if res.IsError == accepted {
			t.Fatalf("filter %q: accepted by the pattern = %v, but IsError = %v (%s)", filter, accepted, res.IsError, res.Text)
		}
		switch {
		case accepted && (len(runner.calls) != 1 || !slices.Contains(runner.calls[0].env, "ORCH_TEST_FILTER="+filter)):
			t.Fatalf("filter %q accepted but run as %+v", filter, runner.calls)
		case !accepted && len(runner.calls) != 0:
			t.Fatalf("filter %q refused but something ran", filter)
		}
		if accepted && strings.Contains(runner.calls[0].command, filter) {
			t.Fatalf("filter %q reached the command line", filter)
		}
	})
}

func TestCall_LimitReached(t *testing.T) {
	spec := testSpec(t)
	spec.MaxCalls = 8
	runner := &fakeRunner{}
	tools, dir := newTools(t, spec, runner, &fakeGuard{})

	var last Result
	for i := 1; i <= 9; i++ {
		last = tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
		if i <= 8 {
			if last.IsError {
				t.Fatalf("call %d refused: %s", i, last.Text)
			}
			if want := fmt.Sprintf("calls left in this step: %d\n", 8-i); !strings.Contains(last.Text, want) {
				t.Errorf("call %d: %q not in the answer", i, want)
			}
		}
	}
	if !last.IsError || !strings.Contains(last.Text, "limit reached") {
		t.Errorf("9th call = %+v, want a refusal with \"limit reached\"", last)
	}
	if len(runner.calls) != 8 {
		t.Errorf("runner called %d times, want 8", len(runner.calls))
	}
	calls := readCalls(t, dir)
	if len(calls) != 9 || calls[8].Refused != "limit reached" || calls[8].Tool != "run_tests" {
		t.Errorf("calls.jsonl = %+v", calls)
	}
}

func TestCall_ARefusedCallCountsAgainstTheLimit(t *testing.T) {
	spec := testSpec(t)
	spec.MaxCalls = 2
	runner := &fakeRunner{}
	tools, _ := newTools(t, spec, runner, &fakeGuard{})
	tools.Call(context.Background(), domain.VerifyToolRunTests, args(t, "filter", ";"))
	tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if !res.IsError || !strings.Contains(res.Text, "limit reached") || len(runner.calls) != 1 {
		t.Errorf("answer = %+v, runs = %d", res, len(runner.calls))
	}
}

func TestCall_CallsJSONLHoldsAtMostFiftyLines(t *testing.T) {
	spec := testSpec(t)
	spec.MaxCalls = 50
	runner := &fakeRunner{}
	tools, dir := newTools(t, spec, runner, &fakeGuard{})
	for range 60 {
		tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	}
	if len(runner.calls) != 50 {
		t.Errorf("runner called %d times, want 50", len(runner.calls))
	}
	if calls := readCalls(t, dir); len(calls) != domain.MaxVerifyCalls {
		t.Errorf("calls.jsonl has %d lines, want %d", len(calls), domain.MaxVerifyCalls)
	}
}

func TestCall_TimeoutIsExitMinusOne(t *testing.T) {
	spec := testSpec(t)
	spec.CallTimeout = 50 * time.Millisecond
	runner := &fakeRunner{block: true}
	tools, dir := newTools(t, spec, runner, &fakeGuard{})

	res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if res.IsError {
		t.Fatalf("a timeout is a result, not a refusal: %s", res.Text)
	}
	if !strings.Contains(res.Text, "exit code: -1") || !strings.Contains(res.Text, "timed out") {
		t.Errorf("answer:\n%s", res.Text)
	}
	calls := readCalls(t, dir)
	if len(calls) != 1 || calls[0].ExitCode != -1 || calls[0].Success || calls[0].Refused != "" {
		t.Errorf("calls.jsonl = %+v", calls)
	}
}

func TestCall_CancellationIsNotATimeout(t *testing.T) {
	runner := &fakeRunner{block: true}
	tools, _ := newTools(t, testSpec(t), runner, &fakeGuard{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := tools.Call(ctx, domain.VerifyToolRunTests, nil)
	if !res.IsError || strings.Contains(res.Text, "timed out") || !strings.Contains(res.Text, "failed to run") {
		t.Errorf("answer = %+v", res)
	}
}

func TestCall_RunnerFailureIsARefusal(t *testing.T) {
	runner := &fakeRunner{err: errors.New("bwrap: not found")}
	tools, dir := newTools(t, testSpec(t), runner, &fakeGuard{})
	res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if !res.IsError || !strings.Contains(res.Text, "failed to run: bwrap: not found") {
		t.Errorf("answer = %+v", res)
	}
	if calls := readCalls(t, dir); len(calls) != 1 || calls[0].Refused != "failed to run" {
		t.Errorf("calls.jsonl = %+v", calls)
	}
}

func TestCall_AGuardChangeWritesViolationAndRefusesEveryLaterCall(t *testing.T) {
	runner := &fakeRunner{}
	g := &fakeGuard{changed: []string{".git (config or hooks)", ".github/workflows/ci.yml"}}
	tools, dir := newTools(t, testSpec(t), runner, g)

	res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if !res.IsError || !strings.Contains(res.Text, "refused: protected files changed: .git (config or hooks), .github/workflows/ci.yml") {
		t.Errorf("answer = %+v", res)
	}
	path := filepath.Join(dir, violationFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no violation file: %v", err)
	}
	if string(data) != ".git (config or hooks)\n.github/workflows/ci.yml\n" {
		t.Errorf("violation = %q", data)
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("violation mode = %v, want 0600", m)
	}

	// The tree is restored, but every later call is refused all the same, of either tool.
	g.changed = nil
	later := []struct {
		tool string
		args json.RawMessage
	}{
		{domain.VerifyToolRunTests, nil},
		{domain.VerifyToolRunCheck, args(t, "name", "vet")},
		{domain.VerifyToolRunTests, args(t, "filter", "TestStore")},
	}
	for _, c := range later {
		res := tools.Call(context.Background(), c.tool, c.args)
		if !res.IsError || !strings.Contains(res.Text, "earlier call found protected files changed") {
			t.Errorf("%s after a violation = %+v", c.tool, res)
		}
	}
	if len(runner.calls) != 0 {
		t.Errorf("the runner ran %d times after a violation", len(runner.calls))
	}
	if g.calls != 1 {
		t.Errorf("the guard was asked %d times; later calls are refused before it", g.calls)
	}
	for i, c := range readCalls(t, dir) {
		if c.Refused == "" {
			t.Errorf("call %d was not refused: %+v", i+1, c)
		}
	}
}

func TestCall_AGuardErrorFailsClosed(t *testing.T) {
	runner := &fakeRunner{}
	g := &fakeGuard{err: errors.New("git: exit status 128\nfatal: not a git repository")}
	tools, dir := newTools(t, testSpec(t), runner, g)

	res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if !res.IsError || strings.Contains(res.Text, "\n") || !strings.Contains(res.Text, "failed") {
		t.Errorf("answer = %+v", res)
	}
	if len(runner.calls) != 0 {
		t.Error("the command ran although the guard could not tell")
	}
	data, err := os.ReadFile(filepath.Join(dir, violationFile))
	if err != nil || !strings.Contains(string(data), "guard check failed") {
		t.Errorf("violation = %q, %v", data, err)
	}
	g.err = nil
	if res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil); !res.IsError || len(runner.calls) != 0 {
		t.Error("a call after a failed guard check must stay refused")
	}
}

func TestCall_ShutdownDuringTheGuardIsNotAViolation(t *testing.T) {
	g := &fakeGuard{err: context.Canceled}
	tools, dir := newTools(t, testSpec(t), &fakeRunner{}, g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := tools.Call(ctx, domain.VerifyToolRunTests, nil)
	if !res.IsError {
		t.Errorf("answer = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, violationFile)); err == nil {
		t.Error("a cancelled session planted a violation")
	}
}

func TestCall_OutputIsBoundedButTheLogsKeepEverything(t *testing.T) {
	spec := testSpec(t)
	spec.OutputBytes = 512
	stdout := strings.Repeat("noise line\n", 500) + "--- FAIL: TestX\n"
	runner := &fakeRunner{result: domain.TestResult{ExitCode: 1, Stdout: stdout, Stderr: "boom\n"}}
	tools, dir := newTools(t, spec, runner, &fakeGuard{})

	res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if !strings.Contains(res.Text, "bytes omitted") || !strings.Contains(res.Text, "--- FAIL: TestX") || len(res.Text) > 2048 {
		t.Errorf("answer (%d bytes) is not bounded:\n%s", len(res.Text), res.Text)
	}
	if calls := readCalls(t, dir); len(calls) != 1 || !calls[0].OutputCut {
		t.Errorf("calls.jsonl = %+v, want output_cut", calls)
	}
	gotOut, err := os.ReadFile(filepath.Join(dir, "call-1.stdout.log"))
	if err != nil || string(gotOut) != stdout {
		t.Errorf("call-1.stdout.log = %d bytes, %v; want the full %d", len(gotOut), err, len(stdout))
	}
	if gotErr, err := os.ReadFile(filepath.Join(dir, "call-1.stderr.log")); err != nil || string(gotErr) != "boom\n" {
		t.Errorf("call-1.stderr.log = %q, %v", gotErr, err)
	}
}

func TestCall_ZeroOutputBytesKeepsEverything(t *testing.T) {
	spec := testSpec(t)
	spec.OutputBytes = 0
	stdout := strings.Repeat("x\n", 100000)
	tools, _ := newTools(t, spec, &fakeRunner{result: domain.TestResult{Stdout: stdout}}, &fakeGuard{})
	if res := tools.Call(context.Background(), domain.VerifyToolRunTests, nil); !strings.Contains(res.Text, stdout) {
		t.Error("with no bound the whole output must be returned")
	}
}

func TestCall_FilesAreOwnerOnly(t *testing.T) {
	runner := &fakeRunner{result: domain.TestResult{Stdout: "o", Stderr: "e"}}
	tools, dir := newTools(t, testSpec(t), runner, &fakeGuard{})
	tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	tools.Call(context.Background(), domain.VerifyToolRunTests, args(t, "filter", ";"))
	for _, name := range []string{callsFile, "call-1.stdout.log", "call-1.stderr.log"} {
		if m := mode(t, filepath.Join(dir, name)); m != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, m)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"call-1.stderr.log", "call-1.stdout.log", "calls.jsonl"}; !slices.Equal(names, want) {
		t.Errorf("files in the directory = %q, want %q (a refused call writes no logs)", names, want)
	}
}

func TestCall_ALogIsNeverWrittenThroughASymlink(t *testing.T) {
	tools, dir := newTools(t, testSpec(t), &fakeRunner{result: domain.TestResult{Stdout: "o"}}, &fakeGuard{})
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(target, filepath.Join(dir, "call-1.stdout.log")); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	tools.Log = &log
	tools.Call(context.Background(), domain.VerifyToolRunTests, nil)
	if _, err := os.Stat(target); err == nil {
		t.Error("the log was written through a symlink")
	}
	if !strings.Contains(log.String(), "call-1.stdout.log") {
		t.Errorf("the failure was not reported: %q", log.String())
	}
}

func TestNewTools_ResumesTheStateOfTheStep(t *testing.T) {
	spec := testSpec(t)
	spec.MaxCalls = 2
	dir := t.TempDir()
	first, err := NewTools(spec, dir, &fakeRunner{}, &fakeGuard{})
	if err != nil {
		t.Fatal(err)
	}
	first.Call(context.Background(), domain.VerifyToolRunTests, nil)
	first.Call(context.Background(), domain.VerifyToolRunTests, nil)

	// The server is restarted: the limit still holds.
	runner := &fakeRunner{}
	second, err := NewTools(spec, dir, runner, &fakeGuard{})
	if err != nil {
		t.Fatal(err)
	}
	if res := second.Call(context.Background(), domain.VerifyToolRunTests, nil); !res.IsError || len(runner.calls) != 0 {
		t.Errorf("a restarted server forgot the calls: %+v", res)
	}

	// And so does a violation.
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, violationFile), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := NewTools(testSpec(t), dir2, runner, &fakeGuard{})
	if err != nil {
		t.Fatal(err)
	}
	if res := third.Call(context.Background(), domain.VerifyToolRunTests, nil); !res.IsError || len(runner.calls) != 0 {
		t.Errorf("a restarted server forgot the violation: %+v", res)
	}
}

func TestCall_EveryRecordDecodesStrictly(t *testing.T) {
	runner := &fakeRunner{result: domain.TestResult{ExitCode: 2, Stdout: "x"}}
	g := &fakeGuard{}
	tools, dir := newTools(t, testSpec(t), runner, g)
	ctx := context.Background()
	tools.Call(ctx, domain.VerifyToolRunTests, args(t, "filter", "TestA"))
	tools.Call(ctx, domain.VerifyToolRunCheck, args(t, "name", "vet"))
	tools.Call(ctx, domain.VerifyToolRunCheck, args(t, "name", "nope"))
	tools.Call(ctx, domain.VerifyToolRunTests, args(t, "filter", "a b"))
	g.changed = []string{"x"}
	tools.Call(ctx, domain.VerifyToolRunTests, nil)

	calls := readCalls(t, dir)
	if len(calls) != 5 {
		t.Fatalf("calls.jsonl has %d lines, want 5: %+v", len(calls), calls)
	}
	for i, c := range calls {
		refused := i >= 2
		if (c.Refused != "") != refused {
			t.Errorf("record %d: refused = %q", i+1, c.Refused)
		}
		if refused && (c.ExitCode != 0 || c.Success || c.Duration != 0) {
			t.Errorf("record %d: a refused call has a result: %+v", i+1, c)
		}
		if (c.Check != "") != (c.Tool == "run_check" && c.Refused != "invalid arguments: "+reasonUnknownCheck) {
			t.Errorf("record %d: check = %q for %s", i+1, c.Check, c.Tool)
		}
	}
	if calls[0].Success || calls[0].ExitCode != 2 || calls[0].Filter != "TestA" {
		t.Errorf("record 1 = %+v", calls[0])
	}
}
