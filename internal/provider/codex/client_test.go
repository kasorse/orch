package codex

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

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "do the thing", Model: "gpt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := fake.Calls[0]
	if call.Executable != "codex" {
		t.Errorf("expected executable codex, got %q", call.Executable)
	}
	want := []string{"exec", "do the thing", "--model", "gpt"}
	if len(call.Args) != len(want) {
		t.Fatalf("expected args %v, got %v", want, call.Args)
	}
	for i := range want {
		if call.Args[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, call.Args[i], want[i])
		}
	}
}

func TestClient_NonZeroExit(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "backend error", Stderr: "boom", ExitCode: 1}},
	}
	c := New(fake)

	res, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "gpt", WorkingDir: "/repo"})
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureExit {
		t.Errorf("expected Kind FailureExit, got %q", provErr.Kind)
	}
	if provErr.Stdout != "backend error" {
		t.Errorf("expected Stdout to carry the CLI's stdout, got %q", provErr.Stdout)
	}
	if provErr.Executable != "codex" {
		t.Errorf("expected Executable codex, got %q", provErr.Executable)
	}
	if provErr.WorkingDir != "/repo" {
		t.Errorf("expected WorkingDir /repo, got %q", provErr.WorkingDir)
	}
	for _, arg := range provErr.Args {
		if arg == "x" {
			t.Errorf("expected prompt to be redacted in Args, found raw prompt: %v", provErr.Args)
		}
	}
	if res.Output != "backend error" || res.Stderr != "boom" {
		t.Errorf("expected populated result alongside error, got %+v", res)
	}
}

func TestClient_PropagatesRunError(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Errs: []error{context.Canceled},
	}
	c := New(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "gpt"})
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
		Errs: []error{&exec.Error{Name: "codex", Err: exec.ErrNotFound}},
	}
	c := New(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "gpt"})
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureSpawn {
		t.Errorf("expected Kind FailureSpawn, got %q", provErr.Kind)
	}
}
