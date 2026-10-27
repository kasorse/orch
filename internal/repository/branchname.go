package repository

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

// ShowFile returns the content of the file rel (slash-separated, relative to the repository's
// root) as the commit ref records it, by git show <ref>:<rel>: never the working tree. ref must be
// a full commit SHA and rel a plain path; anything else is refused without running git. A file the
// commit does not hold is not an error: ok is false.
func (r *Repository) ShowFile(ctx context.Context, ref, rel string) (content string, ok bool, err error) {
	if !isFullSHA(ref) {
		return "", false, &apperr.RepositoryError{Msg: fmt.Sprintf("%q is not a full commit SHA", ref)}
	}
	if rel == "" || strings.HasPrefix(rel, "-") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, ".") ||
		strings.ContainsAny(rel, ":\x00\n") {
		return "", false, &apperr.RepositoryError{Msg: fmt.Sprintf("%q is not a plain repository path", rel)}
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"show", ref + ":" + rel}, WorkingDir: r.Dir})
	if err != nil {
		return "", false, &apperr.RepositoryError{Msg: "failed to run git show", Err: err}
	}
	if result.ExitCode != 0 {
		return "", false, nil
	}
	return result.Stdout, true, nil
}

// ValidBranchRef reports whether git accepts name as a branch name (git check-ref-format
// --branch). A name that could be read as an option is false without running git.
func (r *Repository) ValidBranchRef(ctx context.Context, name string) (bool, error) {
	if name == "" || strings.HasPrefix(name, "-") {
		return false, nil
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"check-ref-format", "--branch", name}, WorkingDir: r.Dir})
	if err != nil {
		return false, &apperr.RepositoryError{Msg: "failed to run git check-ref-format", Err: err}
	}
	return result.ExitCode == 0, nil
}
