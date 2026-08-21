package action

import (
	"context"
	"encoding/json"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

func (d *Dispatcher) executeAnalyze(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildAnalyzePrompt(AnalyzePromptInput{Task: run.Task.Description})

	result, ref, err := d.call(ctx, d.Config.Actions.Analyze, domain.ActionAnalyze, prompt)
	if err != nil {
		return workflow.StepOutcome{}, err
	}

	artifact := parseAnalysis(result.Output)
	d.lastAnalysis = &artifact
	path := d.saveArtifact(run, domain.ActionAnalyze, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		RawStdout:    result.Output,
	}, nil
}

// parseAnalysis is lenient: correctness of the analysis itself is judged by the
// downstream test/review steps, not by strict schema validation here, so a malformed or
// prose response is not a hard failure - it falls back to the raw text as the summary.
func parseAnalysis(output string) domain.AnalysisArtifact {
	var artifact domain.AnalysisArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	return domain.AnalysisArtifact{Summary: output}
}
