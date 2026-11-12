package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/classification"
	"github.com/kasorse/orch/internal/config"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/interaction"
	"github.com/kasorse/orch/internal/logging"
	"github.com/kasorse/orch/internal/persistence"
	"github.com/kasorse/orch/internal/policy"
	"github.com/kasorse/orch/internal/projectctx"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/translate"
	"github.com/kasorse/orch/internal/workflow"
)

// decisionOutcome is what run needs from the decision step, whether the decision layer
// produced it or the operator named the stages with --stage.
type decisionOutcome struct {
	decision domain.Decision
	// trace builds decision.json for the built workflow. When the decision step fails it is the
	// only field set (or none is): the trace so far, whose usage stopUndecided counts.
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

// decideManual builds the decision for stages named with --stage. It constructs no translator
// or Context Manager, and the decision stays manual. What it asks of the decision provider is the
// clarity question, on the request as typed (specs/clarify), and, when the task is unclear, the
// same clarify loop as a decided run: the clarification is recorded in run.json beside the
// decision and the stages stay the operator's.
func decideManual(ctx, userCtx context.Context, cfg config.Config, registry *provider.Registry, store *persistence.Store,
	repo *repository.Repository, cwd string, run *domain.Run, stages []domain.Stage, task string, reporter *logging.ConsoleReporter,
	prompter interaction.Prompter, interactive bool, stream, stderr io.Writer) (decisionOutcome, error) {
	dec, err := domain.ManualDecision(stages)
	if err != nil {
		return decisionOutcome{}, &apperr.ConfigError{Msg: err.Error()}
	}
	clarity, err := askClarity(ctx, userCtx, cfg, registry, store, repo, cwd, run, task, reporter, prompter, interactive, stream, stderr)
	trace := func(wf domain.Workflow) domain.DecisionTrace {
		t := domain.ManualTrace(task, dec, wf)
		t.Clarity = clarity.trace
		if !clarity.usage.Empty() {
			u := clarity.usage
			t.Usage = &u
		}
		return t
	}
	if err != nil {
		return decisionOutcome{trace: trace}, err
	}
	return decisionOutcome{
		decision:     dec,
		manualChoice: clarity.operator,
		trace:        trace,
		announce: func(wf domain.Workflow, stderr io.Writer) {
			reporter.ManualStages(dec.Stages.EnabledStages())
			for _, gap := range workflow.Gaps(wf) {
				logging.GapWarning(stderr, gap)
			}
		},
	}, nil
}

// manualClarity is what a --stage run learned about the clarity of its task.
type manualClarity struct {
	// trace is decision.json's clarity; nil when the question was not answered.
	trace *domain.ClarityTrace
	// usage is what the clarity question, the clarify step and the re-checks cost.
	usage domain.DecisionUsage
	// operator is the time the operator spent answering, which is not the run's time.
	operator time.Duration
}

// askClarity asks the decision provider the clarity question alone, on task, and compares P(clear)
// with policy.clarify.threshold. The stages are the operator's, so an unanswered question is a
// warning, not a failed run: the trace then has no clarity. Below the threshold the clarify loop
// runs, and its failures are those of a decided run: a failed clarify call fails the run, a task
// that stays unclear stops it (*classification.NeedsInputError); a failed re-check is a warning,
// and no clarification is recorded. The calls' usage is returned whenever it was reported, a
// failed call's included.
func askClarity(ctx, userCtx context.Context, cfg config.Config, registry *provider.Registry, store *persistence.Store,
	repo *repository.Repository, cwd string, run *domain.Run, task string, reporter *logging.ConsoleReporter,
	prompter interaction.Prompter, interactive bool, stream, stderr io.Writer) (manualClarity, error) {
	var out manualClarity
	decisions, err := newDecisionProvider(cfg, registry, store, run.ID, reporter, "Checking the task", stream, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Warning: the clarity of the task was not checked: %v\n", err)
		return out, nil
	}
	d, err := decisions.Clarity(ctx, task, "")
	if err != nil {
		var derr *apperr.DecisionError
		if errors.As(err, &derr) && derr.Usage != nil {
			out.usage.ClarityRecheck = append(out.usage.ClarityRecheck, *derr.Usage)
		}
		if ctx.Err() != nil || (derr != nil && derr.Kind == apperr.DecisionCancelled) {
			return out, err
		}
		fmt.Fprintf(stderr, "Warning: the clarity of the task was not checked: %v\n", err)
		return out, nil
	}
	if d.Usage != nil {
		out.usage.ClarityRecheck = append(out.usage.ClarityRecheck, *d.Usage)
	}
	pol := cfg.DecisionPolicy()
	trace := policy.Clarity([]float64{d.PClear}, pol)
	out.trace = &trace
	if !cfg.Clarify.Active() {
		trace.Status, trace.Reason = domain.ClarityDisabled, "clarify.enabled is false"
		return out, nil
	}
	if trace.Status != domain.ClarityUnclear {
		return out, nil
	}

	loop := newClarifyLoop(cfg, registry, store, repo, cwd, decisions, reporter, prompter, interactive, stream)
	res, err := loop.Run(ctx, classification.ClarifyInput{RunID: run.ID, Request: task, CheckText: task, First: d.PClear, UserCtx: userCtx})
	out.usage.Clarify = res.Usage.Clarify
	out.usage.ClarityRecheck = append(out.usage.ClarityRecheck, res.Usage.ClarityRecheck...)
	out.operator = res.Operator
	if err != nil {
		return out, err
	}
	updated := policy.Clarity(res.Checks, pol)
	out.trace = &updated
	if res.RecheckErr != nil {
		fmt.Fprintf(stderr, "Warning: the clarity of the task was not checked again after the clarify step: %v\n", res.RecheckErr)
		return out, nil
	}
	run.Task.Clarification = res.Clarification
	if res.NeedsInput {
		return out, &classification.NeedsInputError{Clarification: res.Clarification}
	}
	return out, nil
}

// decideWithProvider is the decision layer: translation, pre-check, project context and
// classification (MVP.md §8-§9). storyTask is run --story-task and revise is orchestrator revise:
// development is then forced on after the policy.
func decideWithProvider(ctx, userCtx context.Context, cfg config.Config, registry *provider.Registry, store *persistence.Store,
	repo *repository.Repository, cwd string, run *domain.Run, text string, reporter *logging.ConsoleReporter,
	prompter interaction.Prompter, interactive, storyTask, revise bool, stream, stderr io.Writer) (decisionOutcome, error) {
	decisions, err := newDecisionProvider(cfg, registry, store, run.ID, reporter, "Deciding the workflow", stream, stderr)
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
	if cfg.Clarify.Active() {
		svc.Clarify = newClarifyLoop(cfg, registry, store, repo, cwd, decisions, reporter, prompter, interactive, stream)
	} else {
		svc.ClarifyDisabled = true
	}

	reporter.Deciding()
	// The manual choice and the clarify step's questions wait on userCtx: Ctrl-C still cancels
	// them, execution.timeout does not.
	res, err := svc.Classify(ctx, classification.Request{RunID: run.ID, Text: text, UserCtx: userCtx, StoryTask: storyTask})
	// Recorded before the error is looked at: a task that stays unclear stops with its clarification.
	run.Task.Clarification = res.Clarification
	if err != nil {
		return decisionOutcome{trace: res.Trace}, err
	}
	// policy.explore may turn on a stage just below its threshold. It is applied to the result
	// itself, so res.Trace (decision.json), the announced table and run.json all record the
	// same decision. Only here: a --stage run never reaches this function and a resume reads
	// the persisted workflow.
	res.Decision = policy.Explore(res.Decision, svc.Policy, run.ID)
	// A story task is a code change by construction (specs/story/SPEC.md §11.2): development,
	// and testing with it, is on whatever the probabilities said, and decision.json says why.
	if storyTask {
		res.Decision = policy.ForceDevelopment(res.Decision, storyTaskReason)
		// The task file is the plan: research and investigation are not run again.
		res.Decision = policy.SkipPlanning(res.Decision, storyTaskPlannedReason)
	}
	// A revision is a code change too, always reviewed over the whole merge request (MVP.md §54.7);
	// research and investigation stay the policy's.
	if revise {
		res.Decision = policy.ForceReview(policy.ForceDevelopment(res.Decision, reviseReason), reviseReason)
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
