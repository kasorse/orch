// Package repository wraps the real git executable (via execution.ProcessExecutor) so the
// orchestrator can inspect the repository it operates on without ever mutating it - it
// never stashes, resets, checks out, or reverts user files.
package repository

import (
	"context"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

type Repository struct {
	Dir      string
	Executor execution.ProcessExecutor
}

// Open verifies dir is inside a Git working tree.
func Open(ctx context.Context, dir string, executor execution.ProcessExecutor) (*Repository, error) {
	repo := &Repository{Dir: dir, Executor: executor}

	result, err := executor.Run(ctx, "git", []string{"rev-parse", "--is-inside-work-tree"}, "", dir)
	if err != nil {
		return nil, &apperr.RepositoryError{Msg: "failed to run git", Err: err}
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "true" {
		return nil, &apperr.RepositoryError{Msg: dir + " is not a Git repository"}
	}
	return repo, nil
}

// IsDirty reports whether the working tree has uncommitted changes (staged, unstaged, or
// untracked).
func (r *Repository) IsDirty(ctx context.Context) (bool, error) {
	result, err := r.Executor.Run(ctx, "git", []string{"status", "--porcelain"}, "", r.Dir)
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git status", Err: err}
	}
	if result.ExitCode != 0 {
		return false, &apperr.RepositoryError{Msg: "git status failed: " + result.Stderr}
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

// Diff returns the diff of tracked changes against HEAD, plus a note listing any untracked
// files (which "git diff" does not show).
func (r *Repository) Diff(ctx context.Context) (string, error) {
	diffResult, err := r.Executor.Run(ctx, "git", []string{"diff", "HEAD"}, "", r.Dir)
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git diff", Err: err}
	}
	if diffResult.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git diff failed: " + diffResult.Stderr}
	}

	statusResult, err := r.Executor.Run(ctx, "git", []string{"status", "--porcelain"}, "", r.Dir)
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git status", Err: err}
	}
	if statusResult.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git status failed: " + statusResult.Stderr}
	}

	var untracked []string
	for _, line := range strings.Split(statusResult.Stdout, "\n") {
		if strings.HasPrefix(line, "?? ") {
			untracked = append(untracked, strings.TrimPrefix(line, "?? "))
		}
	}

	diff := diffResult.Stdout
	if len(untracked) > 0 {
		diff += "\n\nUntracked files (not shown in diff above):\n  " + strings.Join(untracked, "\n  ")
	}
	return diff, nil
}
