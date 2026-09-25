package action

import (
	"context"
	"encoding/json"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func (d *Dispatcher) executeResearch(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildResearchPrompt(ResearchPromptInput{
		Task:        run.Task.Description,
		FinalAnswer: isFinalReadOnlyStep(run, domain.ActionResearch),
	})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Research.ProviderModel, domain.ActionResearch, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseResearch(result.Output)
	d.lastResearch = &artifact
	path := d.saveArtifact(run, domain.ActionResearch, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		RawStdout:    result.Output,
	}, nil
}

// parseResearch is lenient, matching parseAnalysis - see its comment.
func parseResearch(output string) domain.ResearchArtifact {
	var artifact domain.ResearchArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	return domain.ResearchArtifact{Summary: output}
}

// readOnlyActions are the stages that inspect without changing the repository.
var readOnlyActions = map[domain.ActionType]bool{
	domain.ActionResearch: true,
	domain.ActionAnalyze:  true,
	domain.ActionPlan:     true,
}

// isFinalReadOnlyStep reports whether action is the last read-only step of a workflow
// that changes no code. That step's answer is the reply to the user (TASKS.md В4):
// without development there is nothing else to show for the run.
func isFinalReadOnlyStep(run *domain.Run, action domain.ActionType) bool {
	if run.Workflow == nil {
		return false
	}
	last := domain.ActionType("")
	for _, step := range run.Workflow.Steps {
		if step.Action == domain.ActionImplement {
			return false
		}
		if readOnlyActions[step.Action] {
			last = step.Action
		}
	}
	return last == action
}
