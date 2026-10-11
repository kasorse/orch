package action

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/workflow"
)

// countingGuard counts its comparisons and reports a violation at the violateAt-th one
// (1-based; 0 never).
type countingGuard struct {
	snapshots, compares int
	violateAt           int
}

func (g *countingGuard) Snapshot(ctx context.Context) (GuardState, error) {
	g.snapshots++
	return GuardState{}, nil
}

func (g *countingGuard) Changed(ctx context.Context, before GuardState) ([]string, error) {
	g.compares++
	if g.compares == g.violateAt {
		return []string{".git/hooks/pre-commit"}, nil
	}
	return nil, nil
}

// checkDispatcher is a dispatcher whose runner is scripted: results[i] is the answer of the
// i-th command run.
func checkDispatcher(checks []config.CheckConfig, guard Guard, results ...execution.ProcessResult) (*Dispatcher, *execution.FakeProcessExecutor) {
	exec := &execution.FakeProcessExecutor{Results: results}
	cfg := testConfig()
	cfg.Checks = checks
	d := New(cfg, provider.NewRegistry(), &repository.Repository{Dir: "/repo"}, testexec.New(exec))
	d.Guard = guard
	return d, exec
}

// commandsRun is the shell command of each process the executor ran, in order.
func commandsRun(exec *execution.FakeProcessExecutor) []string {
	var cmds []string
	for _, c := range exec.Calls {
		cmds = append(cmds, c.Args[1])
	}
	return cmds
}

var abChecks = []config.CheckConfig{{Name: "a", Command: "check-a"}, {Name: "b", Command: "check-b"}}

func TestGuardedTest_FailedCheckStopsTheSequence(t *testing.T) {
	guard := &countingGuard{}
	d, exec := checkDispatcher(abChecks, guard,
		execution.ProcessResult{Stdout: "a ok", Duration: time.Second},
		execution.ProcessResult{Stdout: "b out", Stderr: "b err", ExitCode: 1, Duration: 2 * time.Second},
	)
	run := &domain.Run{ID: "run-1", PreRunRef: "abc123"}

	outcome, err := d.Execute(context.Background(), domain.ActionTest, run)
	if err != nil {
		t.Fatal(err)
	}
	if got := commandsRun(exec); !slices.Equal(got, []string{"check-a", "check-b"}) {
		t.Errorf("commands run = %q, want a and b but not the test command", got)
	}
	if guard.snapshots != 1 || guard.compares != 2 {
		t.Errorf("guard: %d snapshots, %d comparisons; want 1 and 2", guard.snapshots, guard.compares)
	}
	r := outcome.TestResult
	if outcome.Outcome != workflow.OutcomeFailure || r == nil || r.Success || r.FailedCheck != "b" {
		t.Fatalf("outcome = %s, result = %+v", outcome.Outcome, r)
	}
	if r.Command != "check-b" || r.ExitCode != 1 || r.Stdout != "b out" || r.Stderr != "b err" {
		t.Errorf("the top level must be the failing check's: %+v", r)
	}
	want := []domain.CheckResult{
		{Name: "a", ExitCode: 0, Success: true, Duration: time.Second},
		{Name: "b", ExitCode: 1, Success: false, Duration: 2 * time.Second},
	}
	if !slices.Equal(r.Checks, want) {
		t.Errorf("checks = %+v, want %+v", r.Checks, want)
	}
	if r.Duration != 3*time.Second {
		t.Errorf("duration = %s, want the sum of what ran", r.Duration)
	}
	if outcome.RawStdout != "b out" || outcome.RawStderr != "b err" {
		t.Errorf("raw output = %q, %q", outcome.RawStdout, outcome.RawStderr)
	}
	wantLogs := []domain.CheckOutput{{Name: "a", Stdout: "a ok"}, {Name: "b", Stdout: "b out", Stderr: "b err"}}
	if !slices.Equal(outcome.CheckLogs, wantLogs) {
		t.Errorf("check logs = %+v, want %+v", outcome.CheckLogs, wantLogs)
	}
	if d.pendingTestFailure == nil || d.pendingTestFailure.FailedCheck != "b" {
		t.Errorf("the retry context must carry the failed check: %+v", d.pendingTestFailure)
	}
}

