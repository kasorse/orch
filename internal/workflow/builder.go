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

// Build maps a stage selection to its workflow (specs/JEV integration/SPEC.md §13,
// TASKS.md T04): it filters domain.StageOrder() down to sel's enabled stages and maps
// each to the action that executes it. It walks StageOrder itself rather than trusting
// sel.Stages' own order, so the result does not depend on the order the caller built
// sel in - stage order is fixed, deterministic code, never something a decision
// provider or a caller controls (SPEC.md §23). The same selection always produces the
// same workflow.
func Build(sel domain.StageSelection) (domain.Workflow, error) {
	enabled := make(map[domain.Stage]bool, len(sel.Stages))
	for _, st := range sel.EnabledStages() {
		enabled[st] = true
	}

	var steps []domain.Step
	for _, st := range domain.StageOrder() {
		if !enabled[st] {
			continue
		}
		action, ok := st.Action()
		if !ok {
			return domain.Workflow{}, fmt.Errorf("stage %q has no action mapping", st)
		}
		steps = append(steps, domain.Step{Action: action, Stage: st})
	}

	if len(steps) == 0 {
		return domain.Workflow{}, fmt.Errorf("stage selection enables no stages")
	}

	return domain.Workflow{Steps: steps}, nil
}

// BuildLegacy maps a Classification to its fixed workflow, per the table in the root
// MVP.md §9 - the pre-Jev classifier flow (internal/classifier, domain.TaskType,
// domain.LegacyComplexity). It is kept only until that flow is removed
// (specs/JEV integration/TASKS.md T12); new code calls Build. The same classification
// always produces the same workflow - this is plain Go code, never an LLM decision.
func BuildLegacy(c domain.Classification) (domain.Workflow, error) {
	switch c.Type {
	case domain.TaskQuestion:
		return domain.Workflow{Steps: []domain.Step{{Action: domain.ActionAnswer}}}, nil

	case domain.TaskTest:
		return domain.Workflow{Steps: []domain.Step{{Action: domain.ActionTest}}}, nil

	case domain.TaskReview:
		return domain.Workflow{Steps: []domain.Step{{Action: domain.ActionReview}}}, nil

	case domain.TaskBugfix:
		steps := []domain.Step{}
		if c.Complexity == domain.LegacyComplexityMedium || c.Complexity == domain.LegacyComplexityHigh {
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
