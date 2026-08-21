package testexec

import (
	"context"
	"errors"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

func TestRunner_Success(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "PASS", ExitCode: 0}},
	}
	runner := New(exec)

	result, err := runner.Run(context.Background(), "go test ./...", "/repo")
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

	result, err := runner.Run(context.Background(), "go test ./...", "/repo")
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

	_, err := runner.Run(context.Background(), "go test ./...", "/repo")
	if err == nil {
		t.Fatal("expected error")
	}
	var execErr *apperr.ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("expected *apperr.ExecutionError, got %T", err)
	}
}
