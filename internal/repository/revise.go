package repository

import (
	"context"
	"fmt"
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

// DeleteBranch deletes the local branch, but only while it is at sha (git update-ref -d).
func (r *Repository) DeleteBranch(ctx context.Context, branch, sha string) error {
	if !validBranchName(branch) {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("cannot delete %q: not a valid branch name", branch)}
	}
	if err := checkRefs("delete a branch", sha); err != nil {
		return err
	}
	_, err := r.git(ctx, "update-ref", "-d", "refs/heads/"+branch, sha)
	return err
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
