// Package app wires config, provider, translation, the decision provider, context
// collection, classification, repository, testexec, action, persistence, and workflow
// together into the orchestrator's four CLI commands
// (version, validate, run, resume). This is the composition root - no other package
// constructs the full dependency graph.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/config"
	"orchestrator/internal/decision"
	"orchestrator/internal/decision/jev"
	"orchestrator/internal/decision/llm"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/interaction"
	"orchestrator/internal/jira"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/claude"
	"orchestrator/internal/provider/codex"
	"orchestrator/internal/provider/gemini"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/workflow"
)

const Version = "0.1.0"

const defaultConfigPath = "orchestrator.yaml"

const runsBaseDir = ".orchestrator/runs"

const usage = `usage: orchestrator [--config path] [--mode plan|manual|accept-edits|auto] [--verbose] ` +
	`<version|validate|run [--stage <name>]... "<task>"|run --jira <KEY|URL> ["notes"]|resume <run-id>>`

// Run parses args and dispatches to the requested subcommand. It never calls
// os.Exit - main.go is the sole exit point, mapping the returned error via
// exitcode.FromError.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cli, err := parseArgs(args)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	rest := cli.rest
	if len(rest) == 0 {
		return &apperr.ConfigError{Msg: usage}
	}
	if cli.jiraRef != "" && rest[0] != "run" {
		return &apperr.ConfigError{Msg: "--jira is only valid with run"}
	}
	if len(cli.stages) > 0 && rest[0] != "run" {
		return &apperr.ConfigError{Msg: "--stage is only valid with run"}
	}

	switch rest[0] {
	case "version":
		fmt.Fprintf(stdout, "orchestrator version %s\n", Version)
		return nil
	case "validate":
		return runValidate(cli.configPath, stdout)
	case "run":
		// With --jira the ticket is the task, and any text is the operator's notes on it.
		if cli.jiraRef == "" && len(rest) < 2 {
			return &apperr.ConfigError{Msg: `usage: orchestrator run [--stage <name>]... "<task description>"   or   orchestrator run --jira <KEY|URL> ["notes"]`}
		}
		return runTask(ctx, cli.configPath, strings.Join(rest[1:], " "), cli.jiraRef, cli.stages, cli.mode, cli.verbose, stdin, stdout, stderr)
	case "resume":
		if len(rest) != 2 {
			return &apperr.ConfigError{Msg: `usage: orchestrator resume <run-id>   (the full ID, or a unique prefix of at least 8 characters)`}
		}
		return resumeTask(ctx, cli.configPath, rest[1], cli.mode, cli.verbose, stdin, stdout, stderr)
	default:
		return &apperr.ConfigError{Msg: fmt.Sprintf("unknown command %q", rest[0])}
	}
}

// cliArgs is the command line after parseArgs.
type cliArgs struct {
	configPath string
	// mode is empty if --mode was not given - the caller then runs the interactive
	// mode-select prompt.
	mode domain.RunMode
	// verbose is --verbose (spec §11 Q5: the sole verbosity switch - no config key or env
	// var).
	verbose bool
	// jiraRef is --jira's value: an issue key or its URL (specs/jira/SPEC.md §6).
	jiraRef string
	// stages are the --stage values, parsed, in the order given (specs/stage-select/SPEC.md
	// §4); empty means the decision layer chooses.
	stages []domain.Stage
	// rest are the positional arguments.
	rest []string
}

