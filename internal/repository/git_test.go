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
