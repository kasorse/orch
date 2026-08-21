package action

import (
	"context"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// Execute implements workflow.StepExecutor.
func (d *Dispatcher) Execute(ctx context.Context, actionType domain.ActionType, run *domain.Run) (workflow.StepOutcome, error) {
	switch actionType {
	case domain.ActionAnalyze:
		return d.executeAnalyze(ctx, run)
	case domain.ActionPlan:
		return d.executePlan(ctx, run)
	case domain.ActionImplement:
		return d.executeImplement(ctx, run)
	case domain.ActionTest:
		return d.executeTest(ctx, run)
	case domain.ActionReview:
		return d.executeReview(ctx, run)
	case domain.ActionAnswer:
		return d.executeAnswer(ctx, run)
	default:
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "no action handler for " + string(actionType)}
	}
}
