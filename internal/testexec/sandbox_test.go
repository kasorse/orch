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

func testSandbox() *Sandbox {
	return &Sandbox{
		Home:      "/home/u",
		RepoRoot:  "/home/u/repo",
		GitDirs:   []string{"/home/u/repo/.git"},
		ReadOnly:  []string{"/home/u/go/pkg/mod"},
		ReadWrite: []string{"/home/u/.cache/go-build"},
	}
}

// TestSandboxArgs pins the whole argv: the order of the mounts is what makes the sandbox
// hold (SPEC.md §8).
func TestSandboxArgs(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{Stdout: "ok"}}}
	runner := &Runner{Executor: exec, Sandbox: testSandbox()}

	result, err := runner.Run(context.Background(), "go test ./... && echo done", "/home/u/repo/sub", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	call := exec.Calls[0]
	want := []string{
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp",
		"--tmpfs", "/home/u", // before anything under it
		"--bind", "/home/u/repo", "/home/u/repo",
		"--ro-bind", "/home/u/repo/.git", "/home/u/repo/.git", // after the repository bind
		"--ro-bind", "/home/u/go/pkg/mod", "/home/u/go/pkg/mod",
		"--bind", "/home/u/.cache/go-build", "/home/u/.cache/go-build",
		"--unshare-user", "--unshare-net", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--die-with-parent", "--new-session",
		"--chdir", "/home/u/repo/sub", "sh", "-c", "go test ./... && echo done",
	}
	if call.Executable != "bwrap" || !slices.Equal(call.Args, want) {
		t.Errorf("got %s %q\nwant bwrap %q", call.Executable, call.Args, want)
	}
	if !result.Sandboxed || result.Command != "go test ./... && echo done" {
		t.Errorf("result = %+v", result)
	}
}

// TestSandboxArgs_WorktreePointer: in a linked worktree the .git file is bound read-only
// after the worktree's writable bind and the git directories; before it, the repository bind
// would make it writable again (specs/worktree/SPEC.md §12).
func TestSandboxArgs_WorktreePointer(t *testing.T) {
	sb := &Sandbox{
		Home:          "/home/u",
		RepoRoot:      "/state/wt/run",
		GitDirs:       []string{"/home/u/repo/.git/worktrees/run", "/home/u/repo/.git"},
		ReadOnlyFiles: []string{"/state/wt/run/.git"},
	}
	args := sb.args("/state/wt/run", "go test ./...", nil)
	bindAt := func(flag, path string) int {
		for i := 0; i+2 < len(args); i++ {
			if args[i] == flag && args[i+1] == path && args[i+2] == path {
				return i
			}
		}
		t.Fatalf("argv lacks %s %s %s: %q", flag, path, path, args)
		return -1
	}
	file := bindAt("--ro-bind", "/state/wt/run/.git")
	if repo := bindAt("--bind", "/state/wt/run"); file < repo {
		t.Errorf("the .git file bind (%d) comes before the worktree bind (%d): %q", file, repo, args)
	}
	for _, d := range sb.GitDirs {
		if at := bindAt("--ro-bind", d); file < at {
			t.Errorf("the .git file bind (%d) comes before the git directory %s (%d)", file, d, at)
		}
	}
	if iso := slices.Index(args, "--unshare-user"); file > iso {
		t.Errorf("the .git file bind (%d) comes after the isolation flags (%d)", file, iso)
	}
}

func TestSandboxEnv(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp/claude-1000")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "planted")
	// A name only this test uses: the credential set is process-wide and only grows, and
	// its reset helper is private to package execution.
	t.Setenv("ORCH_SANDBOX_TEST_TOKEN", "planted")
	execution.ScrubCredentials("ORCH_SANDBOX_TEST_TOKEN")
	env := testSandbox().env(execution.TestEnv())
	tmp := slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return !strings.HasPrefix(kv, "TMPDIR=") })
	if !slices.Equal(tmp, []string{"TMPDIR=/tmp"}) {
		t.Errorf("TMPDIR entries = %q, want exactly TMPDIR=/tmp", tmp)
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CODE_MESSAGING_TOKEN=") }) {
		t.Error("the sandboxed environment must be scrubbed too")
	}
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "ORCH_SANDBOX_TEST_TOKEN=") }) {
		t.Error("the sandboxed environment must not carry a credential variable")
	}
	t.Setenv("AWS_SECRET_ACCESS_KEY", "planted")
	if slices.ContainsFunc(testSandbox().env(execution.TestEnv()), func(kv string) bool { return strings.HasPrefix(kv, "AWS_SECRET_ACCESS_KEY=") }) {
		t.Error("the sandboxed environment must be the allowlist, not the scrubbed environment")
	}
}

func TestRunner_WithoutSandboxIsUnchanged(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	result, err := New(exec).Run(context.Background(), "go test ./...", "/repo", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	call := exec.Calls[0]
	if call.Executable != "sh" || !slices.Equal(call.Args, []string{"-c", "go test ./..."}) || result.Sandboxed {
		t.Errorf("call = %+v, sandboxed = %v", call, result.Sandboxed)
	}
}

// TestSandboxExtraEnv: the extra pair is a --setenv after every mount and isolation flag,
// before --chdir and the command.
func TestSandboxExtraEnv(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	runner := &Runner{Executor: exec, Sandbox: testSandbox()}
	opts := RunOptions{Env: []string{"ORCH_BASE_REF=abc"}}
	if _, err := runner.Run(context.Background(), "lint", "/home/u/repo", opts); err != nil {
		t.Fatal(err)
	}
	args := exec.Calls[0].Args
	set := slices.Index(args, "--setenv")
	if set < 0 || !slices.Equal(args[set:set+3], []string{"--setenv", "ORCH_BASE_REF", "abc"}) {
		t.Fatalf("argv = %q, want --setenv ORCH_BASE_REF abc", args)
	}
	if last := slices.Index(args, "--die-with-parent"); set < last {
		t.Errorf("--setenv at %d comes before the isolation flags (%d)", set, last)
	}
	if shell := slices.Index(args, "sh"); set > shell || args[set+3] != "--chdir" {
		t.Errorf("--setenv at %d must come right before --chdir and sh -c: %q", set, args)
	}
	// bwrap passes its own environment on, and ORCH_BASE_REF is not in it: it is --setenv's.
	if slices.Contains(exec.Calls[0].Env, "ORCH_BASE_REF=abc") {
		t.Errorf("the sandbox environment must stay the allowlist: %q", exec.Calls[0].Env)
	}
}

func TestProbe(t *testing.T) {
	ok := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{}}}
	if err := Probe(context.Background(), ok); err != nil {
		t.Fatalf("a working bwrap must pass: %v", err)
	}
	if got := ok.Calls[0]; got.Executable != "bwrap" || got.Args[len(got.Args)-1] != "true" ||
		!slices.Contains(got.Args, "--unshare-net") || !slices.Contains(got.Args, "--tmpfs") {
		t.Errorf("probe must use the real mounts and isolation, got %q", got.Args)
	}

	failing := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{{ExitCode: 1, Stderr: "bwrap: setting up uid map: Permission denied\n"}}}
	err := Probe(context.Background(), failing)
	var depErr *apperr.DependencyError
	if !errors.As(err, &depErr) {
		t.Fatalf("expected *apperr.DependencyError, got %T: %v", err, err)
	}
	for _, want := range []string{"setting up uid map: Permission denied", "AppArmor", "userns,", "test.sandbox: none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in:\n%v", want, err)
		}
	}
}
