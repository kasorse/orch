package repository

import (
	"context"
	"path/filepath"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

// CommitAll stages every change in the working tree - tracked changes, deletions and
// untracked files, minus .gitignore'd ones and the orchestrator's run directory - and
// commits it on the current branch with message, as the user's own git identity and with
// the repository's own hooks. It returns the new commit's SHA. This is the one call that
// writes the user's index and a branch, and only on the operator's explicit approval
// (MVP.md §42). If the commit fails after staging (a hook refused it, no identity), the
// changes stay staged and the error says so.
func (r *Repository) CommitAll(ctx context.Context, message string) (string, error) {
	top, err := r.TopLevel(ctx)
	if err != nil {
		return "", err
	}
	pathspecs := []string{":(top)"}
	// Excluding a path that is itself .gitignore'd makes git add exit non-zero ("paths
	// are ignored") although it stages everything else; an ignored run directory needs no
	// exclusion.
	ignored, err := r.Executor.Run(ctx, execution.Spec{Executable: "git",
		Args: []string{"check-ignore", "-q", "--", filepath.Join(top, ".orchestrator")}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git check-ignore", Err: err}
	}
	if ignored.ExitCode != 0 {
		pathspecs = append(pathspecs, ":(exclude,top).orchestrator")
	}
	if _, err := r.git(ctx, append([]string{"add", "-A", "--"}, pathspecs...)...); err != nil {
		return "", err
	}
	// The message goes on stdin: it carries the task's text, which may start with "-".
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git",
		Args: []string{"commit", "--quiet", "--file=-"}, Stdin: message, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git commit", Err: err}
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr + "\n" + result.Stdout)
		return "", &apperr.RepositoryError{Msg: "git commit failed (the changes are staged): " + detail}
	}
	return r.HeadRef(ctx)
}
