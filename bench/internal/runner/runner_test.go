package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"orchbench/internal/config"
	"orchbench/internal/task"
	"orchbench/internal/testutil"
)

// configsDir is bench/configs, the files the benchmark ships.
const configsDir = "../../configs"

// The fake programs. Both record how they were called into the files named by the FAKE_*
// variables the harness sets, then behave as those variables say.
const claudeScript = `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_ARGS_FILE"
cat > "$FAKE_STDIN_FILE"
pwd > "$FAKE_PWD_FILE"
if [ -n "$FAKE_PIDFILE" ]; then sleep 60 & echo $! > "$FAKE_PIDFILE"; fi
if [ -n "$FAKE_STDERR" ]; then echo "$FAKE_STDERR" >&2; fi
if [ -n "$FAKE_STDOUT" ]; then printf '%s\n' "$FAKE_STDOUT"; fi
if [ -n "$FAKE_SLEEP" ]; then sleep "$FAKE_SLEEP"; fi
exit "${FAKE_EXIT:-0}"
`

// The fake orchestrator copies the files of FAKE_RUN_SRC into .orchestrator/runs/<id> of its
// working directory, for every id in FAKE_RUN_IDS (default r1).
const orchestratorScript = `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_ARGS_FILE"
pwd > "$FAKE_PWD_FILE"
if [ -n "$FAKE_RUN_SRC" ]; then
  for d in ${FAKE_RUN_IDS:-r1}; do
    mkdir -p ".orchestrator/runs/$d"
    cp -R "$FAKE_RUN_SRC"/. ".orchestrator/runs/$d/"
  done
fi
if [ -n "$FAKE_STDERR" ]; then echo "$FAKE_STDERR" >&2; fi
if [ -n "$FAKE_SLEEP" ]; then sleep "$FAKE_SLEEP"; fi
exit "${FAKE_EXIT:-0}"
`

