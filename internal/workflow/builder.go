// Package workflow builds the deterministic, fixed workflow for a decision and
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
