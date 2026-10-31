package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/execution"
	"orchestrator/internal/listener"
	"orchestrator/internal/repository"
)

// staleClonePrefix starts the name of a clone in progress, in the services directory: a
// directory that begins with it is never a service (a service name starts with a letter or a
// digit), so a clone that was cut off never looks like one (specs/jira-listener/SPEC.md §6.1).
const staleClonePrefix = ".clone-"

// checkoutKind is why a service's checkout could not be made ready. Each is one failure the loop
// maps to SPEC §8 through class.
type checkoutKind string

const (
	checkoutNoProject    checkoutKind = "no_project"          // no project label and no directory
	checkoutNotAllowed   checkoutKind = "project_not_allowed" // outside gitlab.allowed_groups
	checkoutNotWorkTree  checkoutKind = "not_a_work_tree"     // the directory is not a git work tree's top level
	checkoutWrongProject checkoutKind = "wrong_project"       // origin is not the project the ticket names
	checkoutNoDefault    checkoutKind = "no_default_branch"   // origin/HEAD is not set
	checkoutOffDefault   checkoutKind = "off_default_branch"  // on another branch (or detached)
	checkoutDirty        checkoutKind = "dirty"               // uncommitted changes
	checkoutDiverged     checkoutKind = "diverged"            // commits of its own, or origin was rewritten
	checkoutTampered     checkoutKind = "git_config_changed"  // the git config or hooks changed under the checkout's own git calls
	checkoutCloneFailed  checkoutKind = "clone_failed"        // the network or the remote, not the ticket
	checkoutFetchFailed  checkoutKind = "fetch_failed"
	checkoutAuth         checkoutKind = "auth" // the remote refused the credential
)

// checkoutClass is what the loop does with a checkout failure (SPEC §8, §10).
type checkoutClass string

const (
	classBlocked checkoutClass = "blocked" // comment and the blocked transition, no attempt
	classFailed  checkoutClass = "failed"  // comment and the failed transition, an attempt
	classRetry   checkoutClass = "retry"   // no comment, the retry transition with a backoff, no attempt
	classStop    checkoutClass = "stop"    // the listener stops (exit 2)
)

func (k checkoutKind) class() checkoutClass {
	switch k {
	case checkoutNoProject, checkoutNotAllowed:
		return classBlocked
	case checkoutCloneFailed, checkoutFetchFailed:
		return classRetry
	case checkoutAuth:
		return classStop
	}
	return classFailed
}

// checkoutError is a failure of one of those kinds. Msg is a line the comment may quote: it holds
// no URL and no git output. Err, when there is one, is the cause, for the log.
type checkoutError struct {
	Kind checkoutKind
	Msg  string
	Err  error
}

func (e *checkoutError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *checkoutError) Unwrap() error { return e.Err }

func checkoutFail(kind checkoutKind, cause error, format string, args ...any) *checkoutError {
	return &checkoutError{Kind: kind, Msg: fmt.Sprintf(format, args...), Err: cause}
}

// serviceCheckout makes a service's directory ready for a run: it finds or clones the repository
// and brings it to a clean, fresh default branch (SPEC §5, §6). It owns the services directory:
// nobody works in it by hand (D16).
type serviceCheckout struct {
	servicesDir   string
	baseURL       string // gitlab.base_url, without a trailing "/"
	allowedGroups []string
	cloneTimeout  time.Duration // bounds a clone and a fetch
	executor      execution.ProcessExecutor
	// env is the whole environment of the git processes that talk to the remote: the process's own
	// with the credential helper's entries (withListenerGit).
	env []string
	// suffix names a clone in progress; the default is random.
	suffix func() string
}

// checkoutResult is a ready checkout.
type checkoutResult struct {
	Dir     string
	Default string // the default branch, which the checkout is on
	Cloned  bool
}

// removeStaleClones removes the .clone-* directories a crash left in the services directory. It
// runs once at start, when no clone is in progress.
func (c *serviceCheckout) removeStaleClones() error {
	entries, err := os.ReadDir(c.servicesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read the services directory: %w", err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), staleClonePrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(c.servicesDir, e.Name())); err != nil {
			return fmt.Errorf("cannot remove the unfinished clone %s: %w", e.Name(), err)
		}
	}
	return nil
}

