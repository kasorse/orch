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
// (specs/worktree/SPEC.md §6), or it is a review run, which always has one
// (specs/delivery/SPEC.md §12.2). It is the rule config.WorktreeRun applies before approval, read
// from run.json: options.isolation already folds in run --inplace.
func worktreeRun(run *domain.Run) bool {
	return run.Options.IsolationMode() == domain.IsolationWorktree && run.Workflow != nil &&
		(run.ReviewTarget != nil || run.Workflow.Contains(domain.ActionImplement))
}

// enterWorktree applies the isolation rule to a run that has not executed a step, once the
// operator approved its workflow (specs/worktree/SPEC.md §6, §7). A workflow without
// implement runs in place whatever options.isolation said (O3), and the record is rewritten
// to say so. A worktree run gets its worktree, and the session switches to it. A resumed run
// whose worktree was created before its first step is already in it (resumeInWorktree): it is
// never created twice.
func enterWorktree(ctx context.Context, s *session) error {
	if s.worktree != nil {
		return nil
	}
	run := s.run
	if !worktreeRun(run) {
		if run.Options.Isolation == domain.IsolationWorktree {
			run.Options.Isolation = domain.IsolationInplace
			return s.store.SaveRun(run)
		}
		return nil
	}
	if err := checkDeliverable(ctx, s.repo, *s.cfg, run.Task.Source); err != nil {
		return err
	}
	wt, err := openWorktree(ctx, s)
	if err != nil {
		return err
	}
	switchToWorktree(s, wt)
	w := s.worktree
	if run.ReviewTarget != nil {
		s.reporter.ReviewWorktree(w.Path, w.BaseRef)
		return nil
	}
	s.reporter.Worktree(w.Path, w.Branch, w.BaseBranch, w.BaseRef)
	return nil
}

