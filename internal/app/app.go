// Package app wires config, provider, translation, the decision provider, context
// collection, classification, repository, testexec, action, persistence, and workflow
// together into the orchestrator's three CLI commands
// (version, validate, run). This is the composition root - no other package
// constructs the full dependency graph.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/classification"
	"orchestrator/internal/config"
	"orchestrator/internal/decision"
	"orchestrator/internal/decision/jev"
	"orchestrator/internal/decision/llm"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/interaction"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/projectctx"
	"orchestrator/internal/provider"
	"orchestrator/internal/provider/claude"
	"orchestrator/internal/provider/codex"
	"orchestrator/internal/provider/gemini"
	"orchestrator/internal/repository"
	"orchestrator/internal/testexec"
	"orchestrator/internal/translate"
	"orchestrator/internal/workflow"
)

const Version = "0.1.0"

const defaultConfigPath = "orchestrator.yaml"

const runsBaseDir = ".orchestrator/runs"

// Run parses args and dispatches to the requested subcommand. It never calls
// os.Exit - main.go is the sole exit point, mapping the returned error via
// exitcode.FromError.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	configPath, mode, verbose, rest, err := parseArgs(args)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	if len(rest) == 0 {
		return &apperr.ConfigError{Msg: `usage: orchestrator [--config path] [--mode plan|manual|accept-edits|auto] [--verbose] <version|validate|run "<task>">`}
	}

	switch rest[0] {
	case "version":
		fmt.Fprintf(stdout, "orchestrator version %s\n", Version)
		return nil
	case "validate":
		return runValidate(configPath, stdout)
	case "run":
		if len(rest) < 2 {
			return &apperr.ConfigError{Msg: `usage: orchestrator run "<task description>"`}
		}
		return runTask(ctx, configPath, strings.Join(rest[1:], " "), mode, verbose, stdin, stdout, stderr)
	default:
		return &apperr.ConfigError{Msg: fmt.Sprintf("unknown command %q", rest[0])}
	}
}

// parseArgs pulls --config/--config=X, --mode/--mode=X, and --verbose/-v out of args (in
// any position) and returns the resolved config path, resolved mode (empty if --mode was
// not given - the caller then runs the interactive mode-select prompt), whether --verbose
// was given (spec §11 Q5: the sole verbosity switch - no config key or env var), and the
// remaining positional arguments.
func parseArgs(args []string) (configPath string, mode domain.RunMode, verbose bool, rest []string, err error) {
	configPath = defaultConfigPath
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			if i+1 >= len(args) {
				return "", "", false, nil, fmt.Errorf("--config requires a value")
			}
			configPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--mode":
			if i+1 >= len(args) {
				return "", "", false, nil, fmt.Errorf("--mode requires a value")
			}
			if mode, err = domain.ParseRunMode(args[i+1]); err != nil {
				return "", "", false, nil, err
			}
			i++
		case strings.HasPrefix(arg, "--mode="):
			if mode, err = domain.ParseRunMode(strings.TrimPrefix(arg, "--mode=")); err != nil {
				return "", "", false, nil, err
			}
		case arg == "--verbose" || arg == "-v":
			verbose = true
		default:
			rest = append(rest, arg)
		}
	}
	return configPath, mode, verbose, rest, nil
}

// sandboxProbeTimeout bounds the one bwrap run that checks the host can sandbox tests.
const sandboxProbeTimeout = 10 * time.Second