// serviceDirs are the names of the directories in the services directory that can be services:
// the input of listener.Service for a ticket's components.
func (c *serviceCheckout) serviceDirs() ([]string, error) {
	entries, err := os.ReadDir(c.servicesDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the services directory: %w", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs, nil
}

// prepare resolves the service's directory (SPEC §5) and makes the checkout in it ready (§6): it
// exists, is a git work tree's top level, and for a ticket that names a project is that project;
// or it does not exist, the project is under an allowed group, and it is cloned. Then the
// checkout must be clean, it is switched to the default branch, and fast-forwarded to the remote's.
// A failure of the checkout is a *checkoutError; a cancelled ctx is returned as it is.
func (c *serviceCheckout) prepare(ctx context.Context, service, project string) (checkoutResult, error) {
	if !listener.ValidService(service) {
		return checkoutResult{}, fmt.Errorf("%q is not a valid service name", service)
	}
	dir := filepath.Join(c.servicesDir, service)
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := c.clone(ctx, service, project, dir); err != nil {
			return checkoutResult{}, err
		}
		res, err := c.freshBase(ctx, dir, false)
		res.Cloned = true
		return res, err
	case err != nil:
		return checkoutResult{}, fmt.Errorf("cannot look at %s: %w", dir, err)
	case !info.IsDir():
		return checkoutResult{}, checkoutFail(checkoutNotWorkTree, nil, "%s is not a directory", dir)
	}

	if err := c.checkWorkTree(ctx, dir); err != nil {
		return checkoutResult{}, err
	}
	if project != "" {
		repo := &repository.Repository{Dir: dir, Executor: c.executor}
		origin, err := repo.RemoteURL(ctx, "origin")
		if err != nil {
			return checkoutResult{}, checkoutFail(checkoutWrongProject, err, "the service checkout %s has no origin, and the ticket names the project %s", service, project)
		}
		if !listener.SameProject(origin, c.baseURL, project) {
			return checkoutResult{}, checkoutFail(checkoutWrongProject, nil, "the origin of the service checkout %s is not the project %s", service, project)
		}
	}
	return c.freshBase(ctx, dir, true)
}

// checkWorkTree requires dir to be the top level of a git work tree, not a directory inside the
// work tree of the listener's own working directory or of a parent.
func (c *serviceCheckout) checkWorkTree(ctx context.Context, dir string) error {
	repo, err := repository.Open(ctx, dir, c.executor)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return checkoutFail(checkoutNotWorkTree, err, "%s is not a git work tree", dir)
	}
	top, err := repo.TopLevel(ctx)
	if err != nil {
		return checkoutFail(checkoutNotWorkTree, err, "%s is not a git work tree", dir)
	}
	same, err := sameDir(top, dir)
	if err != nil {
		return fmt.Errorf("cannot compare %s with %s: %w", top, dir, err)
	}
	if !same {
		return checkoutFail(checkoutNotWorkTree, nil, "%s is not the top level of a git work tree", dir)
	}
	return nil
}

func sameDir(a, b string) (bool, error) {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false, err
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false, err
	}
	ra, err = filepath.Abs(ra)
	if err != nil {
		return false, err
	}
	rb, err = filepath.Abs(rb)
	if err != nil {
		return false, err
	}
	return ra == rb, nil
}