// parseArgs pulls --config/--config=X, --mode/--mode=X, --jira/--jira=X, the repeatable
// --stage/--stage=X and --verbose/-v out of args, in any position, and returns them with the remaining
// positional arguments.
func parseArgs(args []string) (cliArgs, error) {
	cli := cliArgs{configPath: defaultConfigPath}
	// value returns the flag's value from "--flag=X" or the next argument.
	value := func(i *int, name string) (string, error) {
		if v, ok := strings.CutPrefix(args[*i], name+"="); ok {
			return v, nil
		}
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", name)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config" || strings.HasPrefix(arg, "--config="):
			v, err := value(&i, "--config")
			if err != nil {
				return cliArgs{}, err
			}
			cli.configPath = v
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, err := value(&i, "--mode")
			if err != nil {
				return cliArgs{}, err
			}
			if cli.mode, err = domain.ParseRunMode(v); err != nil {
				return cliArgs{}, err
			}
		case arg == "--jira" || strings.HasPrefix(arg, "--jira="):
			if cli.jiraRef != "" {
				return cliArgs{}, fmt.Errorf("--jira given twice")
			}
			v, err := value(&i, "--jira")
			if err != nil {
				return cliArgs{}, err
			}
			if cli.jiraRef = strings.TrimSpace(v); cli.jiraRef == "" {
				return cliArgs{}, fmt.Errorf("--jira requires an issue key or URL")
			}
		case arg == "--stage" || strings.HasPrefix(arg, "--stage="):
			v, err := value(&i, "--stage")
			if err != nil {
				return cliArgs{}, err
			}
			parsed, err := parseStageFlags([]string{v})
			if err != nil {
				return cliArgs{}, err
			}
			if !slices.Contains(cli.stages, parsed[0]) {
				cli.stages = append(cli.stages, parsed[0])
			}
		case arg == "--verbose" || arg == "-v":
			cli.verbose = true
		default:
			cli.rest = append(cli.rest, arg)
		}
	}
	return cli, nil
}

// sandboxProbeTimeout bounds the one bwrap run that checks the host can sandbox tests.
const sandboxProbeTimeout = 10 * time.Second

// loadAndValidateConfig runs the whole fail-fast chain (MVP.md §7): Load -> Validate ->
// (register the credential variables to scrub) -> ValidateDependencies (-> the sandbox probe, with test.sandbox: bwrap) ->
// ValidateAuthorities. The registry is only asked what its
// adapters can enforce; constructing it makes no calls. depOpts chooses the optional
// dependency checks from the loaded config (see config.DependencyOptions).
func loadAndValidateConfig(configPath string, registry *provider.Registry, executor execution.ProcessExecutor, reporter *logging.ConsoleReporter, depOpts func(config.Config) config.DependencyOptions) (config.Config, error) {
	reporter.ValidatingConfig()

	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return config.Config{}, err
	}
	// From here on no child process - the sandbox probe included - inherits a variable
	// the config names as a credential (specs/jira/SPEC.md §12).
	execution.ScrubCredentials(cfg.CredentialEnvNames()...)
	if err := config.ValidateDependencies(cfg, config.ExecDependencyChecker{}, depOpts(cfg)); err != nil {
		return config.Config{}, err
	}
	// A host that cannot sandbox (e.g. AppArmor blocking bwrap's user namespaces) must fail
	// here, not at the first test step after the AI calls.
	if cfg.Test.Sandboxed() {
		probeCtx, cancel := context.WithTimeout(context.Background(), sandboxProbeTimeout)
		err := testexec.Probe(probeCtx, executor)
		cancel()
		if err != nil {
			return config.Config{}, err
		}
	}
	// Authority does not depend on the run mode (specs/permissions/SPEC.md §5.2), so this
	// single check, before the mode is even known, is complete.
	if err := config.ValidateAuthorities(cfg, registry); err != nil {
		return config.Config{}, err
	}

	reporter.ConfigValid()
	return cfg, nil
}

// newRegistry registers every supported provider adapter with executor.
func newRegistry(executor execution.ProcessExecutor) *provider.Registry {
	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(executor))
	registry.Register(provider.Codex, codex.New(executor))
	registry.Register(provider.Gemini, gemini.New(executor))
	return registry
}

// noJiraDependencies is the dependency check of a command that does not fetch a ticket:
// a jira section's variables are not its concern (specs/jira/SPEC.md §7.2).
func noJiraDependencies(config.Config) config.DependencyOptions { return config.DependencyOptions{} }

