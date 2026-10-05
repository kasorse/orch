package action

import (
	"context"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// executeTest makes no AI call - it runs the real test command and trusts only its exit
// code (MVP.md §18/§31.4).
//
// The test command runs code a model may have written, so the same guard as implement
// checks what it changed: a test that plants a hook, CI file or agent setting stops the
// run like an implement that did (specs/permissions/SPEC.md §7.3).
func (d *Dispatcher) executeTest(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	result, err := d.guardedTest(ctx, run, domain.ActionTest)
	if err != nil {
		if result == nil {
			return workflow.StepOutcome{}, err
		}
		return workflow.StepOutcome{TestResult: result, RawStdout: result.Stdout, RawStderr: result.Stderr}, err
	}

	d.applyTest(*result)
	path := d.saveArtifact(run, domain.ActionTest, *result)

	outcome := workflow.OutcomeSuccess
	if !result.Success {
		outcome = workflow.OutcomeFailure
	}

	return workflow.StepOutcome{
		Outcome:      outcome,
		ArtifactPath: path,
		TestResult:   result,
		RawStdout:    result.Stdout,
		RawStderr:    result.Stderr,
	}, nil
}

// PreRunTest runs the test command once before the first step of a workflow that
// implements and tests (specs/hardening/SPEC.md §8), under the same runner, sandbox and
// guard as the test step. It is not a step: nothing is recorded in the run's executions and
// no artifact is saved. A red result is remembered for the first implement prompt, which
// only matters when the run goes on anyway (run --allow-red-tests); the caller decides that.
// The result is returned with a guard violation too, so its output can be logged.
func (d *Dispatcher) PreRunTest(ctx context.Context, run *domain.Run) (*domain.TestResult, error) {
	result, err := d.guardedTest(ctx, run, preRunTestStep)
	if err != nil {
		return result, err
	}
	d.applyPreRunTest(*result)
	return result, nil
}

// preRunTestStep names the pre-run test in a guard violation.
const preRunTestStep domain.ActionType = "pre-run test"

// guardedTest runs the test command between a guard snapshot and the comparison after it.
// A nil result means the command never ran.
func (d *Dispatcher) guardedTest(ctx context.Context, run *domain.Run, step domain.ActionType) (*domain.TestResult, error) {
	if d.Guard == nil {
		return nil, &apperr.WorkflowError{Msg: "running the test command requires a guard over the repository, and none is configured"}
	}
	before, err := d.Guard.Snapshot(ctx)
	if err != nil {
		return nil, &apperr.WorkflowError{Msg: "failed to snapshot protected paths before the test command", Err: err}
	}

	result, err := d.Runner.Run(ctx, d.Config.Test.Command, d.Repo.Dir)
	if err != nil {
		return nil, err
	}

	changed, err := d.Guard.Changed(ctx, before)
	if err != nil {
		return &result, &apperr.WorkflowError{Msg: "failed to check protected paths after the test command", Err: err}
	}
	if len(changed) > 0 {
		return &result, &apperr.WorkflowError{Msg: protectedViolation(step, changed, run.PreRunRef)}
	}
	return &result, nil
}
