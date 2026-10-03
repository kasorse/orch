package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/classification"
	"orchestrator/internal/config"
	"orchestrator/internal/domain"
	"orchestrator/internal/interaction"
	"orchestrator/internal/logging"
	"orchestrator/internal/persistence"
	"orchestrator/internal/projectctx"
	"orchestrator/internal/provider"
	"orchestrator/internal/repository"
	"orchestrator/internal/translate"
	"orchestrator/internal/workflow"
)

// decisionOutcome is what run needs from the decision step, whether the decision layer
// produced it or the operator named the stages with --stage.
type decisionOutcome struct {
	decision domain.Decision
	// trace builds decision.json for the built workflow.
	trace func(domain.Workflow) domain.DecisionTrace
	// project is what was sent to the decision provider; nil when nothing was.
	project *domain.ProjectContext
	// manualChoice is the time the operator spent on a manual choice, which does not
	// count against execution.timeout.
	manualChoice time.Duration
	// announce prints the decision once the worktree rule has passed.
	announce func(wf domain.Workflow, stderr io.Writer)
}

// parseStageFlags turns the values of the --stage flags into stages. One stage per flag:
// a comma is an error, not a list (specs/stage-select/SPEC.md §4).
func parseStageFlags(values []string) ([]domain.Stage, error) {
	var out []domain.Stage
	for _, v := range values {
		if strings.Contains(v, ",") {
			return nil, fmt.Errorf("--stage takes one stage per flag, got %q (repeat --stage instead)", v)
		}
		st, err := domain.ParseStageFlag(v)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// decideManual builds the decision for stages named with --stage. It constructs no
// decision provider, translator or Context Manager and calls nothing.
func decideManual(stages []domain.Stage, task string, reporter *logging.ConsoleReporter) (decisionOutcome, error) {
	dec, err := domain.ManualDecision(stages)
	if err != nil {
		return decisionOutcome{}, &apperr.ConfigError{Msg: err.Error()}
	}
	return decisionOutcome{
		decision: dec,
		trace: func(wf domain.Workflow) domain.DecisionTrace {
			return domain.ManualTrace(task, dec, wf)
		},
		announce: func(wf domain.Workflow, stderr io.Writer) {
			reporter.ManualStages(dec.Stages.EnabledStages())
			for _, gap := range workflow.Gaps(wf) {
				logging.GapWarning(stderr, gap)
			}
		},
	}, nil
}

// decideWithProvider is the decision layer: translation, pre-check, project context and
// classification (MVP.md §8-§9).
func decideWithProvider(ctx, userCtx context.Context, cfg config.Config, registry *provider.Registry, store *persistence.Store,
	repo *repository.Repository, cwd string, run *domain.Run, text string, reporter *logging.ConsoleReporter,
	prompter interaction.Prompter, interactive bool, stream, stderr io.Writer) (decisionOutcome, error) {
	decisions, err := newDecisionProvider(cfg, registry, store, run.ID, reporter, stream, stderr)
	if err != nil {
		return decisionOutcome{}, err
	}
	translatorClient, err := registry.Resolve(cfg.Translation.Provider)
	if err != nil {
		return decisionOutcome{}, &apperr.WorkflowError{Msg: "failed to resolve translation provider", Err: err}
	}

	svc := &classification.Service{
		Translator: &translate.Translator{
			Client: translatorClient, ProviderID: cfg.Translation.Provider, Model: cfg.Translation.Model,
			Timeout: cfg.Translation.Timeout, Logger: store,
			Observer: logging.CallReporter{ConsoleReporter: reporter, Label: "Translating"}, Stream: stream,
		},
		Decisions: decisions,
		Context: projectctx.NewManager(repo, cwd, projectctx.Options{
			MaxFiles: cfg.Context.MaxFiles, MaxBytes: cfg.Context.MaxBytes, ExtraDeny: cfg.Context.ExtraDeny,
		}),
		Chooser:      interaction.NewChooser(prompter, reporter.PausingWriter(), interactive),
		Policy:       cfg.DecisionPolicy(),
		Primary:      domain.ComplexitySource(cfg.Decision.Complexity.Primary),
		AskOnFailure: cfg.Decision.OnFailure == config.OnFailureAsk,
	}

	reporter.Deciding()
	// The manual choice waits on userCtx: Ctrl-C still cancels it, execution.timeout does not.
	res, err := svc.Classify(ctx, classification.Request{RunID: run.ID, Text: text, UserCtx: userCtx})
	if err != nil {
		return decisionOutcome{}, err
	}
	return decisionOutcome{
		decision:     res.Decision,
		trace:        res.Trace,
		project:      res.Project,
		manualChoice: res.Timings.ManualChoice,
		announce: func(wf domain.Workflow, _ io.Writer) {
			reporter.Decided(res.Trace(wf))
		},
	}, nil
}
