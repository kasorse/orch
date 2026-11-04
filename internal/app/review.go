package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/execution"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/jira"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/workflow"
)

// reviewChange reviews merge-base(base, head)..head without implement and without a clean tree,
// in a detached worktree at head, as an ordinary run: the decision is manual, testing and review,
// and everything after it is the tail run and resume share (specs/delivery/SPEC.md §12). A
// completed run whose verdict fails ends in apperr.NeedsPersonError, which main maps to exit 6.
func reviewChange(ctx context.Context, opts reviewOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	switch {
	case opts.base == "" && opts.head != "":
		return &apperr.ConfigError{Msg: "--head requires --base\n" + reviewUsage}
	case opts.base == "":
		return &apperr.ConfigError{Msg: "review requires --base <ref>\n" + reviewUsage}
	case opts.task != "" && opts.jiraRef != "":
		return &apperr.ConfigError{Msg: "--task cannot be combined with --jira: the ticket is the task"}
	}
	headName := opts.head
	if headName == "" {
		headName = "HEAD"
	}
	if err := domain.ValidReviewRefName(opts.base); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("--base %s", err)}
	}
	if err := domain.ValidReviewRefName(headName); err != nil {
		return &apperr.ConfigError{Msg: fmt.Sprintf("--head %s", err)}
	}

	reporter := newReporter(stdout, opts.verbose)
	defer reporter.Finish()
	stderr = reporter.Wrap(stderr)

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)
	cfg, clis, org, auth, codexAuth, err := loadAndValidateConfig(opts.configPath, opts.claudeAuth, opts.codexAuth, registry, osExec, reporter,
		func(config.Config) config.DependencyOptions {
			return config.DependencyOptions{Jira: opts.jiraRef != ""}
		})
	if err != nil {
		return err
	}
	// A review run always has a worktree of its own, whatever execution.isolation says.
	cfg.Execution.Isolation = domain.IsolationWorktree
	reporter.TestCommand = cfg.Test.Command
	var jiraKey string
	if opts.jiraRef != "" {
		if jiraKey, err = jira.ParseRef(opts.jiraRef, cfg.Jira.BaseURL); err != nil {
			return err
		}
	}

	started := time.Now()
	decideCtx, cancelDecide := context.WithTimeout(ctx, cfg.Execution.Timeout)
	defer cancelDecide()

	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to determine working directory", Err: err}
	}
	reporter.Cwd = cwd
	repo, err := repository.Open(decideCtx, cwd, osExec)
	if err != nil {
		return err
	}
	// Before the run exists: a ref that is no commit, or a change that is empty, creates nothing.
	target, err := resolveReviewTarget(decideCtx, repo, opts.base, headName)
	if err != nil {
		return err
	}

	var stream io.Writer
	if opts.verbose {
		stream = reporter.StreamWriter()
	}

	task := domain.Task{CreatedAt: time.Now()}
	var ticket domain.Ticket
	switch {
	case jiraKey != "":
		// The one Jira request. A failure creates no run (specs/jira/SPEC.md J2).
		if ticket, task.Description, err = taskFromJira(decideCtx, cfg, jiraKey, "", reporter); err != nil {
			return err
		}
		task.Source = &domain.TaskSource{
			Kind: domain.TaskSourceJira, Key: ticket.Key, RequestedKey: ticket.RequestedKey,
			URL: ticket.URL, FetchedAt: ticket.FetchedAt,
		}
	case opts.task != "":
		task.Description = strings.TrimSpace(opts.task)
	default:
		if task.Description, err = reviewTaskFromCommits(decideCtx, repo, target); err != nil {
			return err
		}
	}

	store := persistence.NewStore(runsBaseDir)
	run, err := store.NewRun(task)
	if err != nil {
		return err
	}
	run.ProviderCLI = clis
	run.OrgConfig = orgRef(org)
	run.ClaudeAuth = &auth
	if codexAuth.Mode != "" {
		run.CodexAuth = &codexAuth
	}
	run.Options = runOptions(cfg)
	run.Options.Isolation = domain.IsolationWorktree
	run.ReviewTarget = &target
	recordResultRun(ctx, store, run)
	// Held until the run returns, so a resume cannot take the run over while it is alive.
	unlock, err := store.Lock(run.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if jiraKey != "" {
		if err := store.SaveTicket(run.ID, ticket); err != nil {
			return failRun(store, run, err)
		}
	}
	if reporter.Pretty {
		reporter.SetRepoInfo(repoInfo(decideCtx, repo))
	}
	reporter.RunHeader(run.ID, store.RunDir(run.ID))

	// No decision provider, translator or Context Manager: the stages are fixed.
	dec, err := domain.ManualDecision([]domain.Stage{domain.StageTesting, domain.StageReview})
	if err != nil {
		return failRun(store, run, &apperr.ConfigError{Msg: err.Error()})
	}
	wf, err := workflow.Build(dec.Stages)
	if err != nil {
		return failRun(store, run, &apperr.WorkflowError{Msg: "failed to build the workflow", Err: err})
	}
	run.Decision = &dec
	run.Workflow = &wf
	reporter.SetRun(run)
	trace := domain.ManualTrace(task.Description, dec, wf)
	trace.Models = modelRoutes(cfg, dec, wf)
	if err := store.SaveDecision(run.ID, trace); err != nil {
		return err
	}
	if err := store.SaveRun(run); err != nil {
		return err
	}
	reporter.ManualStages(dec.Stages.EnabledStages())
	warnPathRules(stderr, run.Options.PathRules, wf)

	prompter := reporter.PausingPrompter(interaction.NewTerminalPrompter(stdin, stdout))
	s := newSession(ctx, &cfg, opts.configPath, registry, osExec, repo, store, run, reporter, prompter, stream, stdout)
	s.stdin = stdin
	tail := tailOptions{mode: opts.mode}
	tail.engineContext = func() (context.Context, context.CancelFunc) {
		return engineContext(ctx, decideCtx, cfg.Execution.Timeout, time.Since(started), 0)
	}
	if err := executeWorkflow(ctx, s, tail); err != nil {
		return err
	}
	return reviewVerdictError(run, store)
}
