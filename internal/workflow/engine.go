package workflow

import (
	"context"
	"time"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
)

// StepOutcome is what a StepExecutor reports back after running one action.
type StepOutcome struct {
	Outcome      Outcome
	Provider     *domain.ProviderRef
	ArtifactPath string
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

type Engine struct {
	Executor StepExecutor
	Observer Observer
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

	current := State(run.Workflow.Steps[0].Action)
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
		e.stepStarted(run, index, action)

		stepExec := domain.StepExecution{
			Index:     index,
			Action:    action,
			Status:    domain.StepRunning,
			StartedAt: time.Now(),
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

		next := Next(current, outcome.Outcome, *run.Workflow)

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