// runValidate checks the Jira variables whenever the section is present, so validate
// answers "would run --jira work here?".
func runValidate(configPath string, stdout io.Writer) error {
	executor := execution.NewOSProcessExecutor()
	_, err := loadAndValidateConfig(configPath, newRegistry(executor), executor, logging.NewTerminalReporter(stdout),
		func(cfg config.Config) config.DependencyOptions {
			return config.DependencyOptions{Jira: cfg.Jira != nil}
		})
	return err
}

// runTask runs a new task. text is the task typed on the command line or, with jiraRef
// (run --jira), the operator's notes on the ticket, which may be empty.
func runTask(ctx context.Context, configPath, text, jiraRef string, stages []domain.Stage, mode domain.RunMode, verbose bool, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := newReporter(stdout, verbose)
	defer reporter.Pause()

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	// Fail fast (MVP.md §7): the whole validation chain runs before any decision client
	// or translator exists and before any provider is called. With --jira it includes the
	// credential variables, and the reference is resolved before any request.
	cfg, err := loadAndValidateConfig(configPath, registry, osExec, reporter,
		func(config.Config) config.DependencyOptions { return config.DependencyOptions{Jira: jiraRef != ""} })
	if err != nil {
		return err
	}
	reporter.TestCommand = cfg.Test.Command
	var jiraKey string
	if jiraRef != "" {
		if jiraKey, err = jira.ParseRef(jiraRef, cfg.Jira.BaseURL); err != nil {
			return err
		}
	}

	// execution.timeout starts here and covers translation, the pre-check, context
	// collection, classification and the workflow - but not the time the user spends on
	// a manual choice (see engineContext).
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
	// Taken now, before the run directory exists: .orchestrator/runs/ may live inside the
	// worktree, and the run must not count its own files as uncommitted changes.
	dirty, err := repo.IsDirty(decideCtx)
	if err != nil {
		return err
	}

	var stream io.Writer
	if verbose {
		stream = reporter.StreamWriter()
	}

	task := domain.Task{Description: text, CreatedAt: time.Now()}
	var ticket domain.Ticket
	if jiraKey != "" {
		// The one Jira request. A failure creates no run: there is nothing to resume
		// without a task (specs/jira/SPEC.md J2).
		if ticket, task.Description, err = taskFromJira(decideCtx, cfg, jiraKey, text, reporter); err != nil {
			return err
		}
		task.Source = &domain.TaskSource{
			Kind: domain.TaskSourceJira, Key: ticket.Key, RequestedKey: ticket.RequestedKey,
			URL: ticket.URL, FetchedAt: ticket.FetchedAt, Notes: strings.TrimSpace(text) != "",
		}
	}

	store := persistence.NewStore(runsBaseDir)
	run, err := store.NewRun(task)
	if err != nil {
		return err
	}
	// Held until the run returns, so a resume cannot take the run over while it is alive
	// (specs/resume/SPEC.md §11).
	unlock, err := store.Lock(run.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if task.Source != nil {
		if err := store.SaveTicket(run.ID, ticket); err != nil {
			return failRun(store, run, err)
		}
	}
	reporter.RunHeader(run.ID, store.RunDir(run.ID))

	// One prompter for every question on stdin: it buffers input, so a second reader
	// would swallow answers meant for the first.
	prompter := reporter.PausingPrompter(interaction.NewTerminalPrompter(stdin, stdout))
	interactive := mode != domain.ModeAuto && isTerminal(stdin)

	var out decisionOutcome
	if len(stages) > 0 {
		out, err = decideManual(stages, task.Description, reporter)
	} else {
		out, err = decideWithProvider(decideCtx, ctx, cfg, registry, store, repo, cwd, run, task.Description,
			reporter, prompter, interactive, stream, stderr)
	}
	if err != nil {
		return failRun(store, run, err)
	}

	wf, err := workflow.Build(out.decision.Stages)
	if err != nil {
		return failRun(store, run, &apperr.WorkflowError{Msg: "failed to build the workflow", Err: err})
	}
	// Persisted before the dirty-tree refusal, so a refused run keeps its decided workflow
	// and can be resumed once the tree is clean (specs/resume/SPEC.md G10).
	run.Decision = &out.decision
	run.Workflow = &wf
	if err := store.SaveDecision(run.ID, out.trace(wf)); err != nil {
		return err
	}
	if err := store.SaveProjectContext(run.ID, out.project); err != nil {
		return err
	}
	if err := store.SaveRun(run); err != nil {
		return err
	}
	reporter.Pause()
	if err := checkWorktree(cfg, wf, dirty, stderr); err != nil {
		return failRun(store, run, err)
	}
	out.announce(wf, stderr)

	s := newSession(ctx, &cfg, configPath, registry, osExec, repo, store, run, reporter, prompter, stream, stdout)
	return executeWorkflow(ctx, s, tailOptions{
		mode:  mode,
		dirty: dirty,
		engineContext: func() (context.Context, context.CancelFunc) {
			return engineContext(ctx, decideCtx, cfg.Execution.Timeout, time.Since(started), out.manualChoice)
		},
	})
}

// checkWorktree applies the clean-worktree rule to a run that has not executed a step: a
// workflow that writes must start from a clean restore point (specs/permissions/SPEC.md
// §7.2). It is checked before any approval prompt, so nobody approves a run that cannot
// start. A dirty tree that is allowed only gets a warning.
func checkWorktree(cfg config.Config, wf domain.Workflow, dirty bool, stderr io.Writer) error {
	if !dirty {
		return nil
	}
	if wf.Contains(domain.ActionImplement) && cfg.Security.CleanWorktreeRequired() {
		return &apperr.ConfigError{Msg: "the workflow includes implement, but the repository has uncommitted changes\n\n" +
			"Commit or stash them first, so the run starts from a clean restore point,\n" +
			"or set security.require_clean_worktree: false to run on top of them."}
	}
	logging.DirtyRepoWarning(stderr)
	return nil
}

// session is what the part of a run after the decision needs. run and resume each build
// one, and both hand it to executeWorkflow.
type session struct {
	cfg        *config.Config
	repo       *repository.Repository
	store      *persistence.Store
	run        *domain.Run
	reporter   *logging.ConsoleReporter
	prompter   interaction.Prompter
	stdout     io.Writer
	runner     *testexec.Runner
	dispatcher *action.Dispatcher
	// protected are the patterns the run must never change: security.protected_paths and
	// the config file. They make the run-start baseline; the step guard adds the runs
	// directory's resume inputs, which change with every step.
	protected []string
	// runsRel is the runs directory relative to the repository top level, or "".
	runsRel string
}

// newSession builds the Dispatcher and resolves the guarded paths. Nothing here calls a
// provider or runs git status: resume builds it before the baseline check, to restore the
// Dispatcher's context from the artifacts.
func newSession(ctx context.Context, cfg *config.Config, configPath string, registry *provider.Registry, osExec execution.ProcessExecutor,
	repo *repository.Repository, store *persistence.Store, run *domain.Run, reporter *logging.ConsoleReporter,
	prompter interaction.Prompter, stream, stdout io.Writer) *session {
	runner := testexec.New(osExec)
	dispatcher := action.New(cfg, registry, repo, runner)
	dispatcher.Artifacts = store
	dispatcher.Logs = store
	dispatcher.Observer = reporter
	dispatcher.Stream = stream
	runsRel := worktreeRel(ctx, repo, runsBaseDir)
	dispatcher.RunsDir = runsRel
	return &session{
		cfg: cfg, repo: repo, store: store, run: run, reporter: reporter, prompter: prompter, stdout: stdout,
		runner: runner, dispatcher: dispatcher,
		protected: slices.Concat(cfg.Security.ProtectedPaths, configPattern(ctx, repo, configPath)),
		runsRel:   runsRel,
	}
}

// tailOptions is how run and resume differ in the shared tail.
type tailOptions struct {
	// mode is --mode; empty asks.
	mode domain.RunMode
	// defaultMode is what an empty answer to the mode prompt picks; empty has no default.
	defaultMode domain.RunMode
	// dirty is whether a run without executions starts on uncommitted changes, which
	// makes its restore point a snapshot commit.
	dirty bool
	// resume is set by `resume`: the record appended to run.Resumes once the operator
	// approved. Until then the persisted run is left exactly as it was found.
	resume *domain.Resume
	// engineContext starts the workflow's share of execution.timeout.
	engineContext func() (context.Context, context.CancelFunc)
}

// executeWorkflow is everything after the decision, shared by run and resume: the
// authority line, approval and mode, the restore point and run-start baseline (for a run
// that has not executed a step), the test sandbox, the step guard, the engine and the
// final answer.
func executeWorkflow(ctx context.Context, s *session, opts tailOptions) error {
	run, store, cfg := s.run, s.store, s.cfg
	s.reporter.Authority(*run.Workflow, cfg.Test.Command, cfg.Test.Sandboxed())
	if len(run.Executions) > 0 && run.PreRunRef != "" {
		s.reporter.RestorePoint(run.PreRunRef)
	}

	mode := opts.mode
	if mode != domain.ModeAuto {
		resolvedMode, approved, err := interaction.ReviewAndSelectMode(s.prompter, s.stdout, mode, opts.defaultMode)
		if err != nil {
			if opts.resume != nil {
				return err
			}
			return failRun(store, run, err)
		}
		if !approved {
			if opts.resume == nil {
				run.Status = domain.RunStopped
				run.FailureReason = "cancelled by user"
				_ = store.SaveRun(run)
			}
			return fmt.Errorf("run cancelled by user: %w", context.Canceled)
		}
		mode = resolvedMode
		if mode == domain.ModePlan {
			// A plan-mode resume changes nothing: the run stays resumable as it was.
			if opts.resume == nil {
				run.Mode = mode
				if err := store.SaveRun(run); err != nil {
					return err
				}
			}
			s.reporter.PlanApproved()
			return nil
		}
	}
	run.Mode = mode
	if opts.resume != nil {
		record := *opts.resume
		record.At = time.Now()
		record.Mode = mode
		run.Resumes = append(run.Resumes, record)
		run.Status = domain.RunRunning
		run.FailureReason = ""
	}
	if err := store.SaveRun(run); err != nil {
		return err
	}

	if len(run.Executions) == 0 {
		recordRestorePoint(ctx, s.repo, run, opts.dirty, s.reporter)
		// The guard over what the run must never change: the run-start baseline a resume
		// is checked against.
		if err := recordBaseline(ctx, s.repo, run, action.NewRepoGuard(s.repo, s.protected)); err != nil {
			return failRun(store, run, err)
		}
		if err := store.SaveRun(run); err != nil {
			return err
		}
	}

	runCtx, cancelRun := opts.engineContext()
	defer cancelRun()

	if cfg.Test.Sandboxed() {
		sandbox, err := testSandbox(ctx, s.repo, cfg.Test)
		if err != nil {
			return failRun(store, run, err)
		}
		s.runner.Sandbox = sandbox
	}
	// The step guard also covers the runs directory's resume inputs, which change with
	// every step and so cannot be part of the baseline.
	s.dispatcher.Guard = action.NewRepoGuard(s.repo, slices.Concat(s.protected, action.RunDirPatterns(s.runsRel)))

	s.reporter.MaxFixIterations = cfg.Limits.MaxFixIterations

	engine := &workflow.Engine{
		Executor: s.dispatcher,
		Observer: &runObserver{reporter: s.reporter, store: store},
		Persist:  store.SaveRun,
		Limits:   limitsView(*cfg),
	}

	if mode == domain.ModeManual {
		gate := interaction.NewTerminalGate(s.prompter, s.reporter.PausingWriter())
		gate.Changes = s.repo.StatusSummary
		engine.Gate = gate
	}

	if err := engine.Run(runCtx, run); err != nil {
		return err
	}
	if answer := s.dispatcher.FinalAnswer(run); answer != "" {
		s.reporter.Answer(answer)
	}
	return nil
}

func limitsView(cfg config.Config) domain.LimitsView {
	return domain.LimitsView{MaxSteps: cfg.Limits.MaxSteps, MaxFixIterations: cfg.Limits.MaxFixIterations}
}

// recordRestorePoint sets run.PreRunRef before the first step: HEAD on a clean tree, or a
// snapshot commit of everything the run starts on top of, untracked files included.
// Failing to record it (e.g. a repository without commits) is reported, not fatal.
func recordRestorePoint(ctx context.Context, repo *repository.Repository, run *domain.Run, dirty bool, reporter *logging.ConsoleReporter) {
	var ref string
	var err error
	if dirty {
		ref, err = repo.SnapshotCommit(ctx)
	} else {
		ref, err = repo.HeadRef(ctx)
	}
	if err != nil {
		reporter.RestorePointUnavailable(err)
		return
	}
	run.PreRunRef = ref
	reporter.RestorePoint(ref)
}

// recordBaseline sets run.HeadRef and run.ResumeBaseline before the first step
// (specs/resume/SPEC.md §9.1-§9.2). Unlike the restore point, a baseline that cannot be
// taken stops the run: without it the run could never be resumed safely, and the guard
// takes the same snapshot around every implement and test anyway.
func recordBaseline(ctx context.Context, repo *repository.Repository, run *domain.Run, guard *action.RepoGuard) error {
	if head, err := repo.HeadRef(ctx); err == nil {
		run.HeadRef = head
	}
	state, err := guard.Snapshot(ctx)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to record the protected state before the first step", Err: err}
	}
	baseline, err := json.Marshal(state)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to record the protected state before the first step", Err: err}
	}
	run.ResumeBaseline = baseline
	return nil
}

