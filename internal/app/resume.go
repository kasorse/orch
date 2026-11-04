package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/interaction"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/workflow"
)

// resumeRun continues a persisted run where its history left off (specs/resume/SPEC.md
// §6.1). It never decides again: no translator, decision provider or project-context
// manager exists here. Everything read back from the run directory is validated first,
// and on a run that has executed a step, the protected state is compared with the run's
// start - git fingerprint first - before anything runs git status. A worktree run resumes in
// its worktree, verified before any git command runs there (specs/worktree/SPEC.md §11). Until
// the operator approves, the persisted run is left exactly as it was found.
//
// dir is the repository the run lives in, as for runTask (specs/embed/SPEC.md §2.1): the runs
// directory and the repository come from it, never from the process's working directory.
func resumeRun(ctx context.Context, dir, configPath, runArg string, mode domain.RunMode, claudeAuthFlag, codexAuthFlag string, verbose bool, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := newReporter(stdout, verbose)
	defer reporter.Finish()
	stderr = reporter.Wrap(stderr)

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	// The current config applies to everything that runs from now on (SPEC §10), so it
	// goes through the same fail-fast chain as for run.
	cfg, clis, org, auth, codexAuth, err := loadAndValidateConfig(configPath, claudeAuthFlag, codexAuthFlag, registry, osExec, reporter, noJiraDependencies)
	if err != nil {
		return err
	}
	reporter.TestCommand = cfg.Test.Command

	repo, err := repository.Open(ctx, dir, osExec)
	if err != nil {
		return err
	}

	store := persistence.NewStore(runsDirOf(dir))
	runID, err := store.ResolveRunID(runArg)
	if err != nil {
		return err
	}
	unlock, err := store.Lock(runID)
	if err != nil {
		return err
	}
	defer unlock()
	run, err := store.LoadRun(runID)
	if err != nil {
		return err
	}
	if err := verifyWorkflow(run); err != nil {
		return err
	}
	recordResultRun(ctx, store, run)

	var stream io.Writer
	if verbose {
		stream = reporter.StreamWriter()
	}
	prompter := reporter.PausingPrompter(interaction.NewTerminalPrompter(stdin, stdout))
	s := newSession(ctx, &cfg, configPath, registry, osExec, repo, store, run, reporter, prompter, stream, stdout)
	// The review screen's buttons approve a resume too; what to run is fixed by then.
	s.stdin = stdin

	if run.Status == domain.RunCompleted {
		return reportCompleted(s)
	}
	// A run that has its worktree continues there, once the worktree is shown to be the one the
	// run created (specs/worktree/SPEC.md §11); from here on s.repo is the worktree, and the
	// operator's tree, dirty or on another branch, plays no part. A worktree run gets its
	// worktree before its first step, so one that executed a step without it is not continued
	// in the operator's tree.
	if run.Worktree != nil {
		if err := resumeInWorktree(ctx, s); err != nil {
			return err
		}
	} else if len(run.Executions) > 0 && worktreeRun(run) {
		return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID,
			Msg: "run.json: options.isolation is worktree and the workflow implements, but no worktree is recorded for a run that executed a step"}
	}
	if err := checkBaselineRecorded(run); err != nil {
		return err
	}

	resumeRecord := &domain.Resume{FromStatus: run.Status, FromReason: run.FailureReason, ProviderCLI: clis, OrgConfig: orgRef(org), ClaudeAuth: &auth}
	if codexAuth.Mode != "" {
		resumeRecord.CodexAuth = &codexAuth
	}
	opts := tailOptions{
		mode:        mode,
		defaultMode: resumeDefaultMode(run.Mode),
		resume:      resumeRecord,
		// execution.timeout is a fresh budget for this process's steps (SPEC §8); there
		// is no decision phase to discount.
		engineContext: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, cfg.Execution.Timeout)
		},
	}

	// What the working tree holds, shown before approval; read only once the protected
	// state matched the run's start, since git status runs a planted core.fsmonitor.
	var treeSummary string
	var treeErr error
	if len(run.Executions) == 0 {
		// No step was recorded: this is the tail of a fresh run (plan mode, cancelled at
		// approval, refused for a dirty tree, or crashed before the first step).
		if s.worktree == nil && run.ReviewTarget != nil {
			// A review run gets its detached worktree after approval (enterWorktree); the
			// operator's tree, dirty or not, plays no part.
		} else if s.worktree == nil {
			dirty, err := repo.IsDirty(ctx)
			if err != nil {
				return err
			}
			// The clean-start rule follows the isolation the run recorded, never the current
			// config (specs/worktree/SPEC.md §11), as executeWorkflow does: a worktree run
			// without its worktree yet creates it after approval (enterWorktree), and the
			// operator's dirty tree only gets the warning.
			modeCfg := cfg
			modeCfg.Execution.Isolation = run.Options.IsolationMode()
			if err := checkWorktreeFor(ctx, repo, modeCfg, *run.Workflow, dirty, run.Task.Source, stderr); err != nil {
				return err
			}
			opts.dirty = dirty
		} else if len(run.ResumeBaseline) > 0 {
			// The worktree exists and starts from its base_ref. A baseline means the process
			// got as far as the first step, which may have run before it died: the protected
			// state is compared before anything else runs there, as for a run with executions.
			if err := checkProtectedState(ctx, s.repo, run); err != nil {
				return err
			}
		}
		opts.resume.Entry, opts.resume.Why = run.Workflow.Steps[0].Action, "start"
	} else {
		entry, why, err := workflow.Entry(run, limitsView(cfg))
		if err != nil {
			return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID, Msg: "cannot derive where to continue", Err: err}
		}
		if entry == workflow.StateDone {
			// The process died between persisting the last step and persisting the run's
			// completion; nothing is left to run.
			run.Status = domain.RunCompleted
			if err := writeOutcome(store, run); err != nil {
				return err
			}
			if err := reportCompleted(s); err != nil {
				return err
			}
			// No commit is made here: the result stays in the worktree, which is kept.
			if s.worktree != nil {
				reporter.WorktreeKept(s.worktree.Path, "")
			}
			return nil
		}
		if err := checkProtectedState(ctx, s.repo, run); err != nil {
			return err
		}
		if err := workflow.CheckLimits(run, limitsView(cfg), entry); err != nil {
			return err
		}
		if err := s.dispatcher.Restore(run, store); err != nil {
			return err
		}
		opts.resume.Entry, opts.resume.Why = domain.ActionType(entry), why
		treeSummary, treeErr = s.repo.StatusSummary(ctx)
	}

	reporter.ResumeHeader(run, store.RunDir(run.ID))
	reporter.History(run)
	if w := s.worktree; w != nil {
		if run.ReviewTarget != nil {
			reporter.ReviewWorktree(w.Path, w.BaseRef)
		} else {
			reporter.Worktree(w.Path, w.Branch, w.BaseBranch, w.BaseRef)
		}
	}
	reporter.WorkingTree(treeSummary, treeErr)
	remaining := remainingSteps(*run.Workflow, opts.resume.Entry)
	reporter.NextStep(opts.resume.Entry, opts.resume.Why, remaining)
	if w := orgChangeWarning(run.OrgConfig, orgRef(org)); w != "" {
		logging.GapWarning(stderr, w)
	}
	if w := claudeAuthChangeNotice(run, auth); w != "" {
		logging.GapWarning(stderr, w)
	}
	if codexAuth.Mode != "" {
		if w := codexAuthChangeNotice(run, codexAuth); w != "" {
			logging.GapWarning(stderr, w)
		}
	}
	if pathRulesChanged(run.Options.PathRules, cfg.PathRules()) {
		logging.GapWarning(stderr, pathRulesChangedWarning)
	}
	// Advisory only: a decision.json that is missing or unreadable costs the warning, not
	// the resume.
	if trace, _, err := store.LoadDecision(run.ID); err == nil {
		for _, w := range modelChangeWarnings(cfg, run.Decision, trace.Models, remaining) {
			logging.GapWarning(stderr, w)
		}
	}
	// The overview above is read in full width; the sidebar opens for the steps.
	if reporter.Pretty {
		reporter.SetRepoInfo(repoInfo(ctx, s.repo))
		reporter.OpenDock()
		reporter.SetRun(run)
	}
	return executeWorkflow(ctx, s, opts)
}