type harness struct {
	t                *testing.T
	ws               Workspace
	args, stdin, pwd string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	bin, work := filepath.Join(root, "bin"), filepath.Join(root, "work")
	for _, d := range []string{bin, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := &harness{
		t:     t,
		args:  filepath.Join(root, "args"),
		stdin: filepath.Join(root, "stdin"),
		pwd:   filepath.Join(root, "pwd"),
		ws: Workspace{
			Dir:    work,
			OutDir: filepath.Join(root, "out"),
			Bins: Bins{
				Claude:       writeScript(t, bin, "claude", claudeScript),
				Orchestrator: writeScript(t, bin, "orchestrator", orchestratorScript),
			},
		},
	}
	t.Setenv("FAKE_ARGS_FILE", h.args)
	t.Setenv("FAKE_STDIN_FILE", h.stdin)
	t.Setenv("FAKE_PWD_FILE", h.pwd)
	for _, k := range []string{"FAKE_EXIT", "FAKE_STDERR", "FAKE_STDOUT", "FAKE_SLEEP", "FAKE_PIDFILE", "FAKE_RUN_SRC", "FAKE_RUN_IDS"} {
		t.Setenv(k, "")
	}
	return h
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func (h *harness) set(key, value string) {
	h.t.Helper()
	h.t.Setenv(key, value)
}

// runDir makes the fake orchestrator leave a run directory with these files.
func (h *harness) runDir(files map[string]string) {
	h.t.Helper()
	src := h.t.TempDir()
	testutil.Apply(h.t, src, files)
	h.set("FAKE_RUN_SRC", src)
}

func (h *harness) run(name string, tk task.Task) (Outcome, error) {
	h.t.Helper()
	return Run(context.Background(), loadConfig(h.t, name), tk, h.ws)
}

// argv is what the fake was called with.
func (h *harness) argv() []string {
	h.t.Helper()
	data, err := os.ReadFile(h.args)
	if err != nil {
		h.t.Fatalf("the fake was not called: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (h *harness) read(path string) string {
	h.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data)
}

func loadConfig(t *testing.T, name string) config.Config {
	t.Helper()
	c, err := config.Load(configsDir, name)
	if err != nil {
		t.Fatalf("config.Load(%s): %v", name, err)
	}
	return c
}

// codeTask has an oracle, which must never reach the config or the prompt.
func codeTask() task.Task {
	return task.Task{
		ID:          "t1",
		Category:    task.CategoryBugfix,
		Language:    "sh",
		Task:        "mul returns the sum of its arguments instead of the product; fix it.",
		TestCommand: "sh tests/run.sh",
		Oracle:      task.Oracle{Files: []string{"tests/hidden_check.sh"}, Command: "sh tests/hidden_check.sh"},
		Timeout:     task.Duration(time.Minute),
	}
}

func questionTask() task.Task {
	tk := codeTask()
	tk.ID, tk.Category, tk.Oracle = "q1", task.CategoryQuestion, task.Oracle{}
	tk.Task = "Where is multiplication implemented?"
	return tk
}

func boolPtr(b bool) *bool { return &b }

// sameOutcome compares the outcomes field by field; paths are checked by the caller and
// floating-point sums within a rounding error.
func sameOutcome(t *testing.T, got, want Outcome) {
	t.Helper()
	if math.Abs(got.CostUSD-want.CostUSD) > 1e-9 || math.Abs(got.Decision.CostUSD-want.Decision.CostUSD) > 1e-9 {
		t.Errorf("cost = %v, decision cost = %v; want %v, %v", got.CostUSD, got.Decision.CostUSD, want.CostUSD, want.Decision.CostUSD)
	}
	got.CostUSD, got.Decision.CostUSD = want.CostUSD, want.Decision.CostUSD
	want.ConfigPath, want.ConfigSHA256, want.RunDir = got.ConfigPath, got.ConfigSHA256, got.RunDir
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outcome differs (pointers are compared by value)\n got: %+v\nwant: %+v", got, want)
	}
}

// checkClassified is the assertion of the classification tables: an infra error whose reason
// holds infraWant, or a result with that status and a failure reason holding reason.
func checkClassified(t *testing.T, out Outcome, err error, infraWant, status, reason string) {
	t.Helper()
	if infraWant != "" {
		var ie *InfraError
		if !errors.As(err, &ie) {
			t.Fatalf("error = %v (outcome %+v), want an infra error containing %q", err, out, infraWant)
		}
		if !strings.Contains(ie.Reason, infraWant) {
			t.Errorf("infra reason = %q, want it to contain %q", ie.Reason, infraWant)
		}
		return
	}
	if err != nil {
		t.Fatalf("error = %v, want a result", err)
	}
	if out.Status != status || !strings.Contains(out.FailureReason, reason) {
		t.Errorf("status %q reason %q, want %q containing %q", out.Status, out.FailureReason, status, reason)
	}
}

// processGone reports whether the process is not running; a zombie waiting to be reaped is gone.
func processGone(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	s := string(data)
	i := strings.LastIndex(s, ")")
	return i >= 0 && strings.HasPrefix(strings.TrimSpace(s[i+1:]), "Z")
}

func requireProcessGone(t *testing.T, pidfile string) {
	t.Helper()
	data, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatalf("the fake did not start its background process: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !processGone(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d outlived the timeout: the process group was not killed", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---- config A ----

const (
	initEvent = `{"type":"system","subtype":"init","session_id":"s1"}`
	resultOK  = `{"type":"result","subtype":"success","is_error":false,"num_turns":3,"result":"done","total_cost_usd":0.42,` +
		`"usage":{"input_tokens":1200,"output_tokens":340,"cache_read_input_tokens":5000,"cache_creation_input_tokens":700}}`
)

// toolResultEvent is the stream-json event that carries a tool's output, such as a test run's.
func toolResultEvent(output string) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":%q}]}}`, output)
}

func TestClaudeMapsTheResultEvent(t *testing.T) {
	h := newHarness(t)
	h.set("FAKE_STDOUT", initEvent+"\n"+`{"type":"assistant","message":{}}`+"\n"+resultOK)
	var logOut bytes.Buffer
	h.ws.Stdout = &logOut

	tk := codeTask()
	got, err := h.run("A", tk)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ConfigSHA256) != 64 {
		t.Errorf("ConfigSHA256 = %q", got.ConfigSHA256)
	}
	sameOutcome(t, got, Outcome{
		Status:       StatusCompleted,
		CostUSD:      0.42,
		Tokens:       Tokens{Input: 1200, Output: 340, CacheRead: 5000, CacheCreation: 700},
		LLMCalls:     1,
		ConfigSHA256: got.ConfigSHA256,
	})

	wantArgs := []string{"-p", "--model", "sonnet", "--output-format", "stream-json", "--verbose",
		"--permission-mode", "acceptEdits", "--allowed-tools", "Read,Grep,Glob,Edit,Write,Bash"}
	if args := h.argv(); !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("argv = %q, want %q", args, wantArgs)
	}
	wantPrompt := tk.Task + "\n\nRun the tests with: sh tests/run.sh\n"
	if prompt := h.read(h.stdin); prompt != wantPrompt {
		t.Errorf("stdin = %q, want %q", prompt, wantPrompt)
	}
	if strings.Contains(h.read(h.stdin), "hidden_check") {
		t.Error("the oracle reached the prompt")
	}
	wantDir, _ := filepath.EvalSymlinks(h.ws.Dir)
	if gotDir := strings.TrimSpace(h.read(h.pwd)); gotDir != wantDir {
		t.Errorf("working directory = %q, want %q", gotDir, wantDir)
	}
	if !strings.Contains(logOut.String(), `"total_cost_usd":0.42`) {
		t.Errorf("Workspace.Stdout did not receive the output: %q", logOut.String())
	}
}

func TestClaudeAnswerOfAQuestion(t *testing.T) {
	h := newHarness(t)
	h.set("FAKE_STDOUT", initEvent+"\n"+strings.Replace(resultOK, `"result":"done"`, `"result":"It is the mul function in calc.sh."`, 1))
	got, err := h.run("A", questionTask())
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "It is the mul function in calc.sh." || got.Status != StatusCompleted {
		t.Errorf("answer %q status %q", got.Answer, got.Status)
	}

	// Another category keeps no answer.
	got, err = h.run("A", codeTask())
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "" {
		t.Errorf("answer of a code task = %q", got.Answer)
	}
}

func TestClaudeClassification(t *testing.T) {
	errResult := func(subtype, text string) string {
		return fmt.Sprintf(`{"type":"result","subtype":%q,"is_error":true,"result":%q,"total_cost_usd":0.1}`, subtype, text)
	}
	cases := []struct {
		name           string
		stdout, stderr string
		exit           string
		infra          string
		status, reason string
	}{
		{name: "completed", stdout: resultOK, status: StatusCompleted},
		{name: "error result", stdout: errResult("error_max_turns", "Reached max turns"), exit: "1", status: StatusFailed, reason: "error_max_turns"},
		{name: "error flag with a success subtype", stdout: errResult("success", "boom"), exit: "1", status: StatusFailed, reason: "claude reported an error"},
		{name: "non-zero exit after a result", stdout: resultOK, exit: "1", status: StatusFailed, reason: "claude exited with code 1"},
		{name: "429 in a successful answer is not a rate limit", stdout: strings.Replace(resultOK, `"result":"done"`, `"result":"retry on HTTP 429"`, 1), status: StatusCompleted},
		{name: "usage limit in the error result", stdout: errResult("success", "You've hit your usage limit"), exit: "1", infra: "rate limit"},
		{name: "rate limit on stderr without a result", stderr: "Error: 429 Too Many Requests", exit: "1", infra: "rate limit"},
		{name: "rate limit on stderr of a successful result", stdout: resultOK, stderr: "API Error: 429 rate_limit_error, retrying", status: StatusCompleted},
		{name: "rate limit on stderr of a non-zero exit", stdout: resultOK, stderr: "API Error: 429 rate_limit_error", exit: "1", infra: "rate limit"},
		{name: "plain rate-limit line without a result", stdout: initEvent + "\nClaude AI usage limit reached", exit: "1", infra: "rate limit"},
		{name: "rate limit in test output before a crash", stdout: initEvent + "\n" + toolResultEvent("FAIL TestRateLimiter: rate limit not applied"), stderr: "panic: oops", exit: "1", infra: "without a result event"},
		{name: "crash without a result", stderr: "panic: oops", exit: "1", infra: "without a result event"},
		{name: "plain text output", stdout: "hello", infra: "without a result event"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.set("FAKE_STDOUT", tc.stdout)
			h.set("FAKE_STDERR", tc.stderr)
			h.set("FAKE_EXIT", tc.exit)
			out, err := h.run("A", codeTask())
			checkClassified(t, out, err, tc.infra, tc.status, tc.reason)
		})
	}

	t.Run("missing binary", func(t *testing.T) {
		h := newHarness(t)
		h.ws.Bins.Claude = filepath.Join(t.TempDir(), "no-such-claude")
		out, err := h.run("A", codeTask())
		checkClassified(t, out, err, "not available", "", "")
		if !IsInfra(err) {
			t.Error("IsInfra = false")
		}
	})
}

