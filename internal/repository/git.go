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

	result, err := executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"rev-parse", "--is-inside-work-tree"}, WorkingDir: dir})
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
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"status", "--porcelain"}, WorkingDir: r.Dir})
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
	diffResult, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"diff", "HEAD"}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git diff", Err: err}
	}
	if diffResult.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git diff failed: " + diffResult.Stderr}
	}

	statusResult, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"status", "--porcelain"}, WorkingDir: r.Dir})
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

// ListFiles returns every file git would consider part of the project: tracked files
// plus untracked ones not excluded by .gitignore, as slash-separated paths relative to
// Dir. Ignored files never appear, so callers get .gitignore handling for free.
func (r *Repository) ListFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var files []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" && !seen[p] { // a file both cached and modified is listed once per stage
			seen[p] = true
			files = append(files, p)
		}
	}
	return files, nil
}

// Branch returns the current branch name, or "" when HEAD is detached. It works in a
// repository without commits too.
func (r *Repository) Branch(ctx context.Context) (string, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git symbolic-ref", Err: err}
	}
	if result.ExitCode != 0 { // detached HEAD
		return "", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

// ChangedFiles returns the paths with uncommitted changes (staged, unstaged, or
// untracked), relative to Dir. A rename is reported under its new path, and an
// untracked directory as the directory itself rather than every file in it.
func (r *Repository) ChangedFiles(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	var files []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		files = append(files, e[3:])
		if e[0] == 'R' || e[0] == 'C' { // -z puts the original path in the next entry
			i++
		}
	}
	return files, nil
}

func (r *Repository) git(ctx context.Context, args ...string) (string, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: args, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git " + args[0], Err: err}
	}
	if result.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "git " + args[0] + " failed: " + result.Stderr}
	}
	return result.Stdout, nil
}
