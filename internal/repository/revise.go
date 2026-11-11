package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kasorse/orch/internal/apperr"
)

// ReviseRef is where orchestrator revise fetches a remote branch to (specs/revise/SPEC.md §6.1):
// under refs/orchestrator/, never refs/remotes/ or refs/heads/. suffix is "" for the merge
// request's branch and "-target" for its target.
func ReviseRef(runID, suffix string) string {
	return "refs/orchestrator/revise/" + runID + suffix
}

// FetchBranch fetches the remote's branch into ref (a ReviseRef), forced, without tags, and
// returns the commit it fetched. It is argv-only with the full environment (the operator's
// credential helper), bounded like a push.
func (r *Repository) FetchBranch(ctx context.Context, remote, branch, ref string) (string, error) {
	if err := checkPushNames(remote, branch); err != nil {
		return "", err
	}
	if !strings.HasPrefix(ref, "refs/orchestrator/revise/") || strings.ContainsAny(ref, " :~^?*[\\") || strings.Contains(ref, "..") {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot fetch into %q", ref)}
	}
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	if _, err := r.git(ctx, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", remote, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", err
	}
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AddWorktreeOnBranch creates a linked worktree at path (absolute) on the existing local branch
// and returns its administrative directory. Git refuses a branch checked out elsewhere.
func (r *Repository) AddWorktreeOnBranch(ctx context.Context, path, branch string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not an absolute path", path)}
	}
	if !validBranchName(branch) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("cannot add a worktree: %q is not a valid branch name", branch)}
	}
	if _, err := r.git(ctx, "worktree", "add", "--quiet", path, branch); err != nil {
		return "", err
	}
	wt := &Repository{Dir: path, Executor: r.Executor}
	out, err := wt.git(ctx, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CheckedOutAt is the worktree the local branch is checked out in, "" for none.
func (r *Repository) CheckedOutAt(ctx context.Context, branch string) (string, error) {
	list, err := r.ListWorktrees(ctx)
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Branch == "refs/heads/"+branch {
			return w.Path, nil
		}
	}
	return "", nil
}