// clone clones the project into .clone-<service>-<rand> under the services directory, then renames
// it to dir: the directory a service has appears whole or not at all (SPEC §6.1).
func (c *serviceCheckout) clone(ctx context.Context, service, project, dir string) error {
	if project == "" {
		return checkoutFail(checkoutNoProject, nil, "the service %s has no checkout here, and the ticket names no project to clone: add a label gitlab:<group>/<project>", service)
	}
	if !listener.Allowed(project, c.allowedGroups) {
		return checkoutFail(checkoutNotAllowed, nil, "the project %s is not under an allowed GitLab group (%s)", project, strings.Join(c.allowedGroups, ", "))
	}
	if err := os.MkdirAll(c.servicesDir, 0o755); err != nil {
		return fmt.Errorf("cannot create the services directory: %w", err)
	}
	name := staleClonePrefix + service + "-" + c.randomSuffix()
	tmp := filepath.Join(c.servicesDir, name)

	cloneCtx, cancel := context.WithTimeout(ctx, c.cloneTimeout)
	defer cancel()
	parent := &repository.Repository{Dir: c.servicesDir, Executor: c.executor}
	if err := parent.Clone(cloneCtx, c.baseURL+"/"+project+".git", name, c.env); err != nil {
		_ = os.RemoveAll(tmp)
		return c.remoteFailure(ctx, err, checkoutCloneFailed, "cloning the project %s failed", project)
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("cannot move the clone of %s into place: %w", project, err)
	}
	return nil
}

// remoteFailure classifies the failure of a git call that talked to the remote: the listener
// being stopped, the remote refusing the credential, or the network and the remote otherwise.
func (c *serviceCheckout) remoteFailure(ctx context.Context, err error, kind checkoutKind, format string, args ...any) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, repository.ErrAuthRefused) {
		return checkoutFail(checkoutAuth, err, format+": the remote refused the credential", args...)
	}
	return checkoutFail(kind, err, format, args...)
}

// freshBase is SPEC §6.3 on dir: it must be clean, is switched to the default branch, and (with
// fetch) is brought up to origin's. The git fingerprint is taken before git runs anything the repository's
// config can name, and must be the same after.
func (c *serviceCheckout) freshBase(ctx context.Context, dir string, fetch bool) (checkoutResult, error) {
	repo := &repository.Repository{Dir: dir, Executor: c.executor, Env: c.env}
	before, err := repo.GitFingerprint(ctx)
	if err != nil {
		return checkoutResult{}, err
	}
	def, err := repo.DefaultBranch(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return checkoutResult{}, ctx.Err()
		}
		return checkoutResult{}, checkoutFail(checkoutNoDefault, err, "the service checkout %s does not know the default branch of origin", filepath.Base(dir))
	}
	branch, err := repo.Branch(ctx)
	if err != nil {
		return checkoutResult{}, err
	}
	const notClean = "the service checkout %s is not on a clean %s"
	dirty, err := repo.IsDirty(ctx)
	if err != nil {
		return checkoutResult{}, err
	}
	if dirty {
		return checkoutResult{}, checkoutFail(checkoutDirty, nil, notClean+": it has uncommitted changes", filepath.Base(dir), def)
	}
	// A clean checkout off the default branch is switched to it: a branch keeps its commits, a detached HEAD the reflog.
	if branch != def {
		if err := repo.Switch(ctx, def); err != nil {
			if ctx.Err() != nil {
				return checkoutResult{}, ctx.Err()
			}
			on := "it is on " + branch
			if branch == "" {
				on = "HEAD is detached"
			}
			return checkoutResult{}, checkoutFail(checkoutOffDefault, err, notClean+": %s, and switching to %s failed", filepath.Base(dir), def, on, def)
		}
	}
	if fetch {
		fetchCtx, cancel := context.WithTimeout(ctx, c.cloneTimeout)
		err := repo.FetchFastForward(fetchCtx, def)
		cancel()
		switch {
		case err == nil:
		case errors.Is(err, repository.ErrDiverged):
			return checkoutResult{}, checkoutFail(checkoutDiverged, err, notClean+": it has diverged from origin/%s", filepath.Base(dir), def, def)
		default:
			return checkoutResult{}, c.remoteFailure(ctx, err, checkoutFetchFailed, "fetching origin of the service checkout %s failed", filepath.Base(dir))
		}
	}
	after, err := repo.GitFingerprint(ctx)
	if err != nil {
		return checkoutResult{}, err
	}
	if after != before {
		return checkoutResult{}, checkoutFail(checkoutTampered, nil, "the git config or hooks of the service checkout %s changed while git ran in it", filepath.Base(dir))
	}
	return checkoutResult{Dir: dir, Default: def}, nil
}

func (c *serviceCheckout) randomSuffix() string {
	if c.suffix != nil {
		return c.suffix()
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
