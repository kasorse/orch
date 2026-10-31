package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/execution"
)

// ErrAuthRefused is wrapped by the error of a Clone or a fetch that git says the remote refused
// for want of a credential (specs/jira-listener/SPEC.md §6.1): the listener stops on it.
var ErrAuthRefused = errors.New("the remote refused the credential")

// ErrDiverged is wrapped by the error of FetchFastForward when the checkout's HEAD is not an
// ancestor of the remote's branch: it has commits of its own, or the remote was rewritten.
var ErrDiverged = errors.New("the checkout has diverged from the remote")

const originRemoteRef = "refs/remotes/origin/"

// maxGitMessage bounds the git output an error message carries.
const maxGitMessage = 2000

// Clone runs
//
//	git clone --no-recurse-submodules -- <url> <dir>
//
// in r.Dir (the directory dir is relative to, if it is; r.Dir need not be a repository). argv
// only; git's terminal prompts are off, so a remote that wants a credential the helper in env does
// not give fails instead of waiting for a person. env is the whole environment of the process
// (nil inherits). A failure that git reports as an authentication refusal wraps ErrAuthRefused.
// dir is not removed on failure: the caller clones into a directory of its own and decides.
func (r *Repository) Clone(ctx context.Context, url, dir string, env []string) error {
	if url == "" || strings.HasPrefix(url, "-") || dir == "" || strings.HasPrefix(dir, "-") {
		return &apperr.RepositoryError{Msg: "git clone: the URL and the directory must be given and must not start with -"}
	}
	if env == nil {
		env = os.Environ()
	}
	env = append(append([]string(nil), env...), "GIT_TERMINAL_PROMPT=0")
	result, err := r.Executor.Run(ctx, execution.Spec{
		Executable: "git", Args: []string{"clone", "--no-recurse-submodules", "--", url, dir}, WorkingDir: r.Dir, Env: env,
	})
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to run git clone", Err: err}
	}
	if result.ExitCode != 0 {
		return remoteError("git clone", result.Stderr)
	}
	return nil
}

// DefaultBranch is the remote's default branch as the checkout knows it, from origin/HEAD
// (refs/remotes/origin/HEAD, which git clone sets). It is an error when origin/HEAD is not set or
// names something that is not a plain branch.
func (r *Repository) DefaultBranch(ctx context.Context) (string, error) {
	result, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"symbolic-ref", "--quiet", originRemoteRef + "HEAD"}, WorkingDir: r.Dir})
	if err != nil {
		return "", &apperr.RepositoryError{Msg: "failed to run git symbolic-ref", Err: err}
	}
	if result.ExitCode != 0 {
		return "", &apperr.RepositoryError{Msg: "origin/HEAD is not set: the default branch of the remote is unknown"}
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(result.Stdout), originRemoteRef)
	if !ok || branch == "HEAD" || !safeBranchName(branch) {
		return "", &apperr.RepositoryError{Msg: fmt.Sprintf("origin/HEAD names %q, which is not a branch", strings.TrimSpace(result.Stdout))}
	}
	return branch, nil
}

// FetchFastForward brings the checked-out branch up to origin/<branch>:
//
//	git fetch --no-recurse-submodules origin
//	git merge --ff-only refs/remotes/origin/<branch>
//
// The caller has checked that the checkout is on branch and clean. A HEAD that is not an
// ancestor of the fetched branch is not merged and gives an error wrapping ErrDiverged; a fetch
// the remote refused for authentication wraps ErrAuthRefused. r.Env is the environment of git
// (nil inherits): the credential helper is in it.
func (r *Repository) FetchFastForward(ctx context.Context, branch string) error {
	if !safeBranchName(branch) {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("%q is not a branch name", branch)}
	}
	env := r.Env
	if env == nil {
		env = os.Environ()
	}
	env = append(append([]string(nil), env...), "GIT_TERMINAL_PROMPT=0")

	fetch, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"fetch", "--no-recurse-submodules", "origin"}, WorkingDir: r.Dir, Env: env})
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to run git fetch", Err: err}
	}
	if fetch.ExitCode != 0 {
		return remoteError("git fetch", fetch.Stderr)
	}

	ref := originRemoteRef + branch
	if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("origin has no branch %s", branch)}
	}
	ancestor, err := r.Executor.Run(ctx, execution.Spec{Executable: "git", Args: []string{"merge-base", "--is-ancestor", "HEAD", ref}, WorkingDir: r.Dir})
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to run git merge-base", Err: err}
	}
	switch ancestor.ExitCode {
	case 0:
	case 1:
		return &apperr.RepositoryError{Msg: fmt.Sprintf("HEAD is not an ancestor of origin/%s", branch), Err: ErrDiverged}
	default:
		return &apperr.RepositoryError{Msg: "git merge-base failed: " + boundedMessage(ancestor.Stderr)}
	}
	_, err = r.git(ctx, "merge", "--ff-only", "--no-edit", ref)
	return err
}

// Switch checks out branch: git switch --quiet <branch>. A local branch that does not exist is
// created from origin/<branch> (git's guess). The caller has checked that the tree is clean.
func (r *Repository) Switch(ctx context.Context, branch string) error {
	if !safeBranchName(branch) {
		return &apperr.RepositoryError{Msg: fmt.Sprintf("%q is not a branch name", branch)}
	}
	_, err := r.git(ctx, "switch", "--quiet", branch)
	return err
}

// remoteError is the error of a git command that talked to a remote and failed.
func remoteError(what, stderr string) error {
	err := &apperr.RepositoryError{Msg: what + " failed: " + boundedMessage(stderr)}
	if authRefused(stderr) {
		err.Err = ErrAuthRefused
	}
	return err
}

// authRefused reports whether git's stderr says the remote refused for want of a credential.
func authRefused(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, marker := range []string{
		"authentication failed",
		"could not read username",
		"could not read password",
		"http basic: access denied",
		"returned error: 401",
		"returned error: 403",
		"invalid credentials",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// boundedMessage is the end of git's stderr, which is where the reason is.
func boundedMessage(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxGitMessage {
		s = "..." + s[len(s)-maxGitMessage:]
	}
	return s
}