func TestClaudeTimeoutIsAResultAndKillsTheProcessGroup(t *testing.T) {
	h := newHarness(t)
	pidfile := filepath.Join(t.TempDir(), "pid")
	h.set("FAKE_SLEEP", "30")
	h.set("FAKE_PIDFILE", pidfile)
	tk := codeTask()
	tk.Timeout = task.Duration(time.Second)

	start := time.Now()
	got, err := h.run("A", tk)
	if err != nil {
		t.Fatalf("a timeout is a result, got error %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("took %v for a 1s timeout", elapsed)
	}
	if !got.TimedOut || got.Status != StatusFailed || got.FailureReason != ReasonTimeout {
		t.Errorf("outcome = %+v", got)
	}
	requireProcessGone(t, pidfile)
}

func TestClaudeCancelledByTheCaller(t *testing.T) {
	h := newHarness(t)
	h.set("FAKE_SLEEP", "30")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	_, err := Run(ctx, loadConfig(t, "A"), codeTask(), h.ws)
	if !errors.Is(err, context.Canceled) || IsInfra(err) {
		t.Errorf("error = %v, want context.Canceled and not infra", err)
	}
}

// ---- orchestrator configs ----

// A run as the orchestrator writes it: the implementation needed one fix, the final tests are
// green, and the run carries its outcome block.
var runWithOutcome = map[string]string{
	"run.json": `{"status":"completed","fix_iterations":1,"executions":[
{"index":1,"action":"implement","status":"completed","usage":{"input_tokens":999,"output_tokens":999,"cost_usd":9}},
{"index":2,"action":"test","status":"completed","test_result":{"success":true}}],
"outcome":{"status":"completed","fix_iterations":1,"tests_passed_final":true,"review_verdict":"pass","duration_ms":4000,
"usage":{"input_tokens":125,"output_tokens":62,"cache_read_tokens":10,"cache_creation_tokens":5,"cost_usd":0.33,"calls":3},
"decision_overhead":{"cost_usd":0.03,"ms":1200,"calls":1}}}`,
	"decision.json": `{"source":"provider","complexity":{"selected":"medium"},"stages":[
{"stage":"research","enabled":false},{"stage":"development","enabled":true},{"stage":"testing","enabled":true},{"stage":"review","enabled":true}]}`,
}

func TestOrchestratorMapsRunAndDecision(t *testing.T) {
	h := newHarness(t)
	h.runDir(runWithOutcome)
	tk := codeTask()

	got, err := h.run("B", tk)
	if err != nil {
		t.Fatal(err)
	}
	sameOutcome(t, got, Outcome{
		Status:           StatusCompleted,
		CostUSD:          0.33,
		Tokens:           Tokens{Input: 125, Output: 62, CacheRead: 10, CacheCreation: 5},
		LLMCalls:         3,
		Decision:         Decision{CostUSD: 0.03, MS: 1200, Calls: 1},
		FixIterations:    1,
		ReviewVerdict:    "pass",
		TestsPassedFinal: boolPtr(true),
		Stages:           []string{"development", "testing", "review"},
		Complexity:       "medium",
	})
	if !strings.HasSuffix(got.RunDir, filepath.Join(".orchestrator", "runs", "r1")) {
		t.Errorf("RunDir = %q", got.RunDir)
	}

	// The rendered config is kept outside the workspace and is the one that was run.
	wantPath, _ := filepath.Abs(filepath.Join(h.ws.OutDir, "configs", "t1-B.yaml"))
	if got.ConfigPath != wantPath {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, wantPath)
	}
	rendered := h.read(got.ConfigPath)
	if got.ConfigSHA256 != task.SHA256Bytes([]byte(rendered)) {
		t.Errorf("ConfigSHA256 %s is not the hash of the file", got.ConfigSHA256)
	}
	if !strings.Contains(rendered, `command: "sh tests/run.sh"`) || strings.Contains(rendered, "hidden_check") {
		t.Errorf("rendered config:\n%s", rendered)
	}

	wantArgs := []string{"--config", wantPath, "--mode", "auto", "run", tk.Task}
	if args := h.argv(); !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("argv = %q, want %q", args, wantArgs)
	}
	wantDir, _ := filepath.EvalSymlinks(h.ws.Dir)
	if gotDir := strings.TrimSpace(h.read(h.pwd)); gotDir != wantDir {
		t.Errorf("working directory = %q, want %q", gotDir, wantDir)
	}
}