// claudeAuthChangeNotice is the notice for a resume whose credential mode differs from the one
// the run last ran under (specs/claude-auth/SPEC.md §11), and "" when it is the same or the run
// recorded none. The record is only compared for the notice, never used to decide anything.
func claudeAuthChangeNotice(run *domain.Run, now domain.ClaudeAuth) string {
	last := run.ClaudeAuth
	for _, r := range run.Resumes {
		if r.ClaudeAuth != nil {
			last = r.ClaudeAuth
		}
	}
	if last == nil || last.Mode == now.Mode {
		return ""
	}
	return fmt.Sprintf("claude auth changed since the run started: %s → %s", last.Mode, now.Mode)
}

func codexAuthChangeNotice(run *domain.Run, now domain.CodexAuth) string {
	last := run.CodexAuth
	for _, r := range run.Resumes {
		if r.CodexAuth != nil {
			last = r.CodexAuth
		}
	}
	if last == nil || last.Mode == now.Mode {
		return ""
	}
	return fmt.Sprintf("codex auth changed since the run started: %s → %s", last.Mode, now.Mode)
}

// verifyWorkflow refuses a run without a workflow (nothing to continue; that is a new run)
// and a run whose workflow is not the one its decision builds (run.json was edited).
func verifyWorkflow(run *domain.Run) error {
	// A task that stayed unclear stopped before its workflow (specs/clarify, Approach §4): what is
	// missing is in the task, which only a new run can change.
	if run.FailureReason == domain.FailureTaskNeedsInput && run.Workflow == nil {
		return &apperr.RunStateError{Kind: apperr.RunNotResumable, RunID: run.ID,
			Msg: "nothing to resume: the run stopped before its workflow (task needs input); start a new run with the missing details in the task"}
	}
	if run.Decision == nil || run.Workflow == nil {
		return &apperr.RunStateError{Kind: apperr.RunNotResumable, RunID: run.ID, Msg: "no workflow was decided for this run; start a new run"}
	}
	if err := workflow.Verify(run); err != nil {
		return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID, Msg: "run.json", Err: err}
	}
	return nil
}

