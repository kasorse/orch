package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/execution"
)

// ResolveCommit returns the full SHA of the commit ref names, and false when it names none. ref
// is the operator's text: --end-of-options keeps one that starts with "-" from being read as an
// option, and ^{commit} peels a tag.
func (r *Repository) ResolveCommit(ctx context.Context, ref string) (string, bool, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"rev-parse", "--verify", "--quiet", "--end-of-options", ref + "^{commit}"}, WorkingDir: r.Dir})
	if err != nil {
		return "", false, &apperr.RepositoryError{Msg: "failed to run git rev-parse", Err: err}
	}
	sha := strings.TrimSpace(result.Stdout)
	if result.ExitCode != 0 || !isFullSHA(sha) {
		return "", false, nil
	}
	return sha, true, nil
}

// MergeBase returns the best common ancestor of two commits (full SHAs), and false when they
// have none.
func (r *Repository) MergeBase(ctx context.Context, a, b string) (string, bool, error) {
	if !isFullSHA(a) || !isFullSHA(b) {
		return "", false, &apperr.RepositoryError{Msg: fmt.Sprintf("cannot find a merge base: %q and %q are not both full commit SHAs", a, b)}
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"merge-base", a, b}, WorkingDir: r.Dir})
	if err != nil {
		return "", false, &apperr.RepositoryError{Msg: "failed to run git merge-base", Err: err}
	}
	switch result.ExitCode {
	case 0:
		return strings.TrimSpace(result.Stdout), true, nil
	case 1: // no common ancestor
		return "", false, nil
	}
	return "", false, &apperr.RepositoryError{Msg: "git merge-base failed: " + strings.TrimSpace(result.Stderr)}
}

// SameTree reports whether two commits (full SHAs) have the same tree: a change from one to the
// other, if any, changes nothing.
func (r *Repository) SameTree(ctx context.Context, a, b string) (bool, error) {
	if !isFullSHA(a) || !isFullSHA(b) {
		return false, &apperr.RepositoryError{Msg: fmt.Sprintf("cannot compare trees: %q and %q are not both full commit SHAs", a, b)}
	}
	// Not --verify: it takes a single revision, and both are full SHAs already.
	out, err := r.git(ctx, "rev-parse", a+"^{tree}", b+"^{tree}")
	if err != nil {
		return false, err
	}
	trees := strings.Fields(out)
	return len(trees) == 2 && trees[0] == trees[1], nil
}

// CommitSubjects returns the subjects of the commits in base..head, newest first, at most max of
// them, and how many more there were. base and head are full SHAs.
func (r *Repository) CommitSubjects(ctx context.Context, base, head string, limit int) ([]string, int, error) {
	if !isFullSHA(base) || !isFullSHA(head) {
		return nil, 0, &apperr.RepositoryError{Msg: fmt.Sprintf("cannot list commits: %q and %q are not both full commit SHAs", base, head)}
	}
	countOut, err := r.git(ctx, "rev-list", "--count", base+".."+head)
	if err != nil {
		return nil, 0, err
	}
	total, err := strconv.Atoi(strings.TrimSpace(countOut))
	if err != nil || total < 0 {
		return nil, 0, &apperr.RepositoryError{Msg: fmt.Sprintf("git rev-list --count answered %q", strings.TrimSpace(countOut))}
	}
	out, err := r.git(ctx, "log", "-n", strconv.Itoa(limit), "--no-show-signature", "--format=%s%x00", base+".."+head)
	if err != nil {
		return nil, 0, err
	}
	var subjects []string
	for _, s := range strings.Split(out, "\x00") {
		if s = strings.TrimSpace(s); s != "" {
			subjects = append(subjects, s)
		}
	}
	return subjects, max(0, total-len(subjects)), nil
}
