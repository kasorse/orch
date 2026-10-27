package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

// remoteTimeout bounds a call that talks to the remote (specs/delivery/SPEC.md §7.3, A10): fixed,
// not a setting.
const remoteTimeout = 5 * time.Minute

// LsRemoteHead reports whether the remote has the branch refs/heads/<branch> (git ls-remote
// --heads). The pattern is the whole ref and the answer is compared to it, so a branch that only
// ends in the same words is not a match. A remote that cannot be asked is an error, not an
// answer.
func (r *Repository) LsRemoteHead(ctx context.Context, remote, branch string) (bool, error) {
	_, present, err := r.LsRemoteHeadSHA(ctx, remote, branch)
	return present, err
}

// LsRemoteHeadSHA is LsRemoteHead with the commit the remote's branch is at.
func (r *Repository) LsRemoteHeadSHA(ctx context.Context, remote, branch string) (sha string, present bool, err error) {
	if err := checkPushNames(remote, branch); err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	ref := "refs/heads/" + branch
	out, err := r.git(ctx, "ls-remote", "--heads", remote, ref)
	if err != nil {
		return "", false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(line, "\t"); ok && strings.TrimSpace(name) == ref {
			return strings.TrimSpace(sha), true, nil
		}
	}
	return "", false, nil
}

// Push pushes the branch to the remote, once, as the operator (specs/delivery/SPEC.md §7.3):
//
//	git push -u --no-follow-tags <remote> refs/heads/B:refs/heads/B [-o option]...
//
// argv, the full environment (the operator's credential helper and ssh agent), never --force,
// bounded by remoteTimeout. -u records the upstream in the shared git config. Each option is
// passed as it is; one with a line feed or a NUL is refused before git runs, as git itself would
// refuse it. It returns git's stderr whether or not the push succeeded: a GitLab remote answers
// there with the merge request a push option opened. A push that failed is an error whose
// message ends with git's last line; the push may still have reached the remote when it timed
// out, so a caller never repeats it.
func (r *Repository) Push(ctx context.Context, remote, branch string, options []string) (stderr string, err error) {
	return r.push(ctx, true, remote, branch, options)
}

// PushWithoutUpstream is Push without -u, which a story's push uses (specs/delivery/SPEC.md §7.3,
// D22): the upstream is written to the git config that sibling tasks of the same repository, and
// the story run itself, compare while they work.
//
//	git push --no-follow-tags <remote> refs/heads/B:refs/heads/B [-o option]...
func (r *Repository) PushWithoutUpstream(ctx context.Context, remote, branch string, options []string) (stderr string, err error) {
	return r.push(ctx, false, remote, branch, options)
}

func (r *Repository) push(ctx context.Context, upstream bool, remote, branch string, options []string) (stderr string, err error) {
	if err := checkPushNames(remote, branch); err != nil {
		return "", err
	}
	for _, o := range options {
		if strings.ContainsAny(o, "\n\x00") {
			return "", &apperr.RepositoryError{Msg: "a push option holds a line feed or a NUL"}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	args := []string{"push"}
	if upstream {
		args = append(args, "-u")
	}
	args = append(args, "--no-follow-tags", remote, "refs/heads/"+branch+":refs/heads/"+branch)
	for _, o := range options {
		args = append(args, "-o", o)
	}
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: args, WorkingDir: r.Dir})
	if err != nil {
		return result.Stderr, &apperr.RepositoryError{Msg: "failed to run git push", Err: err}
	}
	if result.ExitCode != 0 {
		return result.Stderr, &apperr.RepositoryError{Msg: "git push failed: " + lastLine(result.Stderr)}
	}
	return result.Stderr, nil
}

// checkPushNames refuses a remote or a branch git could read as an option or a refspec.
func checkPushNames(remote, branch string) error {
	for what, name := range map[string]string{"remote": remote, "branch": branch} {
		if name == "" || strings.HasPrefix(name, "-") || strings.ContainsFunc(name, func(r rune) bool { return r <= ' ' || r == 0x7f }) ||
			(what == "branch" && strings.ContainsAny(name, "~^:?*[\\")) {
			return &apperr.RepositoryError{Msg: fmt.Sprintf("%q is not a valid %s name", name, what)}
		}
	}
	return nil
}

// lastLine is the last non-empty line of s, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
