package action

import (
	"context"
	"encoding/json"

	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// executeImplement always reports OutcomeSuccess if the provider process exits 0 -
// whether the implementation is actually correct is verified by the next test/review
// step, not by implement itself (MVP.md §31.4).
func (d *Dispatcher) executeImplement(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	prompt := BuildImplementPrompt(ImplementPromptInput{
		Task:                   run.Task.Description,
		Analysis:               d.lastAnalysis,
		Plan:                   d.lastPlan,
		PreviousImplementation: d.lastImpl,
		LastTestResult:         d.pendingTestFailure,
		ReviewIssues:           d.pendingReviewIssues,
	})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Implement.ProviderModel, domain.ActionImplement, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	artifact := parseImplementation(result.Output)
	d.lastImpl = &artifact
	// Consumed - do not resend stale retry context to a later, unrelated implement call.
	d.pendingTestFailure = nil
	d.pendingReviewIssues = nil

	path := d.saveArtifact(run, domain.ActionImplement, artifact)

	return workflow.StepOutcome{
		Outcome:      workflow.OutcomeSuccess,
		Provider:     &ref,
		ArtifactPath: path,
		RawStdout:    result.Output,
	}, nil
}

// parseImplementation is lenient, matching parseAnalysis - see its comment.
func parseImplementation(output string) domain.ImplementationArtifact {
	var artifact domain.ImplementationArtifact
	if obj := extractJSONObject(output); obj != "" {
		if err := json.Unmarshal([]byte(obj), &artifact); err == nil {
			return artifact
		}
	}
	return domain.ImplementationArtifact{Summary: output}
}