func TestOrchestratorPassesTheStages(t *testing.T) {
	for name, want := range map[string][]string{
		"B": nil,
		"C": nil,
		"D": {"--stage", "development", "--stage", "testing"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.runDir(runWithOutcome)
			tk := codeTask()
			got, err := h.run(name, tk)
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := append([]string{"--config", got.ConfigPath, "--mode", "auto", "run"}, want...)
			wantArgs = append(wantArgs, tk.Task)
			if args := h.argv(); !reflect.DeepEqual(args, wantArgs) {
				t.Errorf("argv = %q, want %q", args, wantArgs)
			}
			if !strings.HasSuffix(got.ConfigPath, "t1-"+name+".yaml") {
				t.Errorf("ConfigPath = %q", got.ConfigPath)
			}
		})
	}
}

// A run without the outcome block (killed before it was written, or older than it) gets the
// numbers the core would have computed.
func TestOrchestratorRecomputesAMissingOutcome(t *testing.T) {
	h := newHarness(t)
	h.runDir(map[string]string{
		"run.json": `{"status":"completed","fix_iterations":1,"executions":[
{"index":1,"action":"implement","status":"completed","usage":{"input_tokens":100,"output_tokens":50,"cache_read_tokens":10,"cache_creation_tokens":5,"cost_usd":0.25}},
{"index":2,"action":"test","status":"failed","test_result":{"success":false}},
{"index":3,"action":"implement","status":"completed","usage":{"input_tokens":20,"output_tokens":10,"cost_usd":0.05}},
{"index":4,"action":"test","status":"completed","test_result":{"success":true}},
{"index":5,"action":"review","status":"completed","review_result":{"issues":[{"severity":"low"},{"severity":"high"}]}}]}`,
		"decision.json": `{"source":"provider","complexity":{"selected":"simple"},
"stages":[{"stage":"development","enabled":true},{"stage":"testing","enabled":true},{"stage":"review","enabled":true}],
"usage":{"classification":{"input_tokens":30,"output_tokens":3,"cost_usd":0.02}},
"latency":{"translation_ms":0,"pre_check_ms":100,"context_ms":50,"classification_ms":650}}`,
	})
	got, err := h.run("B", codeTask())
	if err != nil {
		t.Fatal(err)
	}
	sameOutcome(t, got, Outcome{
		Status:           StatusCompleted,
		CostUSD:          0.32,
		Tokens:           Tokens{Input: 150, Output: 63, CacheRead: 10, CacheCreation: 5},
		LLMCalls:         3,
		Decision:         Decision{CostUSD: 0.02, MS: 800, Calls: 1},
		FixIterations:    1,
		ReviewVerdict:    "fail",
		TestsPassedFinal: boolPtr(true),
		Stages:           []string{"development", "testing", "review"},
		Complexity:       "simple",
	})
}

