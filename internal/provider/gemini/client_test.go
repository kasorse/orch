package gemini

import (
	"context"
	"errors"
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

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "do the thing", Model: "pro"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := fake.Calls[0]
	if call.Executable != "gemini" {
		t.Errorf("expected executable gemini, got %q", call.Executable)
	}
	want := []string{"-p", "do the thing", "-m", "pro"}
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
		Results: []execution.ProcessResult{{Stderr: "boom", ExitCode: 1}},
	}
	c := New(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "pro"})
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
}
