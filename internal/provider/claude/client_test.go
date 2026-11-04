package claude

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/provider"
)

// common is the prefix every authority shares (specs/permissions/SPEC.md §6.2).
var common = []string{
	"--print",
	"--input-format", "text",
	"--output-format", "stream-json",
	"--verbose",
	"--restricted",
	"--permission-mode", "dontAsk",
	"--permission-prompts", "none",
	"--strict-mcp-config",
	"--setting-sources", "",
}

// newLogin is a client under the CLI's own login: a Client whose auth was never set refuses
// to run, and most tests are not about the credential.
func newLogin(e execution.ProcessExecutor) *Client {
	c := New(e)
	c.SetAuth(domain.ClaudeAuthLogin, "", nil)
	return c
}

func TestClient_BuildsExpectedArgsPerAuthority(t *testing.T) {
	cases := []struct {
		authority domain.Authority
		profile   []string
	}{
		{domain.AuthorityNone, []string{"--tools", ""}},
		{domain.AuthorityWeb, []string{"--tools", "WebSearch,WebFetch", "--allowed-tools", "WebSearch,WebFetch"}},
		{domain.AuthorityRead, []string{"--tools", "Read,Grep,Glob"}},
		{domain.AuthorityEdit, []string{"--tools", "Read,Grep,Glob,Edit,Write", "--allowed-tools", "Edit,Write"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.authority), func(t *testing.T) {
			fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
			_, err := newLogin(fake).Execute(context.Background(), provider.Request{
				Prompt: "do the thing", Model: "opus", WorkingDir: "/tmp/repo", Authority: tc.authority,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(fake.Calls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(fake.Calls))
			}
			call := fake.Calls[0]
			if call.Executable != "claude" || call.WorkingDir != "/tmp/repo" {
				t.Errorf("unexpected call %+v", call)
			}
			want := append(append(append([]string{}, common...), tc.profile...), "--model", "opus")
			if !slices.Equal(call.Args, want) {
				t.Errorf("args =\n  %q\nwant\n  %q", call.Args, want)
			}
		})
	}
}

func TestClient_DenyReadBecomesSettings(t *testing.T) {
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	_, err := newLogin(fake).Execute(context.Background(), provider.Request{
		Prompt: "p", Model: "opus", Authority: domain.AuthorityRead, DenyRead: []string{".env", "*.pem"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	settings := `{"permissions":{"deny":["Read(**/.env)","Read(**/.env/**)","Read(**/*.pem)","Read(**/*.pem/**)"]}}`
	want := append(append([]string{}, common...), "--settings", settings, "--tools", "Read,Grep,Glob", "--model", "opus")
	if got := fake.Calls[0].Args; !slices.Equal(got, want) {
		t.Errorf("args =\n  %q\nwant\n  %q", got, want)
	}
}

// TestClient_PromptOnStdin: a prompt far over the per-argument limit travels on stdin and
// no argv element comes near it.
func TestClient_PromptOnStdin(t *testing.T) {
	prompt := strings.Repeat("x", 1<<20)
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	_, err := newLogin(fake).Execute(context.Background(), provider.Request{
		Prompt: prompt, Model: "opus", Authority: domain.AuthorityRead,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := fake.Calls[0]
	if call.Stdin != prompt {
		t.Errorf("Stdin is not the prompt (len %d)", len(call.Stdin))
	}
	for _, a := range call.Args {
		if len(a) > 4096 {
			t.Errorf("argv element of %d bytes", len(a))
		}
	}
}

func TestClient_RefusesMissingOrUnknownAuthority(t *testing.T) {
	for _, a := range []domain.Authority{"", "all"} {
		fake := &execution.FakeProcessExecutor{}
		_, err := newLogin(fake).Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus", Authority: a})
		var authErr *apperr.AuthorityError
		if !errors.As(err, &authErr) {
			t.Errorf("authority %q: expected *apperr.AuthorityError, got %T: %v", a, err, err)
		}
		if len(fake.Calls) != 0 {
			t.Errorf("authority %q: expected no process, got %d", a, len(fake.Calls))
		}
	}
}

func TestClient_NonZeroExit(t *testing.T) {
	fake := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "API Error: backend broke", Stderr: "boom", ExitCode: 1}},
	}
	c := newLogin(fake)

	res, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus", WorkingDir: "/repo", Authority: domain.AuthorityRead})
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
	c := newLogin(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus", Authority: domain.AuthorityRead})
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
	c := newLogin(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus", Authority: domain.AuthorityRead})
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
		Errs: []error{&exec.Error{Name: "claude", Err: exec.ErrNotFound}},
	}
	c := newLogin(fake)

	_, err := c.Execute(context.Background(), provider.Request{Prompt: "x", Model: "opus", Authority: domain.AuthorityRead})
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

func TestClient_Supports(t *testing.T) {
	c := New(&execution.FakeProcessExecutor{})
	for _, a := range []domain.Authority{domain.AuthorityNone, domain.AuthorityWeb, domain.AuthorityRead, domain.AuthorityEdit} {
		if !c.Supports(a) {
			t.Errorf("Supports(%q) = false, want true", a)
		}
	}
	for _, a := range []domain.Authority{"", "all"} {
		if c.Supports(a) {
			t.Errorf("Supports(%q) = true, want false", a)
		}
	}
}

func TestClient_ScrubsParentSessionEnv(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "/tmp/sock")
	fake := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	if _, err := newLogin(fake).Execute(context.Background(), provider.Request{Prompt: "x", Model: "m", Authority: domain.AuthorityRead}); err != nil {
		t.Fatal(err)
	}
	env := fake.Calls[0].Env
	if env == nil {
		t.Fatal("expected an explicit environment, got nil (inherit)")
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CODE_MESSAGING_SOCKET=") {
			t.Errorf("parent-session variable leaked: %s", kv)
		}
	}
}

func TestEffortFlagOnlyWhenSet(t *testing.T) {
	with := buildArgs(domain.AuthorityRead, nil, "opus", "high", provider.Extensions{})
	i := slices.Index(with, "--effort")
	if i < 0 || with[i+1] != "high" || with[len(with)-3] != "--effort" && with[i+2] != "--model" {
		t.Errorf("effort flag missing or misplaced: %q", with)
	}
	if slices.Contains(buildArgs(domain.AuthorityRead, nil, "opus", "", provider.Extensions{}), "--effort") {
		t.Error("--effort passed with no effort configured")
	}
}
