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

func TestDiff_IncludesUntrackedFiles(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{
			{Stdout: "diff --git a/foo.go b/foo.go\n+added line\n", ExitCode: 0},
			{Stdout: "?? new_file.go\n M tracked.go\n", ExitCode: 0},
		},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	diff, err := repo.Diff(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(diff, "+added line") {
		t.Errorf("expected diff to contain tracked changes, got: %s", diff)
	}
	if !strings.Contains(diff, "new_file.go") {
		t.Errorf("expected diff to mention untracked file, got: %s", diff)
	}
}

func TestDiff_GitDiffFailure(t *testing.T) {
	exec := &execution.FakeProcessExecutor{
		Results: []execution.ProcessResult{{ExitCode: 1, Stderr: "fatal: bad revision"}},
	}
	repo := &Repository{Dir: "/tmp/repo", Executor: exec}

	_, err := repo.Diff(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var repoErr *apperr.RepositoryError
	if !errors.As(err, &repoErr) {
		t.Fatalf("expected *apperr.RepositoryError, got %T", err)
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
