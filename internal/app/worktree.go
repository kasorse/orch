package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/guard"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
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
	// The conflict check at the end of the run compares against it (finishWorktree).
	s.operatorFingerprint = after
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
// excluded there; s.operator keeps that repository for the end of the run. s.protected is
// kept as it is: the config file's path relative to the operator's repository is the one its
// copy in the worktree has.
func switchToWorktree(s *session, wt *repository.Repository) {
	s.operator = s.repo
	s.repo = wt
	s.dispatcher.Repo = wt
	s.runsRel = ""
	s.dispatcher.RunsDir = ""
	s.worktree = s.run.Worktree
}

// finishWorktree ends a completed worktree run after the commit (specs/worktree/SPEC.md
// §10): with sha, the commit on the run's branch, it says how to take the branch, checks it
// for conflicts with the operator's branch and removes the worktree; without one the worktree
// stays and its path is printed. Nothing here fails a run that already completed: what goes
// wrong is reported and the worktree is kept. The branch is never deleted: it is the result.
func finishWorktree(ctx context.Context, s *session, sha string) {
	w := s.worktree
	if sha == "" || s.operator == nil {
		s.reporter.WorktreeKept(w.Path, "")
		return
	}
	s.reporter.WorktreeCommitted(w.Branch, w.BaseBranch, w.BaseRef)
	// A detached HEAD has no branch to merge into.
	if w.BaseBranch != "" {
		checkConflicts(ctx, s, sha)
	}
	// Without --force: git refuses when anything is left in the worktree, which then stays.
	if err := s.operator.RemoveWorktree(ctx, w.Path); err != nil {
		s.reporter.WorktreeKept(w.Path, err.Error())
		return
	}
	w.Removed = true
	if err := s.store.SaveRun(s.run); err != nil {
		fmt.Fprintf(s.reporter.PausingWriter(), "Removed the worktree %s, but run.json could not record it: %v\n", w.Path, err)
	}
}

// checkConflicts asks git, in the operator's repository, whether merging the run's commit
// sha into its base branch conflicts (specs/worktree/SPEC.md §10.3), and records the files in
// worktree.conflicts. merge-tree runs a merge driver the git config names, so the config and
// hooks must be the ones the worktree was created with; otherwise the check is skipped.
func checkConflicts(ctx context.Context, s *session, sha string) {
	w := s.worktree
	fingerprint, err := s.operator.GitFingerprint(ctx)
	if err != nil {
		s.reporter.ConflictCheckSkipped(w.BaseBranch, "cannot read the git config and hooks: "+err.Error())
		return
	}
	if fingerprint != s.operatorFingerprint {
		s.reporter.ConflictCheckSkipped(w.BaseBranch, guard.GitViolation+" changed since the worktree was created")
		return
	}
	files, supported, err := s.operator.MergeConflicts(ctx, w.BaseBranch, sha)
	switch {
	case err != nil:
		s.reporter.ConflictCheckSkipped(w.BaseBranch, err.Error())
		return
	case !supported:
		s.reporter.MergeTreeUnsupported(w.BaseBranch)
		return
	case len(files) == 0:
		return
	}
	w.Conflicts = files
	s.reporter.WorktreeConflicts(w.Branch, w.BaseBranch, files)
	if err := s.store.SaveRun(s.run); err != nil {
		fmt.Fprintf(s.reporter.PausingWriter(), "run.json could not record the conflicts: %v\n", err)
	}
}

// runSetup runs worktree.setup once in a new worktree, with the network, before the restore
// point, the baseline and the pre-run test (specs/worktree/SPEC.md §9). It never runs on a
// tree a model has touched: only a run without executions whose setup has not completed. The
// guard compares the git pointer, fingerprint and protected paths around it, so setup that
// reaches outside the worktree (an unsandboxed one) fails the run. ctx is the engine's share of
// execution.timeout: a setup that outlives it is killed and fails the run.
func runSetup(ctx context.Context, s *session) error {
	run, cfg := s.run, s.cfg
	setup := strings.TrimSpace(cfg.Worktree.Setup)
	if s.worktree == nil || setup == "" || len(run.Executions) > 0 || run.Worktree.SetupDone {
		return nil
	}
	g := s.newRepoGuard(s.protected)
	before, err := g.Snapshot(ctx)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to record the protected state before worktree.setup", Err: err}
	}

	// A runner of its own: s.runner is the test steps', and must not get the network.
	runner := &testexec.Runner{Executor: s.runner.Executor}
	if cfg.Test.Sandboxed() {
		sandbox, err := testSandbox(ctx, s.repo, cfg.Test, s.readOnlyFiles())
		if err != nil {
			return err
		}
		sandbox.Network = true
		runner.Sandbox = sandbox
	}
	s.reporter.SetupStarted()
	result, runErr := runner.Run(ctx, setup, s.repo.Dir, testexec.RunOptions{})
	if runErr != nil {
		// The runner discards the output of a run it had to kill or could not start, so the
		// log says why there is none.
		result = domain.TestResult{Stderr: fmt.Sprintf("worktree.setup did not finish: %v\n", runErr)}
	}
	_ = s.store.AppendCallLog(run.ID, "setup", "stdout", result.Stdout)
	_ = s.store.AppendCallLog(run.ID, "setup", "stderr", result.Stderr)
	logPath := s.store.CallLogPath(run.ID, "setup", "stderr")

	// Checked whatever setup's end: a killed one may have changed the git config already. The
	// deadline that killed it must not stop the check.
	changed, err := g.Changed(context.WithoutCancel(ctx), before)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to check protected paths after worktree.setup", Err: err}
	}
	if len(changed) > 0 {
		return action.SetupViolation(changed, run.Worktree.BaseRef)
	}
	switch {
	case errors.Is(runErr, context.DeadlineExceeded):
		return &apperr.WorkflowError{Msg: fmt.Sprintf("worktree setup failed (timed out: execution.timeout ran out); "+
			"it and its child processes were killed; output in %s", logPath), Err: runErr}
	case runErr != nil:
		return &apperr.WorkflowError{Msg: fmt.Sprintf("worktree setup failed; output in %s", logPath), Err: runErr}
	case !result.Success:
		return &apperr.WorkflowError{Msg: fmt.Sprintf("worktree setup failed (exit %d); output in %s", result.ExitCode, logPath)}
	}
	run.Worktree.SetupDone = true
	return s.store.SaveRun(run)
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
