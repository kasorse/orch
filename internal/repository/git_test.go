package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

func TestOpen_Success(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "true\n", ExitCode: 0}},
	}

	repo, err := Open(context.Background(), "/tmp/repo", exec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.Dir != "/tmp/repo" {
		t.Errorf("expected Dir /tmp/repo, got %s", repo.Dir)
	}
	if len(exec.Calls) != 1 || exec.Calls[0].Executable != "git" {
		t.Fatalf("expected one git call, got %v", exec.Calls)
	}
	// git runs on the operator's behalf and must see their full environment (config,
	// credentials helpers); only model-facing processes get ScrubbedEnv.
	if exec.Calls[0].Env != nil {
		t.Errorf("git calls must inherit the environment (Env nil), got %d entries", len(exec.Calls[0].Env))
	}
}

func TestOpen_NotARepository(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "false\n", ExitCode: 128, Stderr: "not a git repository"}},
	}

	_, err := Open(context.Background(), "/tmp/notrepo", exec)
	if err == nil {
		t.Fatal("expected error")
	}
	var repoErr *apperr.RepositoryError
	if !errors.As(err, &repoErr) {
		t.Fatalf("expected *apperr.RepositoryError, got %T", err)
	}
}

func TestOpen_RunFailure(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Errs: []error{errors.New("git not found")},
	}

	_, err := Open(context.Background(), "/tmp/repo", exec)
	if err == nil {
		t.Fatal("expected error")
	}
	var repoErr *apperr.RepositoryError
	if !errors.As(err, &repoErr) {
		t.Fatalf("expected *apperr.RepositoryError, got %T", err)
	}
}

func TestIsDirty_Clean(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "", ExitCode: 0}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	dirty, err := repo.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Error("expected clean, got dirty")
	}
}

func TestIsDirty_Dirty(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: " M internal/foo.go\n", ExitCode: 0}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	dirty, err := repo.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dirty {
		t.Error("expected dirty, got clean")
	}
}

func TestListFiles_SplitsNULAndDedupes(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: "a.go\x00dir/with space.go\x00a.go\x00", ExitCode: 0}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	files, err := repo.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(files, "|") != "a.go|dir/with space.go" {
		t.Errorf("files = %q", files)
	}
	if got := strings.Join(exec.Calls[0].Args, " "); got != "ls-files -z --cached --others --exclude-standard" {
		t.Errorf("args = %q", got)
	}
}

func TestBranch_DetachedIsEmpty(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{ExitCode: 1}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	branch, err := repo.Branch(context.Background())
	if err != nil || branch != "" {
		t.Errorf("Branch = %q, %v; want empty for detached HEAD", branch, err)
	}
}

func TestChangedFiles_ParsesPorcelainZ(t *testing.T) {
	out := " M internal/a.go\x00R  new.go\x00old.go\x00?? notes/todo.md\x00"
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{Stdout: out, ExitCode: 0}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	files, err := repo.ChangedFiles(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Join(files, "|") != "internal/a.go|new.go|notes/todo.md" {
		t.Errorf("files = %q", files)
	}
}

func TestReviewDiff_GitFailure(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{ExitCode: 128, Stderr: "fatal: bad revision 'HEAD'"}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	_, err := repo.ReviewDiff(context.Background(), ReviewDiffOptions{MaxBytes: 1 << 16})
	var repoErr *apperr.RepositoryError
	if !errors.As(err, &repoErr) {
		t.Fatalf("expected *apperr.RepositoryError, got %T (%v)", err, err)
	}
}

func TestReviewDiff_ExcludesPathsAndExternalDrivers(t *testing.T) {
	exec := &execution.FakeProcessExecutor{Results: []execution.ProcessResult{
		{Stdout: "1\t0\ta.go\x00"}, {Stdout: "diff --git a/a.go b/a.go\n+x\n"}, {},
	}}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	if _, err := repo.ReviewDiff(context.Background(), ReviewDiffOptions{Exclude: []string{".orchestrator/runs"}, MaxBytes: 1 << 16}); err != nil {
		t.Fatal(err)
	}
	if len(exec.Calls) != 3 {
		t.Fatalf("calls = %d, want numstat, patch and the untracked list", len(exec.Calls))
	}
	for i, call := range exec.Calls {
		got := strings.Join(call.Args, " ")
		if !strings.HasSuffix(got, "-- . :(exclude).orchestrator/runs") {
			t.Errorf("git %s lacks the exclude pathspec", got)
		}
		if i < 2 && !strings.Contains(got, "--no-ext-diff --no-textconv") {
			t.Errorf("git %s may run a configured diff driver", got)
		}
	}
}
