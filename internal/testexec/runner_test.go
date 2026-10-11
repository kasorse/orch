package testexec

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

func TestRunner_Success(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "PASS", ExitCode: 0}},
	}
	runner := New(exec)

	result, err := runner.Run(context.Background(), "go test ./...", "/repo", RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if result.Command != "go test ./..." {
		t.Errorf("expected command to be recorded, got %q", result.Command)
	}
	call := exec.Calls[0]
	if call.Executable != "sh" || len(call.Args) != 2 || call.Args[0] != "-c" || call.Args[1] != "go test ./..." {
		t.Errorf("expected sh -c invocation, got %+v", call)
	}
}

func TestRunner_Failure(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "", Stderr: "FAIL", ExitCode: 1}},
	}
	runner := New(exec)

	result, err := runner.Run(context.Background(), "go test ./...", "/repo", RunOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure")
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
}

func TestRunner_ExecutorError(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Errs: []error{errors.New("spawn failed")},
	}
	runner := New(exec)

	_, err := runner.Run(context.Background(), "go test ./...", "/repo", RunOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	var execErr *apperr.ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("expected *apperr.ExecutionError, got %T", err)
	}
}

func TestRunner_ScrubsParentSessionEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "planted")
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	if _, err := New(exec).Run(context.Background(), "go test ./...", "/repo", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	env := exec.Calls[0].Env
	if env == nil {
		t.Fatal("expected an explicit environment, got nil (inherit)")
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CODE_MESSAGING_TOKEN=") }) {
		t.Error("the test command must not inherit CLAUDE_CODE_MESSAGING_TOKEN")
	}
}

func TestRunner_UsesTheTestAllowlist(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-planted")
	t.Setenv("GOFLAGS", "-count=1")
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	if _, err := New(exec).Run(context.Background(), "go test ./...", "/repo", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	env := exec.Calls[0].Env
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") }) {
		t.Error("the test command inherited ANTHROPIC_API_KEY")
	}
	if !slices.Contains(env, "GOFLAGS=-count=1") {
		t.Error("the test command lost an allowlisted toolchain variable")
	}
}

func TestRunner_ExtraEnvReachesTheCommand(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	opts := RunOptions{Env: []string{"ORCH_BASE_REF=abc123"}}
	if _, err := New(exec).Run(context.Background(), "golangci-lint run", "/repo", opts); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(exec.Calls[0].Env, "ORCH_BASE_REF=abc123") {
		t.Errorf("env = %q, want ORCH_BASE_REF=abc123 in it", exec.Calls[0].Env)
	}
}

func TestRunner_EmptyExtraValueIsStillSet(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	if _, err := New(exec).Run(context.Background(), "true", "/repo", RunOptions{Env: []string{"ORCH_BASE_REF="}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(exec.Calls[0].Env, "ORCH_BASE_REF=") {
		t.Errorf("env = %q", exec.Calls[0].Env)
	}
}

func TestRunner_BaseEnvReplacesTheAllowlist(t *testing.T) {
	t.Setenv("GOFLAGS", "-count=1")
	base := []string{"PATH=/usr/bin", "HOME=/home/u", "TMPDIR=/tmp/outer"}
	for _, sandbox := range []*Sandbox{nil, testSandbox()} {
		exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
		runner := &Runner{Executor: exec, Sandbox: sandbox, BaseEnv: base}
		opts := RunOptions{Env: []string{"ORCH_BASE_REF=abc"}}
		if _, err := runner.Run(context.Background(), "true", "/home/u/repo", opts); err != nil {
			t.Fatal(err)
		}
		call := exec.Calls[0]
		if slices.Contains(call.Env, "GOFLAGS=-count=1") {
			t.Errorf("sandbox %v: BaseEnv must replace execution.TestEnv(), env = %q", sandbox != nil, call.Env)
		}
		if !slices.Contains(call.Env, "PATH=/usr/bin") {
			t.Errorf("sandbox %v: BaseEnv lost PATH: %q", sandbox != nil, call.Env)
		}
		if sandbox == nil {
			if !slices.Contains(call.Env, "ORCH_BASE_REF=abc") || !slices.Contains(call.Env, "TMPDIR=/tmp/outer") {
				t.Errorf("unsandboxed env = %q", call.Env)
			}
			continue
		}
		if !slices.Contains(call.Env, "TMPDIR=/tmp") || slices.Contains(call.Env, "TMPDIR=/tmp/outer") {
			t.Errorf("sandboxed env = %q, want TMPDIR=/tmp only", call.Env)
		}
		if !slices.Contains(call.Args, "ORCH_BASE_REF") {
			t.Errorf("the extra pair must still reach bwrap through --setenv: %q", call.Args)
		}
	}
}

func TestRunner_EmptyBaseEnvIsNotInherit(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	runner := &Runner{Executor: exec, BaseEnv: []string{}}
	if _, err := runner.Run(context.Background(), "true", "/repo", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if exec.Calls[0].Env == nil {
		t.Error("an empty BaseEnv produced a nil Env, which would inherit the orchestrator's environment")
	}
}

func TestRunner_NilBaseEnvKeepsTheAllowlist(t *testing.T) {
	t.Setenv("GOFLAGS", "-count=1")
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	if _, err := (&Runner{Executor: exec}).Run(context.Background(), "true", "/repo", RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(exec.Calls[0].Env, "GOFLAGS=-count=1") {
		t.Errorf("env = %q", exec.Calls[0].Env)
	}
}

func TestRunner_RefusesAnExtraNameOutsideORCH(t *testing.T) {
	for _, kv := range []string{"PATH=/x", "ORCH=1", "orch_base_ref=x", "ORCH_X", "LD_PRELOAD=/x", "ORCH-X=1"} {
		for _, sandbox := range []*Sandbox{nil, testSandbox()} {
			exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
			runner := &Runner{Executor: exec, Sandbox: sandbox}
			_, err := runner.Run(context.Background(), "true", "/repo", RunOptions{Env: []string{kv}})
			var execErr *apperr.ExecutionError
			if !errors.As(err, &execErr) {
				t.Errorf("%q (sandbox %v): expected *apperr.ExecutionError, got %v", kv, sandbox != nil, err)
			}
			if len(exec.Calls) != 0 {
				t.Errorf("%q: the command must not run", kv)
			}
		}
	}
}