// testSandbox resolves test.sandbox: bwrap into the paths the sandbox binds: the
// repository root writable, its git directories read-only over it, $HOME hidden, and the
// configured binds.
func testSandbox(ctx context.Context, repo *repository.Repository, t config.TestConfig) (*testexec.Sandbox, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, &apperr.ConfigError{Msg: "test.sandbox: cannot determine the home directory to hide", Err: err}
	}
	top, err := repo.TopLevel(ctx)
	if err != nil {
		return nil, err
	}
	gitDirs, err := repo.GitDirs(ctx)
	if err != nil {
		return nil, err
	}
	ro, rw := t.ResolveBinds(home)
	return &testexec.Sandbox{Home: home, RepoRoot: top, GitDirs: gitDirs, ReadOnly: ro, ReadWrite: rw}, nil
}

// configPattern returns the config file's path relative to the repository root when it
// lives inside the worktree, so implement cannot change the test command the next run
// executes; nil when the config is outside the worktree.
func configPattern(ctx context.Context, repo *repository.Repository, configPath string) []string {
	if rel := worktreeRel(ctx, repo, configPath); rel != "" {
		return []string{rel}
	}
	return nil
}

// worktreeRel returns p relative to the repository root, slash-separated, or "" when p is
// outside the worktree or cannot be resolved.
func worktreeRel(ctx context.Context, repo *repository.Repository, p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	// git reports the top level with symlinks resolved; so must p be, or a checkout
	// reached through a symlink would put every path "outside" the worktree.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	top, err := repo.TopLevel(ctx)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(top, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// newDecisionProvider builds the configured decision provider (TASKS.md T22): Jev over
// HTTP, its offline mock, or a CLI provider. The Jev API key is read from the
// environment here and handed only to the client's constructor.
func newDecisionProvider(cfg config.Config, registry *provider.Registry, store *persistence.Store, runID string, reporter *logging.ConsoleReporter, stream, stderr io.Writer) (decision.Provider, error) {
	d := cfg.Decision
	sources := d.Complexity.Sources()

	if d.UsesJev() {
		var client jev.Client
		if d.Jev.Mock {
			fmt.Fprintln(stderr, "Warning: decision.jev.mock is on - decisions come from an offline keyword heuristic, not from Jev.")
			client = jev.NewMockClient()
		} else {
			client = jev.NewHTTPClient(jev.Options{BaseURL: d.Jev.BaseURL, Timeout: d.Timeout, MaxRetries: d.Jev.MaxRetries}, os.Getenv(d.Jev.APIKeyEnv))
		}
		p, err := jev.NewProvider(client, d.Model, sources)
		if err != nil {
			return nil, &apperr.WorkflowError{Msg: "failed to build the jev decision provider", Err: err}
		}
		return p, nil
	}

	client, err := registry.Resolve(d.Provider)
	if err != nil {
		return nil, &apperr.WorkflowError{Msg: "failed to resolve the decision provider", Err: err}
	}
	return &llm.Provider{
		Client: client, ProviderID: d.Provider, Model: d.Model, Timeout: d.Timeout, Sources: sources,
		RunID: runID, Logger: store, Observer: logging.CallReporter{ConsoleReporter: reporter, Label: "Deciding the workflow"}, Stream: stream,
	}, nil
}

// engineContext is the workflow's share of execution.timeout. Without a manual choice
// it is just the deciding context, deadline unchanged. When the user chose by hand,
// their thinking time is given back: the workflow gets execution.timeout minus the time
// the decision phase itself took.
func engineContext(ctx, decideCtx context.Context, timeout, elapsed, userTime time.Duration) (context.Context, context.CancelFunc) {
	if userTime <= 0 {
		return decideCtx, func() {}
	}
	return context.WithTimeout(ctx, timeout-(elapsed-userTime))
}

// newReporter is the run/resume console reporter: pretty on an interactive terminal,
// the spec's plain text anywhere else.
func newReporter(stdout io.Writer, verbose bool) *logging.ConsoleReporter {
	r := logging.NewTerminalReporter(stdout)
	r.Verbose = verbose
	r.Version = Version
	return r
}

// isTerminal reports whether stdin can answer questions. A real stdin that is not a
// terminal (a pipe, /dev/null, a CI runner) cannot - then the manual choice fails at
// once instead of reading EOF. Any other reader is a caller-scripted input (tests).
func isTerminal(stdin io.Reader) bool {
	f, ok := stdin.(*os.File)
	if !ok {
		return true
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// failRun records a failure that ended the run before the workflow started.
func failRun(store *persistence.Store, run *domain.Run, err error) error {
	run.Status = domain.RunFailed
	run.FailureReason = err.Error()
	_ = store.SaveRun(run)
	return err
}

// runObserver renders CLI progress via ConsoleReporter and, alongside that, persists
// each step's raw provider/test output to the run's log files.
type runObserver struct {
	reporter *logging.ConsoleReporter
	store    *persistence.Store
}

func (o *runObserver) StepStarted(run *domain.Run, index int, action domain.ActionType) {
	o.reporter.StepStarted(run, index, action)
}

func (o *runObserver) StepFinished(run *domain.Run, index int, exec domain.StepExecution, outcome workflow.StepOutcome) {
	if outcome.RawStdout != "" {
		_ = o.store.AppendLog(run.ID, index, exec.Action, "stdout", outcome.RawStdout)
	}
	if outcome.RawStderr != "" {
		_ = o.store.AppendLog(run.ID, index, exec.Action, "stderr", outcome.RawStderr)
	}
	o.reporter.StepFinished(run, index, exec, outcome)
}

func (o *runObserver) RunFinished(run *domain.Run) {
	o.reporter.RunFinished(run)
}