func TestGuardedTest_ViolationAfterACheckStopsBeforeTheNext(t *testing.T) {
	guard := &countingGuard{violateAt: 1}
	d, exec := checkDispatcher(abChecks, guard)
	run := &domain.Run{ID: "run-1", PreRunRef: "abc123"}

	outcome, err := d.Execute(context.Background(), domain.ActionTest, run)
	var wfErr *apperr.WorkflowError
	if !errors.As(err, &wfErr) || !strings.Contains(err.Error(), "test changed protected paths") ||
		!strings.Contains(err.Error(), ".git/hooks/pre-commit") {
		t.Fatalf("expected the guard's protected-path error, got %v", err)
	}
	if got := commandsRun(exec); !slices.Equal(got, []string{"check-a"}) {
		t.Errorf("commands run = %q, want only a: b must not run on a tree with a planted hook", got)
	}
	if guard.compares != 1 {
		t.Errorf("comparisons = %d, want 1", guard.compares)
	}
	if outcome.TestResult == nil || len(outcome.TestResult.Checks) != 1 || len(outcome.CheckLogs) != 1 {
		t.Errorf("the partial result must keep what ran: %+v", outcome)
	}
}

func TestGuardedTest_ViolationAfterTheTestCommand(t *testing.T) {
	guard := &countingGuard{violateAt: 3}
	d, exec := checkDispatcher(abChecks, guard)
	_, err := d.Execute(context.Background(), domain.ActionTest, &domain.Run{ID: "run-1"})
	if err == nil || !strings.Contains(err.Error(), "test changed protected paths") {
		t.Fatalf("got %v", err)
	}
	if len(exec.Calls) != 3 || guard.compares != 3 {
		t.Errorf("%d commands, %d comparisons; want 3 and 3", len(exec.Calls), guard.compares)
	}
}

func TestGuardedTest_AllGreenRunsTheTestCommandLast(t *testing.T) {
	guard := &countingGuard{}
	d, exec := checkDispatcher(abChecks, guard,
		execution.ProcessResult{Stdout: "a"}, execution.ProcessResult{Stdout: "b"},
		execution.ProcessResult{Stdout: "PASS", Duration: time.Second},
	)
	run := &domain.Run{ID: "run-1", PreRunRef: "abc123"}

	outcome, err := d.Execute(context.Background(), domain.ActionTest, run)
	if err != nil {
		t.Fatal(err)
	}
	if got := commandsRun(exec); !slices.Equal(got, []string{"check-a", "check-b", "go test ./..."}) {
		t.Errorf("commands run = %q", got)
	}
	if guard.snapshots != 1 || guard.compares != 3 {
		t.Errorf("guard: %d snapshots, %d comparisons; want 1 and 3", guard.snapshots, guard.compares)
	}
	r := outcome.TestResult
	if outcome.Outcome != workflow.OutcomeSuccess || r == nil || !r.Success || r.FailedCheck != "" {
		t.Fatalf("outcome = %s, result = %+v", outcome.Outcome, r)
	}
	if r.Command != "go test ./..." || r.Stdout != "PASS" || len(r.Checks) != 2 || r.Duration != time.Second {
		t.Errorf("the top level must be the test command's, with both checks listed: %+v", r)
	}
	for i, c := range exec.Calls {
		if !slices.Contains(c.Env, "ORCH_BASE_REF=abc123") {
			t.Errorf("command %d lacks ORCH_BASE_REF=abc123: %q", i, c.Env)
		}
	}
}

