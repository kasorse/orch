package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/interaction"
	"orchestrator/internal/persistence"
	"orchestrator/internal/repository"
	"orchestrator/internal/workflow"
)

// resumeTask continues a persisted run where its history left off (specs/resume/SPEC.md
// §6.1). It never decides again: no translator, decision provider or project-context
// manager exists here. Everything read back from the run directory is validated first,
// and on a run that has executed a step, the protected state is compared with the run's
// start - git fingerprint first - before anything runs git status. Until the operator
// approves, the persisted run is left exactly as it was found.
func resumeTask(ctx context.Context, configPath, runArg string, mode domain.RunMode, verbose bool, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := newReporter(stdout, verbose)
	defer reporter.Finish()
	stderr = reporter.Wrap(stderr)

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	// The current config applies to everything that runs from now on (SPEC §10), so it
	// goes through the same fail-fast chain as for run.
	cfg, err := loadAndValidateConfig(configPath, registry, osExec, reporter, noJiraDependencies)
	if err != nil {
		return err
	}
	reporter.TestCommand = cfg.Test.Command

	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to determine working directory", Err: err}
	}
	repo, err := repository.Open(ctx, cwd, osExec)
	if err != nil {
		return err
	}

	store := persistence.NewStore(runsBaseDir)
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
	if err := checkBaselineRecorded(run); err != nil {
		return err
	}

	opts := tailOptions{
		mode:        mode,
		defaultMode: resumeDefaultMode(run.Mode),
		resume:      &domain.Resume{FromStatus: run.Status, FromReason: run.FailureReason},
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
		// No step ran, so no model touched the tree and there is no baseline yet: this is
		// the tail of a fresh run (plan mode, cancelled at approval, refused for a dirty
		// tree, or crashed before the first step).
		dirty, err := repo.IsDirty(ctx)
		if err != nil {
			return err
		}
		if err := checkWorktree(cfg, *run.Workflow, dirty, stderr); err != nil {
			return err
		}
		opts.dirty = dirty
		opts.resume.Entry, opts.resume.Why = run.Workflow.Steps[0].Action, "start"
	} else {
		entry, why, err := workflow.Entry(run)
		if err != nil {
			return &apperr.RunStateError{Kind: apperr.RunCorrupt, RunID: run.ID, Msg: "cannot derive where to continue", Err: err}
		}
		if entry == workflow.StateDone {
			// The process died between persisting the last step and persisting the run's
			// completion; nothing is left to run.
			run.Status = domain.RunCompleted
			if err := store.SaveRun(run); err != nil {
				return err
			}
			return reportCompleted(s)
		}
		if err := checkProtectedState(ctx, repo, run); err != nil {
			return err
		}
		if err := workflow.CheckLimits(run, limitsView(cfg), entry); err != nil {
			return err
		}
		if err := s.dispatcher.Restore(run, store); err != nil {
			return err
		}
		opts.resume.Entry, opts.resume.Why = domain.ActionType(entry), why
		treeSummary, treeErr = repo.StatusSummary(ctx)
	}

	reporter.ResumeHeader(run, store.RunDir(run.ID))
	reporter.History(run)
	reporter.WorkingTree(treeSummary, treeErr)
	reporter.NextStep(opts.resume.Entry, opts.resume.Why, remainingSteps(*run.Workflow, opts.resume.Entry))
	// The overview above is read in full width; the sidebar opens for the steps.
	if reporter.Pretty {
		reporter.SetRepoInfo(repoInfo(ctx, repo))
		reporter.OpenDock()
		reporter.SetRun(run)
	}
	return executeWorkflow(ctx, s, opts)
}

// verifyWorkflow refuses a run without a workflow (nothing to continue; that is a new run)
// and a run whose workflow is not the one its decision builds (run.json was edited).
func verifyWorkflow(run *domain.Run) error {
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
