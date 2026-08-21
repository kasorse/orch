// Package workflow builds the deterministic, fixed workflow for a classification and
// drives its execution as a state machine. This package must never import
// config/provider/repository - it depends only on domain and the StepExecutor
// interface it defines itself (see engine.go); internal/action is the composition
// point that implements StepExecutor.
package workflow

import (
	"fmt"

	"orchestrator/internal/domain"
)

// Build maps a Classification to its fixed workflow, per the table in MVP.md §9. The
// same classification always produces the same workflow - this is plain Go code, never
// an LLM decision.
func Build(c domain.Classification) (domain.Workflow, error) {
	switch c.Type {
	case domain.TaskQuestion:
		return domain.Workflow{Steps: []domain.Step{{Action: domain.ActionAnswer}}}, nil

	case domain.TaskTest:
		return domain.Workflow{Steps: []domain.Step{{Action: domain.ActionTest}}}, nil

	case domain.TaskReview:
		return domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview}}}, nil

	case domain.TaskBugfix:
		steps := []domain.Step{}
		if c.Complexity == domain.ComplexityMedium || c.Complexity == domain.ComplexityHigh {
			steps = append(steps, domain.Step{Action: domain.ActionAnalyze})
		}
		steps = append(steps, domain.Step{Action: domain.ActionImplement})
		return appendTrailing(steps, c), nil

	case domain.TaskFeature:
		steps := []domain.Step{
			{Action: domain.ActionPlan},
			{Action: domain.ActionImplement},
		}
		return appendTrailing(steps, c), nil

	case domain.TaskRefactor:
		steps := []domain.Step{
			{Action: domain.ActionAnalyze},
			{Action: domain.ActionImplement},
		}
		return appendTrailing(steps, c), nil

	default:
		return domain.Workflow{}, fmt.Errorf("no workflow defined for task type %q", c.Type)
	}
}

func appendTrailing(steps []domain.Step, c domain.Classification) domain.Workflow {
	if c.RequiresTests {
		steps = append(steps, domain.Step{Action: domain.ActionTest})
	}
	if c.RequiresReview {
		steps = append(steps, domain.Step{Action: domain.ActionReview})
	}
	return domain.Workflow{Steps: steps}
}
