package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
)

// StepOutcome is what a StepExecutor reports back after running one action.
type StepOutcome struct {
	Outcome      Outcome
	Provider     *domain.ProviderRef
	ArtifactPath string
	// Artifact is the structured result saved at ArtifactPath (research, plan,
	// implement), for display; test and review carry theirs in TestResult/ReviewResult.
	Artifact     any
	TestResult   *domain.TestResult
	ReviewResult *domain.ReviewResult
	RawStdout    string
	RawStderr    string
	// CheckLogs is the raw output of each check a test step ran, for logs/<NNN>-test.<check>.*;
	// RawStdout/RawStderr stay the failing check's or the test command's.
	CheckLogs []domain.CheckOutput
	// VerifyCalls are the verify tool calls an implement step's session made, recorded also
	// when the step failed; VerifyLogs is their raw output, for logs/<NNN>-implement.verify-<K>.*
	// (the Name of each entry is verify-<K>). Only VerifyCalls is persisted in run.json.
	VerifyCalls []domain.VerifyCall
	VerifyLogs  []domain.CheckOutput
	// Usage is what the step's provider call cost, when the CLI reported it - also on a
	// failed call, so it is recorded whatever the outcome.
	Usage *domain.Usage
	// Call is the step's provider call beyond its cost (prompt, models, tools), also on a
	// failed call.
	Call *domain.CallReport
	// Diff is what a completed implement changed, from git.
	Diff *domain.StepDiff
	// PathRules are the policy.paths rules the run's net diff matched after an implement,
	// recorded also when the step failed because of them.
	PathRules []domain.PathRuleMatch
	// TreeBefore and TreeAfter are the git trees of the working tree around a completed
	// implement, for showing its diff. TreeAfter is also recorded, as the execution's
	// tree_after, for the no-progress check; TreeBefore is not.
	TreeBefore, TreeAfter string
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
	// DecisionCalls is the usage of the calls made before the workflow that limits.max_cost_usd
	// counts with the steps: the clarify step's and the clarity re-checks' (specs/clarify).
	DecisionCalls []domain.Usage
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
	current, _, err := Entry(run, e.Limits)
	if err != nil {
		return &apperr.WorkflowError{Msg: "cannot continue the run", Err: err}
	}
	if len(run.Executions) > 0 {
		reason, _, _, _, err := limitReached(run, e.Limits, current)
		if err != nil {
			return &apperr.WorkflowError{Msg: "cannot count the run's history", Err: err}
		}
		run.TotalSteps, run.FixIterations, _ = Counters(run, e.Limits)
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

		// The budget is read before the gate, so the user is not asked to approve a step that
		// will not run. The sum comes from the persisted executions, so a resume counts it all.
		if reason := budgetReason(run, e.DecisionCalls, e.Limits); reason != "" {
			run.Status = domain.RunStopped
			run.FailureReason = reason
			e.persist(run)
			e.runFinished(run)
			return &apperr.LimitExceededError{Reason: "Run status: stopped\nReason: " + reason +
				"\n\nRaise limits.max_cost_usd to continue it."}
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
		stepExec.Usage = outcome.Usage
		stepExec.Call = outcome.Call
		stepExec.Diff = outcome.Diff
		stepExec.VerifyCalls = outcome.VerifyCalls
		stepExec.PathRules = outcome.PathRules
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
		if action == domain.ActionImplement {
			stepExec.TreeAfter = outcome.TreeAfter
		}
		run.Executions = append(run.Executions, stepExec)
		run.TotalSteps++

		// A review's outcome comes from its persisted result with the same function a resume
		// uses, so the two agree on a fixable review (specs/quality/SPEC.md §7.3). The observer
		// gets it too, so what it prints matches where the run goes next. A review without a
		// result keeps what the executor said. A history outcomeOf cannot read ends the run, as
		// it ends a resume (Entry, Counters): continuing on a guess would let the two disagree.
		if action == domain.ActionReview && stepExec.ReviewResult != nil {
			derived, err := outcomeOf(stepExec, run, e.Limits)
			if err != nil {
				err = &apperr.WorkflowError{Msg: "cannot derive where to continue", Err: err}
				run.Status = domain.RunFailed
				run.FailureReason = err.Error()
				e.stepFinished(run, index, stepExec, outcome)
				e.persist(run)
				e.runFinished(run)
				return err
			}
			outcome.Outcome = derived
		}
		e.stepFinished(run, index, stepExec, outcome)
		e.persist(run)

		// An implement that left the tree as the previous one did is caught here, before the
		// test step is spent on it. Only a finished implement is judged: Entry never calls
		// NoProgress, so the implement a resume enters is always run.
		if action == domain.ActionImplement && e.Limits.StopWithoutProgress {
			if reason := NoProgress(run); reason != "" {
				return e.stopWithoutProgress(run, reason)
			}
		}

		next, reason := Next(current, outcome.Outcome, *run.Workflow)
		if next == StateFailed {
			run.FailureReason = reason
		}

		if isBackwardEntryToImplement(current, next) {
			run.FixIterations++
			// max_fix_iterations is the hard limit and is read first, so a run that exhausted it
			// keeps the reason it always had; the progress check only ends a loop earlier.
			if run.FixIterations > e.Limits.MaxFixIterations {
				run.Status = domain.RunStopped
				run.FailureReason = "maximum fix iterations exceeded"
				e.persist(run)
				e.runFinished(run)
				return &apperr.LimitExceededError{Reason: "Run status: stopped\nReason: maximum fix iterations exceeded"}
			}
			if e.Limits.StopWithoutProgress {
				if reason := NoProgress(run); reason != "" {
					return e.stopWithoutProgress(run, reason)
				}
			}
		}

		current = next
	}
}

// spentUSD sums the cost the run's steps and the decision calls the budget counts reported, and
// counts the calls that ended before the CLI's final report: those record no cost, so the sum is
// then only a lower bound. An extra reviewer's calls are already in its step's usage.
func spentUSD(run *domain.Run, decisionCalls []domain.Usage) (spent float64, partial int) {
	add := func(u domain.Usage) {
		spent += u.CostUSD
		if u.Partial {
			partial++
		}
	}
	for _, u := range decisionCalls {
		add(u)
	}
	for _, exec := range run.Executions {
		if exec.Usage != nil {
			add(*exec.Usage)
		}
	}
	return spent, partial
}

// budgetReason is the failure reason of a run whose steps and counted decision calls have cost
// limits.MaxCostUSD or more, "" while it is under budget or has none (0).
func budgetReason(run *domain.Run, decisionCalls []domain.Usage, limits domain.LimitsView) string {
	if limits.MaxCostUSD <= 0 {
		return ""
	}
	spent, partial := spentUSD(run, decisionCalls)
	if spent < limits.MaxCostUSD {
		return ""
	}
	reason := fmt.Sprintf("budget exceeded: $%.2f of $%.2f", spent, limits.MaxCostUSD)
	if partial > 0 {
		reason += fmt.Sprintf(" (lower bound: %d calls without a final report)", partial)
	}
	return reason
}

// stopWithoutProgress ends the run as stopped with the reason NoProgress gave; a resume
// continues it in implement (Entry).
func (e *Engine) stopWithoutProgress(run *domain.Run, reason string) error {
	run.Status = domain.RunStopped
	run.FailureReason = reason
	e.persist(run)
	e.runFinished(run)
	return &apperr.LimitExceededError{Reason: "Run status: stopped\nReason: " + reason +
		"\n\nSet limits.stop_without_progress: false to turn this check off."}
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