// checkBaselineRecorded checks that a run which executed a step has a well-formed run-start
// baseline (SPEC §6.3): without one, it predates resume support and nothing can show that
// its tree is safe to continue on.
func checkBaselineRecorded(run *domain.Run) error {
	if len(run.Executions) > 0 && len(run.ResumeBaseline) == 0 {
		return &apperr.RunStateError{Kind: apperr.RunNotResumable, RunID: run.ID,
			Msg: "the run has no resume baseline: it was started by an orchestrator without resume support; start a new run"}
	}
	if len(run.ResumeBaseline) > 0 {
		var baseline action.GuardState
		if err := json.Unmarshal(run.ResumeBaseline, &baseline); err != nil {
			return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID, Msg: "run.json: resume_baseline", Err: err}
		}
	}
	return nil
}

// checkProtectedState compares the repository with the run's start before anything runs
// on a tree a model last touched (SPEC §9.1-§9.2): the git fingerprint, then the protected
// paths, then HEAD. A step that failed at the guard, or a crash mid-implement or mid-test,
// left its changes on disk; only a tree back at the run-start state may continue. Only
// then is the restore point checked to be a commit (SPEC §6.3): until the fingerprint
// matched, nothing but rev-parse runs in the repository.
func checkProtectedState(ctx context.Context, repo *repository.Repository, run *domain.Run) error {
	changed, err := action.CheckBaseline(ctx, repo, run.ResumeBaseline)
	if err != nil {
		return err
	}
	if len(changed) > 0 {
		return action.BaselineViolation(changed, run.PreRunRef)
	}

	// Review diffs against HEAD, so on another commit it would review the wrong changes.
	head, err := repo.HeadRef(ctx)
	if err != nil {
		head = ""
	}
	if head != run.HeadRef {
		return &apperr.WorkflowError{Msg: fmt.Sprintf("HEAD moved since the run started: it was %s and is now %s.\n\n"+
			"Review compares the working tree with HEAD, so the run cannot continue on another commit.\n"+
			"Move HEAD back to %s (git reset --soft keeps the changes) to resume it, or start a new run.",
			orNone(run.HeadRef), orNone(head), orNone(run.HeadRef))}
	}

	if run.PreRunRef != "" {
		ok, err := repo.CommitExists(ctx, run.PreRunRef)
		if err != nil {
			return err
		}
		if !ok {
			return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID,
				Msg: fmt.Sprintf("run.json: pre_run_ref %q is not a commit in this repository", run.PreRunRef)}
		}
	}
	return nil
}

// reportCompleted answers a resume of a finished run: nothing runs and nothing is called,
// but a read-only workflow's answer is printed again from its artifacts.
func reportCompleted(s *session) error {
	if err := s.dispatcher.Restore(s.run, s.store); err != nil {
		return err
	}
	s.reporter.AlreadyCompleted(s.run.ID)
	if answer := s.dispatcher.FinalAnswer(s.run); answer != "" {
		s.reporter.Answer(answer)
	}
	return nil
}

// resumeDefaultMode is the mode an empty answer picks on resume: the one the run had, but
// never plan, which would only preview it again (SPEC §10). A persisted auto is offered,
// not applied: an unattended resume needs --mode auto.
func resumeDefaultMode(persisted domain.RunMode) domain.RunMode {
	if persisted == domain.ModePlan {
		return ""
	}
	return persisted
}

// remainingSteps is the workflow from entry on, as it would run forward.
func remainingSteps(wf domain.Workflow, entry domain.ActionType) []domain.ActionType {
	var out []domain.ActionType
	for i, step := range wf.Steps {
		if step.Action != entry {
			continue
		}
		for _, s := range wf.Steps[i:] {
			out = append(out, s.Action)
		}
		break
	}
	return out
}

func orNone(ref string) string {
	if ref == "" {
		return "(none)"
	}
	return ref
}
