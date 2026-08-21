package action

import (
	"context"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// executeTest makes no AI call - it runs the real test command and trusts only its exit
// code (MVP.md §18/§31.4).
func (d *Dispatcher) executeTest(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	result, err := d.Runner.Run(ctx, d.Config.Test.Command, d.Repo.Dir)
	if err != nil {
		return workflow.StepOutcome{}, err
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