// A `--stage` run has a bare decision: nothing was decided, so nothing is charged to deciding,
// whatever else the file says.
func TestOrchestratorManualDecisionCostsNothing(t *testing.T) {
	h := newHarness(t)
	h.runDir(map[string]string{
		"run.json": `{"status":"completed","executions":[
{"index":1,"action":"implement","status":"completed","usage":{"input_tokens":100,"output_tokens":50,"cost_usd":0.25}},
{"index":2,"action":"test","status":"completed","test_result":{"success":true}}]}`,
		"decision.json": `{"source":"manual","stages":[{"stage":"research","enabled":false},{"stage":"development","enabled":true},{"stage":"testing","enabled":true}],
"latency":{"classification_ms":999}}`,
	})
	got, err := h.run("D", codeTask())
	if err != nil {
		t.Fatal(err)
	}
	sameOutcome(t, got, Outcome{
		Status:           StatusCompleted,
		CostUSD:          0.25,
		Tokens:           Tokens{Input: 100, Output: 50},
		LLMCalls:         1,
		TestsPassedFinal: boolPtr(true),
		Stages:           []string{"development", "testing"},
	})
}

func TestOrchestratorAnswerOfAQuestion(t *testing.T) {
	h := newHarness(t)
	// No decision.json and no outcome: the stages come from the executions, the answer from the
	// last read-only step.
	h.runDir(map[string]string{
		"run.json": `{"status":"completed","executions":[
{"index":1,"action":"research","status":"completed","artifact_path":"artifacts/001-research.json"},
{"index":2,"action":"analyze","status":"completed","artifact_path":"artifacts/002-analyze.json"}]}`,
		"artifacts/001-research.json": `{"answer":"first draft"}`,
		"artifacts/002-analyze.json":  `{"answer":"It is the mul function in calc.sh."}`,
	})
	got, err := h.run("B", questionTask())
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "It is the mul function in calc.sh." {
		t.Errorf("Answer = %q", got.Answer)
	}
	if want := []string{"research", "analysis"}; !reflect.DeepEqual(got.Stages, want) {
		t.Errorf("Stages = %v, want %v", got.Stages, want)
	}
	if got.Complexity != "" {
		t.Errorf("Complexity = %q", got.Complexity)
	}

	// A code task keeps no answer, even when the run wrote one.
	h2 := newHarness(t)
	h2.runDir(map[string]string{
		"run.json":                    `{"status":"completed","executions":[{"index":1,"action":"research","status":"completed","artifact_path":"artifacts/001-research.json"}]}`,
		"artifacts/001-research.json": `{"answer":"x"}`,
	})
	got, err = h2.run("B", codeTask())
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer != "" {
		t.Errorf("Answer of a code task = %q", got.Answer)
	}
}

