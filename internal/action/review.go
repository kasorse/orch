package action

import (
	"context"
	"encoding/json"
	"strings"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/workflow"
)

// executeReview requires strict, well-formed structured output - unlike
// research/analyze/plan/implement, a malformed review has no actionable severity to safely
// resume on, so it ends the run as a hard error rather than being treated as a
// retryable "fail" (MVP.md §19, mirroring the classifier's strictness).
func (d *Dispatcher) executeReview(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	// The runs directory is the orchestrator's own bookkeeping, not a change to review.
	var exclude []string
	if d.RunsDir != "" {
		exclude = append(exclude, d.RunsDir)
	}
	diff, err := d.Repo.Diff(ctx, exclude...)
	if err != nil {
		return workflow.StepOutcome{}, err
	}

	if strings.TrimSpace(diff) == "" {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "nothing to review: the working tree has no changes against HEAD"}
	}

	prompt := BuildReviewPrompt(ReviewPromptInput{
		Task:           run.Task.Description,
		Analysis:       d.lastAnalysis,
		Plan:           d.lastPlan,
		Implementation: d.lastImpl,
		TestResult:     d.lastTest,
		GitDiff:        diff,
	})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Review.ProviderModel, domain.ActionReview, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	review, err := parseReviewStrict(result.Output)
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "malformed review output", Err: err}
	}

	path := d.saveArtifact(run, domain.ActionReview, review)

	d.applyReview(review)
	outcome := workflow.OutcomeSuccess
	if review.Decide() == domain.ReviewFail {
		outcome = workflow.OutcomeFailure
	}

	return workflow.StepOutcome{
		Outcome:      outcome,
		Provider:     &ref,
		ArtifactPath: path,
		ReviewResult: &review,
		RawStdout:    result.Output,
	}, nil
}

func parseReviewStrict(output string) (domain.ReviewResult, error) {
	obj := extractJSONObject(output)
	if obj == "" {
		return domain.ReviewResult{}, errNoJSONObject
	}
	var review domain.ReviewResult
	if err := json.Unmarshal([]byte(obj), &review); err != nil {
		return domain.ReviewResult{}, err
	}
	if err := review.Validate(); err != nil {
		return domain.ReviewResult{}, err
	}
	return review, nil
}
