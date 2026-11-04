package action

import (
	"context"
	"encoding/json"

	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/workflow"
)

func (d *Dispatcher) executeResearch(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildResearchPrompt(ResearchPromptInput{
		Task:          run.Task.Description,
		Clarification: run.Task.Clarification.Render(),
		FinalAnswer:   isFinalReadOnlyStep(run, domain.ActionResearch),
	})

	pm, source := d.route(run, domain.ActionResearch)
	result, ref, err := d.call(ctx, run, pm, source, domain.ActionResearch, prompt, provider.Extensions{})
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseResearch(result.Output)
	d.applyResearch(artifact)
	path := d.saveArtifact(run, domain.ActionResearch, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		Artifact:     artifact,
		RawStdout:    result.Output,
	}, nil
}

// parseResearch is lenient, matching parseInvestigation - see its comment.
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
	domain.ActionResearch:    true,
	domain.ActionInvestigate: true,
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

// FinalAnswer returns the reply of a workflow without development (TASKS.md В4): the
// answer of its last read-only step, or "" when there is none.
func (d *Dispatcher) FinalAnswer(run *domain.Run) string {
	if run.Workflow == nil {
		return ""
	}
	for i := len(run.Workflow.Steps) - 1; i >= 0; i-- {
		switch run.Workflow.Steps[i].Action {
		case domain.ActionImplement:
			return ""
		case domain.ActionInvestigate:
			if d.lastInvestigation != nil {
				return d.lastInvestigation.Answer
			}
			return ""
		case domain.ActionResearch:
			if d.lastResearch != nil {
				return d.lastResearch.Answer
			}
			return ""
		}
	}
	return ""
}