// openWorktree creates the run's worktree from the operator's repository s.repo
// (specs/worktree/SPEC.md §7, §8): on the branch nameBranch chose (the repository's instructions,
// worktree.branch.template, else orchestrator/<run-id>), at the derived path, from the commit HEAD
// names. A story task (specs/story/SPEC.md §11) is created on the branch
// and from the commit its task input gave, which run.json records in task.source, with the
// input's base branch for the conflict check; the operator's HEAD and branch play no part.
// run.json: worktree is saved as soon as git created it, so a failure after that never leaves a
// worktree the run cannot find. It returns the worktree's Repository; nothing has switched to
// it yet.
func openWorktree(ctx context.Context, s *session) (*repository.Repository, error) {
	repo, run := s.repo, s.run
	if run.ReviewTarget != nil {
		return openReviewWorktree(ctx, s)
	}
	var base, baseBranch string
	if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceStory {
		// The commit may have gone since the task input was checked, or the run be resumed
		// before it had a worktree. CommitExists refuses anything but a full SHA.
		base, baseBranch = src.BaseRef, src.BaseBranch
		if ok, err := repo.CommitExists(ctx, base); err != nil {
			return nil, err
		} else if !ok {
			return nil, &apperr.ConfigError{Msg: fmt.Sprintf("the story task's base_ref %s is not a commit in this repository", base)}
		}
	} else {
		var err error
		if base, err = repo.HeadRef(ctx); err != nil {
			return nil, &apperr.ConfigError{Msg: "a worktree run starts from the commit HEAD names, and HEAD names none (a repository without commits?)\n\n" +
				"Make a first commit, or " + inplaceHint, Err: err}
		}
		if baseBranch, err = repo.Branch(ctx); err != nil {
			return nil, err
		}
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
	// The branch is named now, once, and never changes (specs/delivery/SPEC.md §4.3); the record is
	// saved with the worktree's. A story task is named as any run is, with this repository's rules,
	// from its Jira child issue and its title, and its task input's branch is the fallback (§9.1).
	in := branchInput{
		Key: runJiraKey(run), Task: action.BranchTaskLine(run.Task.Description), Fallback: domain.WorktreeBranch(run.ID),
		BaseRef: base, BaseBranch: baseBranch,
	}
	if t := s.cfg.Delivery.TargetBranch; t != "" {
		in.Reserved = []string{t}
	}
	if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceStory {
		in.Key, in.Fallback = src.Jira, src.Branch
		if src.Title != "" {
			in.Task = src.Title
		}
	}
	branch, naming, err := nameBranch(ctx, s, in)
	if err != nil {
		return nil, err
	}
	run.BranchNaming = naming

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

// openReviewWorktree creates the worktree of a review run (specs/delivery/SPEC.md §12.2): at the
// derived path, detached at review_target.head, with no branch. It is checked and recorded like
// openWorktree's, and the operator's tree, index and branches play no part.
func openReviewWorktree(ctx context.Context, s *session) (*repository.Repository, error) {
	repo, run := s.repo, s.run
	head := run.ReviewTarget.Head
	// The commit may have gone since the refs were resolved, or the run be resumed before it
	// had a worktree. CommitExists refuses anything but a full SHA.
	if ok, err := repo.CommitExists(ctx, head); err != nil {
		return nil, err
	} else if !ok {
		return nil, &apperr.ConfigError{Msg: fmt.Sprintf("the commit to review, %s, is not in this repository", head)}
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
	if _, err := os.Lstat(path); err == nil {
		return nil, &apperr.WorkflowError{Msg: fmt.Sprintf("the run's worktree path %s already exists; it is not reused. Remove it, or start a new run.", path)}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, &apperr.WorkflowError{Msg: "cannot check the run's worktree path", Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, &apperr.WorkflowError{Msg: "cannot create the worktrees directory", Err: err}
	}

	before, err := repo.GitFingerprint(ctx)
	if err != nil {
		return nil, err
	}
	admin, err := repo.AddDetachedWorktree(ctx, path, head)
	if err != nil {
		return nil, &apperr.WorkflowError{Msg: fmt.Sprintf("failed to create the run's worktree %s at %s", path, head), Err: err}
	}
	pointer, pointerErr := repository.ReadGitPointer(path)
	if pointerErr == nil && pointer != admin && sameResolvedPath(pointer, admin) {
		admin = pointer
	}
	run.Worktree = &domain.RunWorktree{Path: path, BaseRef: head, AdminDir: admin}
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
	s.operatorFingerprint = after
	return &repository.Repository{Dir: path, Executor: repo.Executor}, nil
}

// endReviewWorktree removes the worktree of a review run that ended, completed or failed: nothing
// in it is a result (specs/delivery/SPEC.md §12.2). A stopped run keeps it, for resume to
// continue in. A removal git refuses keeps the worktree and prints its path.
func endReviewWorktree(ctx context.Context, s *session) {
	run, w := s.run, s.worktree
	if run.ReviewTarget == nil || w == nil || w.Removed || s.operator == nil {
		return
	}
	if run.Status != domain.RunCompleted && run.Status != domain.RunFailed {
		return
	}
	// A cancelled context must not keep git from removing what the run made.
	if err := s.operator.RemoveWorktree(context.WithoutCancel(ctx), w.Path); err != nil {
		s.reporter.WorktreeKept(w.Path, err.Error())
		return
	}
	w.Removed = true
	if err := s.store.SaveRun(run); err != nil {
		fmt.Fprintf(s.reporter.PausingWriter(), "Removed the worktree %s, but run.json could not record it: %v\n", w.Path, err)
		return
	}
	saveSummary(s.store, run)
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
	s.dispatcher.SourceDir = s.operator.Dir
	s.runsRel = ""
	s.dispatcher.RunsDir = ""
	s.worktree = s.run.Worktree
}

// resumeInWorktree moves a resumed session into the run's worktree once verifyRunWorktree has
// shown it is the one the run created (specs/worktree/SPEC.md §11), switching exactly as a
// fresh run does. The operator's git fingerprint, the conflict check's reference at the end of
// the run (finishWorktree), is taken now rather than at creation. A config or hook changed in
// between still stops a run that executed a step: the worktree's baseline, compared next,
// hashes the same shared config and hooks.
func resumeInWorktree(ctx context.Context, s *session) error {
	wt, err := verifyRunWorktree(ctx, s.repo, s.run)
	if err != nil {
		return err
	}
	fingerprint, err := s.repo.GitFingerprint(ctx)
	if err != nil {
		return err
	}
	switchToWorktree(s, wt)
	s.operatorFingerprint = fingerprint
	return nil
}

// verifyRunWorktree checks that the worktree run.json records is the one the run created,
// before any git command runs in it (specs/worktree/SPEC.md §8.3, §11). In this order: the path
// derived from repo, the operator's repository, is the recorded one; the directory is there;
// its .git file, read in Go, names the recorded admin directory, which lies directly under
// <common>/worktrees/; and git worktree list, run in repo, lists the path on the run's branch.
// Only then is the worktree's Repository returned. git spells paths symlink-resolved, so a
// resolved match counts where the literal one fails. Nothing here writes run.json.
func verifyRunWorktree(ctx context.Context, repo *repository.Repository, run *domain.Run) (*repository.Repository, error) {
	w := run.Worktree
	refuse := func(kind apperr.RunStateKind, msg string, err error) error {
		return &apperr.RunStateError{Kind: kind, RunID: run.ID, Msg: msg, Err: err}
	}
	if w.Removed {
		if run.ReviewTarget != nil {
			return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s was removed; start a new run", w.Path), nil)
		}
		return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s was removed; its branch %s stays; start a new run", w.Path, w.Branch), nil)
	}

	common, err := repo.CommonDir(ctx)
	if err != nil {
		return nil, err
	}
	state, err := persistence.StateDir()
	if err != nil {
		return nil, &apperr.ConfigError{Msg: "cannot find the run's worktree", Err: err}
	}
	want := persistence.WorktreePath(state, common, run.ID)
	if w.Path != want && !sameResolvedPath(w.Path, want) {
		return nil, refuse(apperr.RunCorrupt, fmt.Sprintf("run.json: worktree.path is %s, but this repository's worktree for the run is %s "+
			"(resumed from another repository, or with another $XDG_STATE_HOME?)", w.Path, want), nil)
	}
	// The branch was named when the worktree was created, so it is checked against the rules (a
	// story task's may also be the fallback its task input gave), and below against what git lists
	// on the derived path: a branch edited to another worktree's is not the one on that path. A
	// review run's worktree is detached: it has no branch.
	if run.ReviewTarget != nil {
		if w.Branch != "" {
			return nil, refuse(apperr.RunCorrupt, fmt.Sprintf("run.json: worktree.branch is %q on a review run, want none", w.Branch), nil)
		}
	} else if src := run.Task.Source; src != nil && src.Kind == domain.TaskSourceStory && w.Branch == src.Branch {
		// The story task's fallback.
	} else if err := domain.CheckBranchName(w.Branch); err != nil {
		return nil, refuse(apperr.RunCorrupt, fmt.Sprintf("run.json: worktree.branch %q: %v", w.Branch, err), nil)
	}

	info, err := os.Lstat(w.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s is gone; start a new run", w.Path), nil)
	}
	if err != nil {
		return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("cannot check the run's worktree %s", w.Path), err)
	}
	if !info.IsDir() {
		return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s is not a directory; start a new run", w.Path), nil)
	}

	pointer, err := repository.ReadGitPointer(w.Path)
	if err != nil {
		return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s: %s cannot be checked", w.Path, guard.PointerViolation), err)
	}
	if pointer != w.AdminDir {
		return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s: %s names %s, not the recorded admin directory %s",
			w.Path, guard.PointerViolation, pointer, w.AdminDir), nil)
	}
	worktrees := filepath.Join(common, "worktrees")
	if parent := filepath.Dir(w.AdminDir); parent != worktrees && !sameResolvedPath(parent, worktrees) {
		return nil, refuse(apperr.RunCorrupt, fmt.Sprintf("run.json: worktree.admin_dir %s is not directly under %s", w.AdminDir, worktrees), nil)
	}

	list, err := repo.ListWorktrees(ctx)
	if err != nil {
		return nil, err
	}
	for _, entry := range list {
		if entry.Path != w.Path && !sameResolvedPath(entry.Path, w.Path) {
			continue
		}
		if t := run.ReviewTarget; t != nil {
			// A review run's worktree is detached at the commit it reviews.
			if entry.Branch != "" {
				return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s is on %s, not a detached HEAD at %s", w.Path, entry.Branch, t.Head), nil)
			}
			if entry.Head != t.Head {
				return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s is at %s, not at %s", w.Path, entry.Head, t.Head), nil)
			}
			return &repository.Repository{Dir: w.Path, Executor: repo.Executor}, nil
		}
		if ref := "refs/heads/" + w.Branch; entry.Branch != ref {
			on := entry.Branch
			if on == "" {
				on = "a detached HEAD"
			}
			return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("the run's worktree %s is on %s, not %s", w.Path, on, ref), nil)
		}
		return &repository.Repository{Dir: w.Path, Executor: repo.Executor}, nil
	}
	return nil, refuse(apperr.RunNotResumable, fmt.Sprintf("git does not list the run's worktree %s in this repository", w.Path), nil)
}

