package action

import (
	"context"
	"encoding/json"

	"orchestrator/internal/domain"
	"orchestrator/internal/provider"
	"orchestrator/internal/workflow"
)

func (d *Dispatcher) executeAnalyze(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildAnalyzePrompt(AnalyzePromptInput{
		Task:        run.Task.Description,
		Research:    d.lastResearch,
		FinalAnswer: isFinalReadOnlyStep(run, domain.ActionAnalyze),
	})

	pm, source := d.route(run, domain.ActionAnalyze)
	result, ref, err := d.call(ctx, run, pm, source, domain.ActionAnalyze, prompt, provider.Extensions{})
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseAnalysis(result.Output)
	d.applyAnalysis(artifact)
	path := d.saveArtifact(run, domain.ActionAnalyze, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		Artifact:     artifact,
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
