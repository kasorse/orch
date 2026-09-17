package claude

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

func TestClient_BuildsExpectedArgs(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "ok", ExitCode: 0}},
	}
	c := New(fake)

	_, err := c.Execute(context.Background(), provider.Request{
		Prompt:     "do the thing",
		Model:      "opus",
		WorkingDir: "/tmp/repo",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.Calls))
	}
	call := fake.Calls[0]
	if call.Executable != "claude" {
		t.Errorf("expected executable claude, got %q", call.Executable)
	}
	want := []string{"--print", "--permission-mode", "bypassPermissions", "--model", "opus", "do the thing"}
	if len(call.Args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, call.Args)
	}
	for i := range want {
		if call.Args[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, call.Args[i], want[i])
		}
	}
	if call.WorkingDir != "/tmp/repo" {
		t.Errorf("expected working dir /tmp/repo, got %q", call.WorkingDir)
	}
}

func TestClient_NonZeroExit(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "API Error: backend broke", Stderr: "boom", ExitCode: 1}},
	}
	c := New(fake)

	res, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus", WorkingDir: "/repo"})
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}

	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.ExitCode != 1 || provErr.Stderr != "boom" {
		t.Errorf("unexpected provider error: %+v", provErr)
	}
	if provErr.Kind != apperr.FailureExit {
		t.Errorf("expected Kind FailureExit, got %q", provErr.Kind)
	}
	if provErr.Stdout != "API Error: backend broke" {
		t.Errorf("expected Stdout to carry the CLI's stdout, got %q", provErr.Stdout)
	}
	if provErr.Executable != "claude" {
		t.Errorf("expected Executable claude, got %q", provErr.Executable)
	}
	if provErr.WorkingDir != "/repo" {
		t.Errorf("expected WorkingDir /repo, got %q", provErr.WorkingDir)
	}
	for _, arg := range provErr.Args {
		if arg == "x" {
			t.Errorf("expected prompt to be redacted in Args, found raw prompt: %v", provErr.Args)
		}
	}
	// The result returned alongside the error must still carry the CLI's output -
	// callers must never discard it (that was the original reported bug).
	if res.Output != "API Error: backend broke" || res.Stderr != "boom" {
		t.Errorf("expected populated result alongside error, got %+v", res)
	}
}

func TestClient_PropagatesRunError(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Errs: []error{context.Canceled},
	}
	c := New(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}

	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureCancelled {
		t.Errorf("expected Kind FailureCancelled, got %q", provErr.Kind)
	}
}

func TestClient_SpawnFailure(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Errs: []error{&exec.Error{Name: "claude", Err: exec.ErrNotFound}},
	}
	c := New(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}

	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureSpawn {
		t.Errorf("expected Kind FailureSpawn, got %q", provErr.Kind)
	}
}
