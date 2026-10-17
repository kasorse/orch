package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/guard"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
)

// worktreeRun reports whether a run that has not executed a step runs in its own git
// worktree: its recorded isolation is worktree and the approved workflow contains implement
// (specs/worktree/SPEC.md §6). It is the rule config.WorktreeRun applies before approval, read
// from run.json: options.isolation already folds in run --inplace.
func worktreeRun(run *domain.Run) bool {
	return run.Options.IsolationMode() == domain.IsolationWorktree && run.Workflow != nil &&
		run.Workflow.Contains(domain.ActionImplement)
}

// enterWorktree applies the isolation rule to a run that has not executed a step, once the
// operator approved its workflow (specs/worktree/SPEC.md §6, §7). A workflow without
// implement runs in place whatever options.isolation said (O3), and the record is rewritten
// to say so. A worktree run gets its worktree, and the session switches to it.
func enterWorktree(ctx context.Context, s *session) error {
	run := s.run
	if !worktreeRun(run) {
		if run.Options.Isolation == domain.IsolationWorktree {
			run.Options.Isolation = domain.IsolationInplace
			return s.store.SaveRun(run)
		}
		return nil
	}
	wt, err := openWorktree(ctx, s)
	if err != nil {
		return err
	}
	switchToWorktree(s, wt)
	w := s.worktree
	s.reporter.Worktree(w.Path, w.Branch, w.BaseBranch, w.BaseRef)
	return nil
}

// openWorktree creates the run's worktree from the operator's repository s.repo
// (specs/worktree/SPEC.md §7, §8): on the branch orchestrator/<run-id>, at the derived path,
// from the commit HEAD names. run.json: worktree is saved as soon as git created it, so a
// failure after that never leaves a worktree the run cannot find. It returns the worktree's
// Repository; nothing has switched to it yet.
func openWorktree(ctx context.Context, s *session) (*repository.Repository, error) {
	repo, run := s.repo, s.run
	base, err := repo.HeadRef(ctx)
	if err != nil {
		return nil, &apperr.ConfigError{Msg: "a worktree run starts from the commit HEAD names, and HEAD names none (a repository without commits?)\n\n" +
			"Make a first commit, or run in place: set execution.isolation: inplace, or run with --inplace.", Err: err}
	}
	baseBranch, err := repo.Branch(ctx)
	if err != nil {
		return nil, err
	}
	common, err := repo.CommonDir(ctx)
	if err != nil {
		return nil, err
	}
	state, err := persistence.StateDir()
	if err != nil {
		return nil, &apperr.ConfigError{Msg: "cannot place the run's worktree", Err: err}
	}
	path := persistence.WorktreePath(state, common, run.ID)
	branch := domain.WorktreeBranch(run.ID)

	// Neither is ever reused: an existing one belongs to something else.
	if exists, err := repo.BranchExists(ctx, branch); err != nil {
		return nil, err
	} else if exists {
		return nil, branchExistsError(branch)
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, &apperr.WorkflowError{Msg: fmt.Sprintf("the run's worktree path %s already exists; it is not reused. Remove it, or start a new run.", path)}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, &apperr.WorkflowError{Msg: "cannot check the run's worktree path", Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, &apperr.WorkflowError{Msg: "cannot create the worktrees directory", Err: err}
	}

	// git worktree add runs the operator's post-checkout hook and filters; they are the
	// operator's own, but must leave the git config and hooks as they were.
	before, err := repo.GitFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	admin, err := repo.AddWorktree(ctx, path, branch, base)
	if err != nil {
		// git refuses an existing branch too, when one appeared after the check above.
		if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
			if exists, _ := repo.BranchExists(ctx, branch); exists {
				return nil, branchExistsError(branch)
			}
		}
		return nil, &apperr.WorkflowError{Msg: fmt.Sprintf("failed to create the run's worktree %s on %s", path, branch), Err: err}
	}
	// git wrote the .git file just now, before any step: it may spell the admin directory
	// through other symlinks than rev-parse does. The guard compares the exact text.
	pointer, pointerErr := repository.ReadGitPointer(path)
	if pointerErr == nil && pointer != admin && sameResolvedPath(pointer, admin) {
		admin = pointer
	}
	run.Worktree = &domain.RunWorktree{Path: path, Branch: branch, BaseRef: base, BaseBranch: baseBranch, AdminDir: admin}
	if err := s.store.SaveRun(run); err != nil {
		return nil, err
	}
	if pointerErr != nil || pointer != admin {
		return nil, &apperr.WorkflowError{Msg: fmt.Sprintf("the new worktree's .git file does not name its admin directory %s "+
			"(worktree.useRelativePaths?); the run stops before any step. The worktree is at %s", admin, path), Err: pointerErr}
	}
	after, err := repo.GitFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	if after != before {
		return nil, &apperr.WorkflowError{Msg: fmt.Sprintf("creating the run's worktree changed %s in your repository "+
			"(a post-checkout hook or a filter?); the run stops before any step. The worktree is at %s", guard.GitViolation, path)}
	}
	return &repository.Repository{Dir: path, Executor: repo.Executor}, nil
}

func branchExistsError(branch string) error {
	return &apperr.WorkflowError{Msg: fmt.Sprintf("the branch %s already exists; it is not reused. Delete it (git branch -D %s), or start a new run.", branch, branch)}
}

// switchToWorktree moves everything that runs a step or checks one to the worktree
// (specs/worktree/SPEC.md §7): the provider's working directory, the guards, the test
// sandbox, the verify server, the secret scan and the review diff all read s.repo or
// s.dispatcher.Repo. The store, the run directory and its lock stay in the operator's
// repository; the runs directory is outside the worktree, so nothing in it is denied or
// excluded there. s.protected is kept as it is: the config file's path relative to the
// operator's repository is the one its copy in the worktree has.
func switchToWorktree(s *session, wt *repository.Repository) {
	s.repo = wt
	s.dispatcher.Repo = wt
	s.runsRel = ""
	s.dispatcher.RunsDir = ""
	s.worktree = s.run.Worktree
}

// gitPointer is the admin directory the worktree's .git file must name, "" in place.
func (s *session) gitPointer() string {
	if s.worktree == nil {
		return ""
	}
	return s.worktree.AdminDir
}

// readOnlyFiles are the files the test sandbox binds read-only over the repository: the
// worktree's .git file, none in place. Like the repository root git reports, the path is
// resolved: the sandbox binds resolved paths.
func (s *session) readOnlyFiles() []string {
	if s.worktree == nil {
		return nil
	}
	dir := s.worktree.Path
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return []string{filepath.Join(dir, ".git")}
}

// newRepoGuard is the guard over s.repo with the protected patterns given, and in a worktree
// its .git pointer.
func (s *session) newRepoGuard(patterns []string) *guard.RepoGuard {
	g := guard.NewRepoGuard(s.repo, patterns)
	g.GitPointer = s.gitPointer()
	return g
}

func sameResolvedPath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
