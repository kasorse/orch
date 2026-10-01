package workflow

import (
	"context"
	"fmt"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// StepOutcome is what a StepExecutor reports back after running one action.
type StepOutcome struct {
	Outcome      Outcome
	Provider     *domain.ProviderRef
	ArtifactPath string
	// Artifact is the structured result saved at ArtifactPath (research, analyze, plan,
	// implement), for display; test and review carry theirs in TestResult/ReviewResult.
	Artifact     any
	TestResult   *domain.TestResult
	ReviewResult *domain.ReviewResult
	RawStdout    string
	RawStderr    string
}

// StepExecutor actually runs one action. A non-nil error means a hard failure (e.g. a
// provider error, or a malformed strictly-parsed response) that ends the run
// immediately - it is not routed through the Outcome/Next transition table, since
// there is nothing actionable to retry on.
type StepExecutor interface {
	Execute(ctx context.Context, action domain.ActionType, run *domain.Run) (StepOutcome, error)
}

// Observer receives CLI-facing progress notifications. All methods are optional -
// Engine tolerates a nil Observer.
type Observer interface {
	StepStarted(run *domain.Run, index int, action domain.ActionType)
	StepFinished(run *domain.Run, index int, exec domain.StepExecution, outcome StepOutcome)
	RunFinished(run *domain.Run)
}

// PersistFunc is invoked after every recorded step (and at run completion) so a run
// can be inspected mid-flight or after the fact.
type PersistFunc func(run *domain.Run) error

// GateDecision is Gate.BeforeStep's verdict on whether the engine may proceed.
type GateDecision string

const (
	GateProceed GateDecision = "proceed"
	GateAbort   GateDecision = "abort"
)

// Gate optionally pauses the engine before each step, e.g. for manual-mode
// per-step confirmation. Engine tolerates a nil Gate, which reproduces today's
// fully-automatic behavior exactly. A Gate implementation may mutate run.Workflow
// before returning GateProceed - such a mutation only affects future Next() lookups,
// never the step about to execute.
type Gate interface {
	BeforeStep(ctx context.Context, run *domain.Run, index int, action domain.ActionType) (GateDecision, error)
}

type Engine struct {
	Executor StepExecutor
	Observer Observer
	Gate     Gate
	Persist  PersistFunc
	Limits   domain.LimitsView
}

// Run drives the workflow state machine to completion, failure, or a limit being hit.
// Execution history is append-only: run.Executions grows with every attempt, including
// retries, never overwriting a previous entry.
func (e *Engine) Run(ctx context.Context, run *domain.Run) error {
	if run.Workflow == nil || len(run.Workflow.Steps) == 0 {
		return &apperr.WorkflowError{Msg: "workflow has no steps"}
	}

	// A fresh run enters at its first step; a resumed one where its history left off
	// (specs/resume/SPEC.md §7). Counters and limits then count the whole history.
	current, _, err := Entry(run)
	if err != nil {
		return &apperr.WorkflowError{Msg: "cannot continue the run", Err: err}
	}
	if len(run.Executions) > 0 {
		reason, _, _, _, err := limitReached(run, e.Limits, current)
		if err != nil {
			return &apperr.WorkflowError{Msg: "cannot count the run's history", Err: err}
		}
		run.TotalSteps, run.FixIterations, _ = Counters(run)
		if reason != "" {
			run.Status = domain.RunStopped
			run.FailureReason = reason
			e.persist(run)
			e.runFinished(run)
			return CheckLimits(run, e.Limits, current)
		}
	}
	run.Status = domain.RunRunning

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			run.Status = domain.RunStopped
			run.FailureReason = "execution cancelled"
			e.persist(run)
			e.runFinished(run)
			return ctxErr
		}

		if current == StateDone {
			run.Status = domain.RunCompleted
			e.persist(run)
			e.runFinished(run)
			return nil
		}

		if current == StateFailed {
			run.Status = domain.RunFailed
			e.persist(run)
			e.runFinished(run)
			return &apperr.WorkflowError{Msg: run.FailureReason}
		}

		if run.TotalSteps >= e.Limits.MaxSteps {
			run.Status = domain.RunStopped
			run.FailureReason = "maximum steps exceeded"
			e.persist(run)
			e.runFinished(run)
			return &apperr.LimitExceededError{Reason: "Run status: stopped\nReason: maximum steps exceeded"}
		}

		action := domain.ActionType(current)
		index := len(run.Executions)

		if e.Gate != nil {
			decision, err := e.Gate.BeforeStep(ctx, run, index, action)
			if err != nil {
				run.Status = domain.RunFailed
				run.FailureReason = err.Error()
				e.persist(run)
				e.runFinished(run)
				return err
			}
			if decision == GateAbort {
				run.Status = domain.RunStopped
				run.FailureReason = "aborted by user"
				e.persist(run)
				e.runFinished(run)
				return fmt.Errorf("run aborted by user: %w", context.Canceled)
			}
		}

		e.stepStarted(run, index, action)

		stepExec := domain.StepExecution{
			Index:     index,
			Action:    action,
			Status:    domain.StepRunning,
			StartedAt: time.Now(),
			Attempt:   len(run.Resumes),
		}

		outcome, err := e.Executor.Execute(ctx, action, run)

		stepExec.FinishedAt = time.Now()
		stepExec.ArtifactPath = outcome.ArtifactPath
		stepExec.TestResult = outcome.TestResult
		stepExec.ReviewResult = outcome.ReviewResult
		if outcome.Provider != nil {
			stepExec.Provider = outcome.Provider
		}

		if err != nil {
			stepExec.Status = domain.StepFailed
			stepExec.Error = err.Error()
			run.Executions = append(run.Executions, stepExec)
			run.TotalSteps++
			run.Status = domain.RunFailed
			run.FailureReason = err.Error()
			e.stepFinished(run, index, stepExec, outcome)
			e.persist(run)
			e.runFinished(run)
			return err
		}

		stepExec.Status = domain.StepCompleted
		run.Executions = append(run.Executions, stepExec)
		run.TotalSteps++
		e.stepFinished(run, index, stepExec, outcome)
		e.persist(run)

		next, reason := Next(current, outcome.Outcome, *run.Workflow)
		if next == StateFailed {
			run.FailureReason = reason
		}

		if isBackwardEntryToImplement(current, next) {
			run.FixIterations++
			if run.FixIterations > e.Limits.MaxFixIterations {
				run.Status = domain.RunStopped
				run.FailureReason = "maximum fix iterations exceeded"
				e.persist(run)
				e.runFinished(run)
				return &apperr.LimitExceededError{Reason: "Run status: stopped\nReason: maximum fix iterations exceeded"}
			}
		}

		current = next
	}
}

func (e *Engine) persist(run *domain.Run) {
	if e.Persist == nil {
		return
	}
	_ = e.Persist(run)
}

func (e *Engine) stepStarted(run *domain.Run, index int, action domain.ActionType) {
	if e.Observer == nil {
		return
	}
	e.Observer.StepStarted(run, index, action)
}

func (e *Engine) stepFinished(run *domain.Run, index int, exec domain.StepExecution, outcome StepOutcome) {
	if e.Observer == nil {
		return
	}
	e.Observer.StepFinished(run, index, exec, outcome)
}

func (e *Engine) runFinished(run *domain.Run) {
	if e.Observer == nil {
		return
	}
	e.Observer.RunFinished(run)
}
