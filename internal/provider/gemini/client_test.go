package gemini

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/provider"
)

func TestClient_BuildsExpectedArgs(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "ok", ExitCode: 0}},
	}
	c := New(fake)

	_, err := c.run(context.Background(), provider.Request{Prompt: "do the thing", Model: "pro"})
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
		Results: []execution.ProcessResult{{Stdout: "backend error", Stderr: "boom", ExitCode: 1}},
	}
	c := New(fake)

	res, err := c.run(context.Background(), provider.Request{Prompt: "x", Model: "pro", WorkingDir: "/repo"})
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
	if provErr.Executable != "gemini" {
		t.Errorf("expected Executable gemini, got %q", provErr.Executable)
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

	_, err := c.run(context.Background(), provider.Request{Prompt: "x", Model: "pro"})
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureCancelled {
		t.Errorf("expected Kind FailureCancelled, got %q", provErr.Kind)
	}
}

// TestClient_TimeoutPreservesExitCode guards against a real bug: the executor reports
// -1 (Go's ExitCode() value for "terminated by signal") for a process SIGKILLed on
// timeout, but that used to get dropped on the floor - the ProviderError literal for
// this error path never copied res.ExitCode across, so it always rendered as "exit
// code: 0", indistinguishable from a clean success.
func TestClient_TimeoutPreservesExitCode(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{ExitCode: -1}},
		Errs:    []error{context.DeadlineExceeded},
	}
	c := New(fake)

	_, err := c.run(context.Background(), provider.Request{Prompt: "x", Model: "pro"})
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureTimeout {
		t.Errorf("expected Kind FailureTimeout, got %q", provErr.Kind)
	}
	if provErr.ExitCode != -1 {
		t.Errorf("expected ExitCode -1 to propagate from the executor, got %d", provErr.ExitCode)
	}
}

func TestClient_SpawnFailure(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Errs: []error{&exec.Error{Name: "gemini", Err: exec.ErrNotFound}},
	}
	c := New(fake)

	_, err := c.run(context.Background(), provider.Request{Prompt: "x", Model: "pro"})
	var provErr *apperr.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("expected *apperr.ProviderError, got %T", err)
	}
	if provErr.Kind != apperr.FailureSpawn {
		t.Errorf("expected Kind FailureSpawn, got %q", provErr.Kind)
	}
}

// TestClient_SupportsNothing pins the fail-closed default: no translation for gemini is
// verified, so every route to it is refused (specs/permissions/SPEC.md §6.4).
func TestClient_SupportsNothing(t *testing.T) {
	c := New(&execution.FakeProcessExecutor{})
	for _, a := range []domain.Authority{domain.AuthorityNone, domain.AuthorityWeb, domain.AuthorityRead, domain.AuthorityEdit} {
		if c.Supports(a) {
			t.Errorf("Supports(%q) = true, want false", a)
		}
	}
}

func TestClient_ExecuteRefusesEveryAuthority(t *testing.T) {
	for _, a := range []domain.Authority{"", domain.AuthorityNone, domain.AuthorityRead, domain.AuthorityEdit} {
		fake := &execution.FakeProcessExecutor{}
		_, err := New(fake).Execute(context.Background(), provider.Request{Prompt: "x", Model: "m", Authority: a})
		var authErr *apperr.AuthorityError
		if !errors.As(err, &authErr) {
			t.Errorf("authority %q: expected *apperr.AuthorityError, got %T: %v", a, err, err)
		}
		if len(fake.Calls) != 0 {
			t.Errorf("authority %q: expected no process, got %d", a, len(fake.Calls))
		}
	}
}
