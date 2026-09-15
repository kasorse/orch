// Package app wires config, provider, classifier, repository, testexec, action,
// persistence, and workflow together into the orchestrator's three CLI commands
// (version, validate, run). This is the composition root - no other package
// constructs the full dependency graph.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"orchestrator/internal/action"
	"orchestrator/internal/apperr"
	"orchestrator/internal/classifier"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/execution"
	"orchestrator/internal/interaction"
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

// Run parses args and dispatches to the requested subcommand. It never calls
// os.Exit - main.go is the sole exit point, mapping the returned error via
// exitcode.FromError.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	configPath, mode, rest, err := parseArgs(args)
	if err != nil {
		return &apperr.ConfigError{Msg: err.Error()}
	}
	if len(rest) == 0 {
		return &apperr.ConfigError{Msg: `usage: orchestrator [--config path] [--mode plan|manual|accept-edits|auto] <version|validate|run "<task>">`}
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
		return runTask(ctx, configPath, strings.Join(rest[1:], " "), mode, stdin, stdout, stderr)
	default:
		return &apperr.ConfigError{Msg: fmt.Sprintf("unknown command %q", rest[0])}
	}
}

// parseArgs pulls --config/--config=X and --mode/--mode=X out of args (in any
// position) and returns the resolved config path, resolved mode (empty if --mode was
// not given - the caller then runs the interactive mode-select prompt), and the
// remaining positional arguments.
func parseArgs(args []string) (configPath string, mode domain.RunMode, rest []string, err error) {
	configPath = defaultConfigPath
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--config":
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("--config requires a value")
			}
			configPath = args[i+1]
			i++
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "--mode":
			if i+1 >= len(args) {
				return "", "", nil, fmt.Errorf("--mode requires a value")
			}
			if mode, err = domain.ParseRunMode(args[i+1]); err != nil {
				return "", "", nil, err
			}
			i++
		case strings.HasPrefix(arg, "--mode="):
			if mode, err = domain.ParseRunMode(strings.TrimPrefix(arg, "--mode=")); err != nil {
				return "", "", nil, err
			}
		default:
			rest = append(rest, arg)
		}
	}
	return configPath, mode, rest, nil
}

func loadAndValidateConfig(configPath string, reporter *logging.ConsoleReporter) (config.Config, error) {
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

	reporter.ConfigValid()
	return cfg, nil
}

func runValidate(configPath string, stdout io.Writer) error {
	_, err := loadAndValidateConfig(configPath, logging.NewConsoleReporter(stdout))
	return err
}

func runTask(ctx context.Context, configPath, taskDescription string, mode domain.RunMode, stdin io.Reader, stdout, stderr io.Writer) error {
	reporter := logging.NewConsoleReporter(stdout)

	cfg, err := loadAndValidateConfig(configPath, reporter)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return &apperr.RepositoryError{Msg: "failed to determine working directory", Err: err}
	}

	osExec := execution.NewOSProcessExecutor()

	repo, err := repository.Open(ctx, cwd, osExec)
	if err != nil {
		return err
	}
	if dirty, err := repo.IsDirty(ctx); err != nil {
		return err
	} else if dirty {
		logging.DirtyRepoWarning(stderr)
	}

	registry := provider.NewRegistry()
	registry.Register(provider.Claude, claude.New(osExec))
	registry.Register(provider.Codex, codex.New(osExec))
	registry.Register(provider.Gemini, gemini.New(osExec))

	classifierClient, err := registry.Resolve(cfg.Classifier.Provider)
	if err != nil {
		return &apperr.WorkflowError{Msg: "failed to resolve classifier provider", Err: err}
	}
	clf := classifier.New(classifierClient, cfg.Classifier.Model)

	store := persistence.NewStore(runsBaseDir)

	task := domain.Task{Description: taskDescription, CreatedAt: time.Now()}
	run, err := store.NewRun(task)
	if err != nil {
		return err
	}

	reporter.ClassifyingTask()
	classification, err := clf.Classify(ctx, task.Description)
	if err != nil {
		run.Status = domain.RunFailed
		run.FailureReason = err.Error()
		_ = store.SaveRun(run)
		return err
	}
	run.Classification = &classification
	reporter.Classified(classification)
	if err := store.SaveRun(run); err != nil {
		return err
	}

	wf, err := workflow.Build(classification)
	if err != nil {
		run.Status = domain.RunFailed
		run.FailureReason = err.Error()
		_ = store.SaveRun(run)
		return err
	}
	run.Workflow = &wf
	reporter.WorkflowBuilt(wf)
	if err := store.SaveRun(run); err != nil {
		return err
	}

	var prompter interaction.Prompter
	if mode != domain.ModeAuto {
		termPrompter := interaction.NewTerminalPrompter(stdin, stdout)
		prompter = termPrompter

		resolvedMode, approved, err := interaction.ReviewAndSelectMode(termPrompter, stdout, run, mode)
		if err != nil {
			run.Status = domain.RunFailed
			run.FailureReason = err.Error()
			_ = store.SaveRun(run)
			return err
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

	dispatcher := action.New(&cfg, registry, repo, testexec.New(osExec))
	dispatcher.Artifacts = store

	reporter.MaxFixIterations = cfg.Limits.MaxFixIterations

	engine := &workflow.Engine{
		Executor: dispatcher,
		Observer: &runObserver{reporter: reporter, store: store},
		Persist:  store.SaveRun,
		Limits:   domain.LimitsView{MaxSteps: cfg.Limits.MaxSteps, MaxFixIterations: cfg.Limits.MaxFixIterations},
	}

	if mode == domain.ModeManual {
		engine.Gate = interaction.NewTerminalGate(prompter, stdout)
	}

	runCtx, cancel := context.WithTimeout(ctx, cfg.Execution.Timeout)
	defer cancel()

	return engine.Run(runCtx, run)
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
