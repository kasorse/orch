package action

import (
	"context"
	"encoding/json"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func (d *Dispatcher) executeAnswer(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildAnswerPrompt(AnswerPromptInput{Task: run.Task.Description})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Answer, domain.ActionAnswer, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseAnswer(result.Output)
	path := d.saveArtifact(run, domain.ActionAnswer, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		RawStdout:    result.Output,
	}, nil
}

// parseAnswer is lenient, matching parseAnalysis - see its comment.
func parseAnswer(output string) domain.AnswerArtifact {
	var artifact domain.AnswerArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	return domain.AnswerArtifact{Answer: output}
}