func runFiles(status, reason, executions string) map[string]string {
	return map[string]string{
		"run.json": fmt.Sprintf(`{"status":%q,"failure_reason":%q,"executions":[%s]}`, status, reason, executions),
	}
}

const (
	execImpl       = `{"index":1,"action":"implement","status":"completed"}`
	execImplFailed = `{"index":1,"action":"implement","status":"failed","error":"claude exited with code 1"}`
	execImplLimit  = `{"index":1,"action":"implement","status":"failed","error":"API Error: Overloaded"}`
)

func TestOrchestratorClassification(t *testing.T) {
	cases := []struct {
		name           string
		run            map[string]string // nil: the orchestrator leaves no run directory
		ids            string
		exit           string
		stderr         string
		infra          string
		status, reason string
	}{
		{name: "completed", run: runFiles("completed", "", execImpl), status: StatusCompleted},
		{name: "429 on stderr of a completed run is not a rate limit", run: runFiles("completed", "", execImpl), stderr: "retrying after HTTP 429", status: StatusCompleted},
		{name: "workflow limit", run: runFiles("failed", "max_fix_iterations reached", execImpl), exit: "4", stderr: "rate limit", status: StatusFailed, reason: "max_fix_iterations"},
		{name: "generic failure", run: runFiles("failed", "tests did not pass", execImpl), exit: "1", status: StatusFailed, reason: "tests did not pass"},
		{name: "provider failure", run: runFiles("failed", "claude exited with code 1", execImplFailed), exit: "3", status: StatusFailed, reason: "claude exited with code 1"},
		{name: "failure without a reason", run: runFiles("failed", "", execImpl), exit: "1", status: StatusFailed, reason: "exited with code 1"},
		{name: "run left unfinished", run: runFiles("running", "", execImpl), exit: "1", status: StatusFailed, reason: "before the run finished"},

		{name: "rate limit on stderr", run: runFiles("failed", "claude exited with code 1", execImplFailed), exit: "3", stderr: "API Error: 429 rate_limit_error", infra: "rate limit"},
		{name: "rate limit in the failure reason", run: runFiles("failed", "claude: You've hit your usage limit", execImplFailed), exit: "3", infra: "rate limit"},
		{name: "rate limit in the last execution", run: runFiles("failed", "claude exited with code 1", execImplLimit), exit: "3", infra: "rate limit"},
		{name: "config or dependency error", exit: "2", stderr: "claude: executable not found", infra: "refused the config"},
		{name: "cancelled from outside", exit: "5", infra: "cancelled from outside"},
		{name: "no run directory", exit: "0", infra: "no run directory"},
		{name: "no run directory after a failure", exit: "1", stderr: "boom", infra: "no run directory"},
		{name: "two run directories", run: runFiles("completed", "", execImpl), ids: "r1 r2", infra: "2 run directories"},
		{name: "crash before any step", run: runFiles("running", "", ""), exit: "1", stderr: "panic: oops", infra: "before any step"},
		{name: "unreadable run.json", run: map[string]string{"run.json": "{"}, exit: "1", infra: "reading run.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.run != nil {
				h.runDir(tc.run)
			}
			h.set("FAKE_RUN_IDS", tc.ids)
			h.set("FAKE_EXIT", tc.exit)
			h.set("FAKE_STDERR", tc.stderr)
			out, err := h.run("B", codeTask())
			checkClassified(t, out, err, tc.infra, tc.status, tc.reason)
		})
	}
}