// finishWorktree ends a completed worktree run after the commit (specs/worktree/SPEC.md
// §10, specs/delivery/SPEC.md §7): with sha, the commit on the run's branch, it says how to
// take the branch, checks it for conflicts with the operator's branch, delivers it (deliver:
// the push and the merge request) and removes the worktree, whatever the delivery did; without
// one the worktree stays, its path is printed and the chain ends there (skipDelivery). Nothing
// here fails a run that already completed: what goes wrong is reported and the worktree is
// kept. The branch is never deleted: it is the result. The error is the delivery's: a
// NeedsPersonError when the branch did not become a merge request and nobody declined that.
func finishWorktree(ctx context.Context, s *session, sha string) error {
	w := s.worktree
	if sha == "" || s.operator == nil {
		s.reporter.WorktreeKept(w.Path, "")
		return skipDelivery(s)
	}
	s.reporter.WorktreeCommitted(w.Branch, w.BaseBranch, w.BaseRef)
	// A detached HEAD has no branch to merge into.
	if w.BaseBranch != "" {
		checkConflicts(ctx, s, sha)
	}
	deliveryErr := deliver(ctx, s, sha)
	// Without --force: git refuses when anything is left in the worktree, which then stays.
	if err := s.operator.RemoveWorktree(ctx, w.Path); err != nil {
		s.reporter.WorktreeKept(w.Path, err.Error())
		return deliveryErr
	}
	w.Removed = true
	if err := s.store.SaveRun(s.run); err != nil {
		fmt.Fprintf(s.reporter.PausingWriter(), "Removed the worktree %s, but run.json could not record it: %v\n", w.Path, err)
	}
	return deliveryErr
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
	// A story task's base branch need not exist in this repository.
	if exists, err := s.operator.BranchExists(ctx, w.BaseBranch); err == nil && !exists {
		s.reporter.ConflictCheckSkipped(w.BaseBranch, "the branch does not exist in this repository")
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
// tree a model has touched: only a run without executions and without a run-start baseline (a
// process killed in its first step recorded no execution, but the baseline) whose setup has not
// completed. The
// guard compares the git pointer, fingerprint and protected paths around it, so setup that
// reaches outside the worktree (an unsandboxed one) fails the run. ctx is the engine's share of
// execution.timeout: a setup that outlives it is killed and fails the run.
func runSetup(ctx context.Context, s *session) error {
	run, cfg := s.run, s.cfg
	setup := strings.TrimSpace(cfg.Worktree.Setup)
	if s.worktree == nil || setup == "" || len(run.Executions) > 0 || len(run.ResumeBaseline) > 0 || run.Worktree.SetupDone {
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
