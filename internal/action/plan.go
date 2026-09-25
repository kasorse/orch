package action

import (
	"context"
	"encoding/json"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func (d *Dispatcher) executePlan(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildPlanPrompt(PlanPromptInput{
		Task:        run.Task.Description,
		Research:    d.lastResearch,
		Analysis:    d.lastAnalysis,
		FinalAnswer: isFinalReadOnlyStep(run, domain.ActionPlan),
	})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Plan.ProviderModel, domain.ActionPlan, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parsePlan(result.Output)
	d.lastPlan = &artifact
	path := d.saveArtifact(run, domain.ActionPlan, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		RawStdout:    result.Output,
	}, nil
}

// parsePlan is lenient, matching parseAnalysis - see its comment.
func parsePlan(output string) domain.PlanArtifact {
	var artifact domain.PlanArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	return domain.PlanArtifact{Summary: output}
}