// loadAndValidateConfig runs the whole fail-fast chain (MVP.md §7): Load -> Validate ->
// ValidateDependencies (-> the sandbox probe, with test.sandbox: bwrap) ->
// ValidateAuthorities. The registry is only asked what its
// adapters can enforce; constructing it makes no calls.
func loadAndValidateConfig(configPath string, registry *provider.Registry, executor execution.ProcessExecutor, reporter *logging.ConsoleReporter) (config.Config, error) {
	reporter.ValidatingConfig()

	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return config.Config{}, err
	}
	if err := config.ValidateDependencies(cfg, config.ExecDependencyChecker{}); err != nil {
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

func runValidate(configPath string, stdout io.Writer) error {
	executor := execution.NewOSProcessExecutor()
	_, err := loadAndValidateConfig(configPath, newRegistry(executor), executor, logging.NewConsoleReporter(stdout))
	return err
}

func runTask(ctx context.Context, configPath, taskDescription string, mode domain.RunMode, verbose bool, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := logging.NewConsoleReporter(stdout)
	reporter.Verbose = verbose

	osExec := execution.NewOSProcessExecutor()
	registry := newRegistry(osExec)

	// Fail fast (MVP.md §7): the whole validation chain runs before any decision client
	// or translator exists and before any provider is called.
	cfg, err := loadAndValidateConfig(configPath, registry, osExec, reporter)
	if err != nil {
		return err
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
		stream = logging.NewPrefixWriter(stdout, "  | ")
	}

	store := persistence.NewStore(runsBaseDir)
	task := domain.Task{Description: taskDescription, CreatedAt: time.Now()}
	run, err := store.NewRun(task)
	if err != nil {
		return err
	}
	reporter.RunHeader(run.ID, store.RunDir(run.ID))

	decisions, err := newDecisionProvider(cfg, registry, store, run.ID, reporter, stream, stderr)
	if err != nil {
		return failRun(store, run, err)
	}
	translatorClient, err := registry.Resolve(cfg.Translation.Provider)
	if err != nil {
		return failRun(store, run, &apperr.WorkflowError{Msg: "failed to resolve translation provider", Err: err})
	}

	// One prompter for every question on stdin: it buffers input, so a second reader
	// would swallow answers meant for the first.
	prompter := interaction.NewTerminalPrompter(stdin, stdout)
	interactive := mode != domain.ModeAuto && isTerminal(stdin)

	svc := &classification.Service{
		Translator: &translate.Translator{
			Client: translatorClient, ProviderID: cfg.Translation.Provider, Model: cfg.Translation.Model,
			Timeout: cfg.Translation.Timeout, Logger: store,
			Observer: logging.CallReporter{ConsoleReporter: reporter}, Stream: stream,
		},
		Decisions: decisions,
		Context: projectctx.NewManager(repo, cwd, projectctx.Options{
			MaxFiles: cfg.Context.MaxFiles, MaxBytes: cfg.Context.MaxBytes, ExtraDeny: cfg.Context.ExtraDeny,
		}),
		Chooser:      interaction.NewChooser(prompter, stdout, interactive),
		Policy:       cfg.DecisionPolicy(),
		Primary:      domain.ComplexitySource(cfg.Decision.Complexity.Primary),
		AskOnFailure: cfg.Decision.OnFailure == config.OnFailureAsk,
	}

	reporter.Deciding()
	// The manual choice waits on ctx: Ctrl-C still cancels it, execution.timeout does not.
	res, err := svc.Classify(decideCtx, classification.Request{RunID: run.ID, Text: task.Description, UserCtx: ctx})
	if err != nil {
		return failRun(store, run, err)
	}

	wf, err := workflow.Build(res.Decision.Stages)
	if err != nil {
		return failRun(store, run, &apperr.WorkflowError{Msg: "failed to build the workflow", Err: err})
	}
	// A workflow that writes must start from a clean restore point (specs/permissions/
	// SPEC.md §7.2); checked before any approval prompt, so nobody approves a run that
	// cannot start.
	if dirty {
		if wf.Contains(domain.ActionImplement) && cfg.Security.CleanWorktreeRequired() {
			return failRun(store, run, &apperr.ConfigError{Msg: "the workflow includes implement, but the repository has uncommitted changes\n\n" +
				"Commit or stash them first, so the run starts from a clean restore point,\n" +
				"or set security.require_clean_worktree: false to run on top of them."})
		}
		logging.DirtyRepoWarning(stderr)
	}
	run.Decision = &res.Decision
	run.Workflow = &wf

	if err := store.SaveDecision(run.ID, res.Trace(wf)); err != nil {
		return err
	}
	if err := store.SaveProjectContext(run.ID, res.Project); err != nil {
		return err
	}
	reporter.Decided(res.Trace(wf))
	reporter.Authority(wf, cfg.Test.Command, cfg.Test.Sandboxed())
	if err := store.SaveRun(run); err != nil {
		return err
	}

	if mode != domain.ModeAuto {
		resolvedMode, approved, err := interaction.ReviewAndSelectMode(prompter, stdout, mode)
		if err != nil {
			return failRun(store, run, err)
		}
		if !approved {
			run.Status = domain.RunStopped
			run.FailureReason = "cancelled by user"
			_ = store.SaveRun(run)
			return fmt.Errorf("run cancelled by user: %w", context.Canceled)
		}

		mode = resolvedMode
		run.Mode = mode
		if err := store.SaveRun(run); err != nil {
			return err
		}

		if mode == domain.ModePlan {
			fmt.Fprintln(stdout, "\nPlan approved. Not executing (plan mode).")
			return nil
		}
	} else {
		run.Mode = domain.ModeAuto
	}

	recordRestorePoint(ctx, repo, run, dirty, reporter)
	// The guard over what the run must never change: the run-start baseline a resume is
	// checked against. The step guard also covers the runs directory's resume inputs,
	// which change with every step and so cannot be part of the baseline.
	protected := append(append([]string(nil), cfg.Security.ProtectedPaths...), configPattern(decideCtx, repo, configPath)...)
	if err := recordBaseline(ctx, repo, run, action.NewRepoGuard(repo, protected)); err != nil {
		return failRun(store, run, err)
	}
	if err := store.SaveRun(run); err != nil {
		return err
	}

	runCtx, cancelRun := engineContext(ctx, decideCtx, cfg.Execution.Timeout, time.Since(started), res.Timings.ManualChoice)
	defer cancelRun()

	runner := testexec.New(osExec)
	if cfg.Test.Sandboxed() {
		if runner.Sandbox, err = testSandbox(ctx, repo, cfg.Test); err != nil {
			return failRun(store, run, err)
		}
	}
	dispatcher := action.New(&cfg, registry, repo, runner)
	dispatcher.Artifacts = store
	dispatcher.Logs = store
	dispatcher.Observer = reporter
	dispatcher.Stream = stream
	runsRel := worktreeRel(decideCtx, repo, runsBaseDir)
	dispatcher.Guard = action.NewRepoGuard(repo, append(protected, action.RunDirPatterns(runsRel)...))
	dispatcher.RunsDir = runsRel

	reporter.MaxFixIterations = cfg.Limits.MaxFixIterations

	engine := &workflow.Engine{
		Executor: dispatcher,
		Observer: &runObserver{reporter: reporter, store: store},
		Persist:  store.SaveRun,
		Limits:   domain.LimitsView{MaxSteps: cfg.Limits.MaxSteps, MaxFixIterations: cfg.Limits.MaxFixIterations},
	}

	if mode == domain.ModeManual {
		gate := interaction.NewTerminalGate(prompter, stdout)
		gate.Changes = repo.StatusSummary
		engine.Gate = gate
	}

	if err := engine.Run(runCtx, run); err != nil {
		return err
	}
	if answer := dispatcher.FinalAnswer(run); answer != "" {
		fmt.Fprintf(stdout, "\nAnswer:\n%s\n", answer)
	}
	return nil
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
		RunID: runID, Logger: store, Observer: logging.CallReporter{ConsoleReporter: reporter}, Stream: stream,
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