func TestOrchestratorInfraBeforeTheRun(t *testing.T) {
	t.Run("missing binary", func(t *testing.T) {
		h := newHarness(t)
		h.ws.Bins.Orchestrator = filepath.Join(t.TempDir(), "no-such-orchestrator")
		out, err := h.run("B", codeTask())
		checkClassified(t, out, err, "not available", "", "")
		if out.ConfigPath == "" {
			t.Error("the rendered config should be kept")
		}
	})

	t.Run("a run directory left in a reused workspace", func(t *testing.T) {
		h := newHarness(t)
		if err := os.MkdirAll(filepath.Join(h.ws.Dir, ".orchestrator", "runs", "old"), 0o755); err != nil {
			t.Fatal(err)
		}
		h.runDir(runWithOutcome)
		_, err := h.run("B", codeTask())
		checkClassified(t, Outcome{}, err, "already has a run directory", "", "")
		if _, err := os.Stat(h.args); err == nil {
			t.Error("the orchestrator was started anyway")
		}
	})

	t.Run("task text that looks like a flag", func(t *testing.T) {
		h := newHarness(t)
		tk := codeTask()
		tk.Task = "--stage review is what I want"
		_, err := h.run("B", tk)
		if err == nil || IsInfra(err) || !strings.Contains(err.Error(), "flag") {
			t.Errorf("error = %v, want a non-infra error about a flag", err)
		}
	})
}

func TestOrchestratorTimeoutIsAResult(t *testing.T) {
	old := orchestratorGrace
	orchestratorGrace = 0
	t.Cleanup(func() { orchestratorGrace = old })

	for _, withRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("run directory %v", withRun), func(t *testing.T) {
			h := newHarness(t)
			if withRun {
				h.runDir(runFiles("running", "", execImpl))
			}
			h.set("FAKE_SLEEP", "30")
			tk := codeTask()
			tk.Timeout = task.Duration(time.Second)

			start := time.Now()
			got, err := h.run("B", tk)
			if err != nil {
				t.Fatalf("a timeout is a result, got error %v", err)
			}
			if elapsed := time.Since(start); elapsed > 15*time.Second {
				t.Errorf("took %v for a 1s timeout", elapsed)
			}
			if !got.TimedOut || got.Status != StatusFailed || got.FailureReason != ReasonTimeout {
				t.Errorf("outcome = %+v", got)
			}
			if withRun && got.RunDir == "" {
				t.Error("the run directory of a timed-out run should be kept")
			}
		})
	}
}

func TestOrchestratorCancelledByTheCaller(t *testing.T) {
	h := newHarness(t)
	h.runDir(runWithOutcome)
	h.set("FAKE_SLEEP", "30")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	_, err := Run(ctx, loadConfig(t, "B"), codeTask(), h.ws)
	if !errors.Is(err, context.Canceled) || IsInfra(err) {
		t.Errorf("error = %v, want context.Canceled and not infra", err)
	}
}

func TestRunValidatesItsInput(t *testing.T) {
	h := newHarness(t)
	noTimeout := codeTask()
	noTimeout.Timeout = 0
	if _, err := h.run("A", noTimeout); err == nil || IsInfra(err) {
		t.Errorf("no timeout: error = %v", err)
	}
	h.ws.Dir = ""
	if _, err := h.run("A", codeTask()); err == nil || IsInfra(err) {
		t.Errorf("no workspace: error = %v", err)
	}
}

// ---- the rate-limit patterns ----