func TestGuardedTest_FailingTestCommandAfterGreenChecks(t *testing.T) {
	d, _ := checkDispatcher(abChecks, &countingGuard{}, execution.ProcessResult{}, execution.ProcessResult{},
		execution.ProcessResult{Stderr: "FAIL", ExitCode: 1})
	outcome, err := d.Execute(context.Background(), domain.ActionTest, &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	r := outcome.TestResult
	if outcome.Outcome != workflow.OutcomeFailure || r.FailedCheck != "" || r.Command != "go test ./..." || len(r.Checks) != 2 {
		t.Errorf("a failing test command is not a failed check: %+v", r)
	}
}

func TestGuardedTest_NoChecksIsUnchanged(t *testing.T) {
	guard := &countingGuard{}
	d, exec := checkDispatcher(nil, guard, execution.ProcessResult{Stdout: "PASS", Duration: time.Second})
	outcome, err := d.Execute(context.Background(), domain.ActionTest, &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := commandsRun(exec); !slices.Equal(got, []string{"go test ./..."}) || guard.snapshots != 1 || guard.compares != 1 {
		t.Errorf("commands %q, %d snapshots, %d comparisons; want one of each", got, guard.snapshots, guard.compares)
	}
	r := outcome.TestResult
	if r.Checks != nil || r.FailedCheck != "" || r.Duration != time.Second || len(outcome.CheckLogs) != 0 {
		t.Errorf("a run without checks must look as before: %+v", r)
	}
	// An empty PreRunRef is still set, so every command sees the same environment.
	if !slices.Contains(exec.Calls[0].Env, "ORCH_BASE_REF=") {
		t.Errorf("env = %q, want ORCH_BASE_REF= (empty)", exec.Calls[0].Env)
	}
}

func TestPreRunTest_RunsTheChecksAndKeepsTheRedOne(t *testing.T) {
	guard := &countingGuard{}
	d, exec := checkDispatcher(abChecks, guard, execution.ProcessResult{}, execution.ProcessResult{Stdout: "bad", ExitCode: 2})
	result, logs, err := d.PreRunTest(context.Background(), &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.FailedCheck != "b" || result.ExitCode != 2 || len(logs) != 2 || len(exec.Calls) != 2 {
		t.Errorf("result = %+v, %d logs, %d commands", result, len(logs), len(exec.Calls))
	}
	if d.preRunFailure == nil || d.preRunFailure.FailedCheck != "b" {
		t.Errorf("the first implement must be told of the failed check: %+v", d.preRunFailure)
	}
}

const goTextFailure = "--- FAIL: TestPut (0.00s)\n    store_test.go:12: boom\nFAIL\nFAIL\texample.com/m/store\t0.004s\n"

func TestGuardedTest_NamesTheFailingTests(t *testing.T) {
	d, _ := checkDispatcher(nil, &countingGuard{}, execution.ProcessResult{Stdout: goTextFailure, ExitCode: 1})
	outcome, err := d.Execute(context.Background(), domain.ActionTest, &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	r := outcome.TestResult
	if r.Success || !slices.Equal(r.FailedTests, []string{"example.com/m/store.TestPut"}) || r.ReportFormat != "gotext" {
		t.Errorf("failed tests = %q, format = %q", r.FailedTests, r.ReportFormat)
	}
	if d.pendingTestFailure == nil || len(d.pendingTestFailure.FailedTests) != 1 {
		t.Errorf("the retry context must carry the names: %+v", d.pendingTestFailure)
	}
}

func TestGuardedTest_NoNamesForAPassOrAnUnknownFormat(t *testing.T) {
	// The output of a green run may well show failures (a test that expects one): only the
	// exit code decides, and the names are recorded for a failed run alone.
	d, _ := checkDispatcher(nil, &countingGuard{}, execution.ProcessResult{Stdout: goTextFailure})
	outcome, err := d.Execute(context.Background(), domain.ActionTest, &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if r := outcome.TestResult; !r.Success || r.FailedTests != nil || r.ReportFormat != "" {
		t.Errorf("a green result carries test names: %+v", r)
	}

	d, _ = checkDispatcher(nil, &countingGuard{}, execution.ProcessResult{Stderr: "segfault", ExitCode: 139})
	outcome, err = d.Execute(context.Background(), domain.ActionTest, &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if r := outcome.TestResult; r.Success || r.FailedTests != nil || r.ReportFormat != "" {
		t.Errorf("an unrecognised output must leave the result as before: %+v", r)
	}
}

func TestPreRunTest_NamesTheFailingTests(t *testing.T) {
	d, _ := checkDispatcher(nil, &countingGuard{}, execution.ProcessResult{Stdout: goTextFailure, ExitCode: 1})
	result, _, err := d.PreRunTest(context.Background(), &domain.Run{ID: "run-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.FailedTests, []string{"example.com/m/store.TestPut"}) || result.ReportFormat != "gotext" {
		t.Errorf("pre-run result = %+v", result)
	}
}

func TestGuardedTest_RunnerErrorHasNoResult(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}, Errs: []error{nil, errors.New("spawn failed")}}
	cfg := testConfig()
	cfg.Checks = abChecks
	d := New(cfg, provider.NewRegistry(), &repository.Repository{Dir: "/repo"}, testexec.New(exec))
	d.Guard = &countingGuard{}
	result, _, err := d.guardedTest(context.Background(), &domain.Run{ID: "run-1"}, domain.ActionTest)
	var execErr *apperr.ExecutionError
	if result != nil || !errors.As(err, &execErr) {
		t.Errorf("result = %+v, err = %v", result, err)
	}
}
