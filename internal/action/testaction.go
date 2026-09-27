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
	if d.Guard == nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "the test step requires a guard over the repository, and none is configured"}
	}
	before, err := d.Guard.Snapshot(ctx)
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to snapshot protected paths before the test command", Err: err}
	}

	result, err := d.Runner.Run(ctx, d.Config.Test.Command, d.Repo.Dir)
	if err != nil {
		return workflow.StepOutcome{}, err
	}

	changed, err := d.Guard.Changed(ctx, before)
	if err != nil {
		return workflow.StepOutcome{TestResult: &result, RawStdout: result.Stdout, RawStderr: result.Stderr},
			&apperr.WorkflowError{Msg: "failed to check protected paths after the test command", Err: err}
	}
	if len(changed) > 0 {
		return workflow.StepOutcome{TestResult: &result, RawStdout: result.Stdout, RawStderr: result.Stderr},
			&apperr.WorkflowError{Msg: protectedViolation(domain.ActionTest, changed, run.PreRunRef)}
	}

	d.lastTest = &result
	path := d.saveArtifact(run, domain.ActionTest, result)

	outcome := workflow.OutcomeSuccess
	if result.Success {
		d.pendingTestFailure = nil
	} else {
		outcome = workflow.OutcomeFailure
		d.pendingTestFailure = &result
	}

	return workflow.StepOutcome{
		Outcome:      outcome,
		ArtifactPath: path,
		TestResult:   &result,
		RawStdout:    result.Stdout,
		RawStderr:    result.Stderr,
	}, nil
}