func TestRateLimited(t *testing.T) {
	limited := []string{
		"Claude AI usage limit reached|1700000000",
		"API Error: 429 Too Many Requests",
		`{"type":"error","error":{"type":"rate_limit_error"}}`,
		"API Error: Overloaded",
		"You've hit your limit · resets 5pm",
		"You've hit your usage limit",
		"You've hit your session limit · resets 2:40pm (UTC)",
		`{"type":"error","error":{"type":"overloaded_error"}}`,
		"HTTP 429",
		"quota exceeded for this project",
		"Credit balance is too low",
		"5-hour limit reached",
	}
	for _, s := range limited {
		if _, ok := rateLimited("ordinary line\n" + s); !ok {
			t.Errorf("%q should be a rate limit", s)
		}
	}

	notLimited := []string{
		"",
		"ok  \texample.com/x\t0.012s",
		"--- FAIL: TestMul (0.00s)",
		"listening on port 4290",
		"undefined: retryAfter",
		"claude exited with code 1",
		"I changed the loop bound",
		// the model's own limits and wording are failures, not the account's limit
		"Error: output token limit reached",
		"context limit reached, stopping",
		"The cache is overloaded; please try again later",
		"Please try again in a moment",
		"retry limit reached after 3 attempts",
	}
	for _, s := range notLimited {
		if line, ok := rateLimited(s); ok {
			t.Errorf("%q is not a rate limit, matched %q", s, line)
		}
	}

	if line, ok := rateLimited("fine", "oops: "+strings.Repeat("x", 500)+" rate limit"); ok && len([]rune(line)) > 201 {
		t.Errorf("the excerpt is %d runes long", len([]rune(line)))
	}
}

// TestUsageLimitIsMarked: only the account's usage limit is marked for a wait, with its reset
// time when the provider reported one; other refusals stay plain infra errors.
func TestUsageLimitIsMarked(t *testing.T) {
	reset := time.Unix(1760970000, 0)
	limitEvent := `{"type":"assistant","api_error":"usage_limit_reached","api_error_params":{"rate_limit_info":{"resetsAt":1760970000}}}`
	errResult := `{"type":"result","subtype":"success","is_error":true,"result":"You've hit your usage limit","total_cost_usd":0}`
	coreLimit := "provider execution failed\n\nfailure: rate_limit (the account's usage limit was reached)\n" +
		"detail: the claude usage limit was reached; it resets at 2026-10-20 15:04 MSK\n"
	coreReset := time.Date(2026, 10, 20, 15, 4, 0, 0, time.Local)
	limitExec := fmt.Sprintf(`{"index":1,"action":"implement","status":"failed","error":%q}`, coreLimit)

	cases := []struct {
		name           string
		config         string
		stdout, stderr string
		run            map[string]string
		exit           string
		usage          bool
		resets         time.Time
	}{
		{name: "claude: limit event with a reset", config: "A", stdout: initEvent + "\n" + limitEvent + "\n" + errResult, exit: "1", usage: true, resets: reset},
		{name: "claude: limit text without a reset", config: "A", stdout: errResult, exit: "1", usage: true},
		{name: "claude: limit event alone", config: "A", stdout: limitEvent, exit: "1", usage: true, resets: reset},
		{name: "claude: a 429 is not waited for", config: "A", stderr: "API Error: 429 rate_limit_error", exit: "1"},
		{name: "claude: an empty balance is not waited for", config: "A", stderr: "Credit balance is too low", exit: "1"},
		{name: "orchestrator: step refused with a reset", config: "B", run: runFiles("failed", "claude exited with code 1", limitExec), exit: "3", usage: true, resets: coreReset},
		{name: "orchestrator: decision call refused before any step", config: "B", run: runFiles("failed", "", ""), stderr: "decision provider claude failed: " + coreLimit, exit: "3", usage: true, resets: coreReset},
		{name: "orchestrator: overloaded is not waited for", config: "B", run: runFiles("failed", "claude exited with code 1", execImplLimit), exit: "3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.run != nil {
				h.runDir(tc.run)
			}
			h.set("FAKE_STDOUT", tc.stdout)
			h.set("FAKE_STDERR", tc.stderr)
			h.set("FAKE_EXIT", tc.exit)
			_, err := h.run(tc.config, codeTask())
			if !IsInfra(err) {
				t.Fatalf("error = %v, want an infra error", err)
			}
			resets, usage := UsageLimit(err)
			if usage != tc.usage || !resets.Equal(tc.resets) {
				t.Errorf("UsageLimit = %v, %v; want %v, %v (%v)", resets, usage, tc.resets, tc.usage, err)
			}
		})
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 8}
	for _, s := range []string{"abcdef", "ghijkl", "mn"} {
		if _, err := b.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.String(); got != "ghijklmn" {
		t.Errorf("tail = %q", got)
	}
}

func TestLineWriterSplitsLines(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(l []byte) { lines = append(lines, string(l)) }}
	for _, chunk := range []string{"ab", "c\nde", "f\n\ng"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.flush()
	if want := []string{"abc", "def", "", "g"}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}
