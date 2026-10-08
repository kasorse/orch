package action

import (
	"context"
	"encoding/json"
	"slices"

	"orchestrator/internal/apperr"
	"orchestrator/internal/domain"
	"orchestrator/internal/projectctx"
	"orchestrator/internal/repository"
	"orchestrator/internal/secretscan"
	"orchestrator/internal/workflow"
)

// DefaultDiffSummarize are the files the review diff always shows as a stat line only:
// lock files and minified or generated output, whose content a reviewer cannot judge and
// which can dwarf the real change (specs/hardening/SPEC.md §5.3.1). Manifests (go.mod,
// package.json, ...) are deliberately absent: a dependency change stays visible.
// limits.prompt.diff_summarize adds to this list, never removes from it.
var DefaultDiffSummarize = []string{
	"go.sum", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb",
	"Cargo.lock", "poetry.lock", "uv.lock", "Pipfile.lock", "composer.lock", "Gemfile.lock",
	"*.min.js", "*.min.css", "*.map",
}

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
	limits := d.Config.Limits.Prompt
	diff, err := d.Repo.ReviewDiff(ctx, repository.ReviewDiffOptions{
		Exclude:   exclude,
		Denied:    projectctx.DenyMatcher(d.Config.Context.ExtraDeny),
		Summarize: append(slices.Clone(DefaultDiffSummarize), limits.DiffSummarize...),
		MaxBytes:  limits.DiffBytes(),
	})
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to collect the diff to review", Err: err}
	}

	if diff.Empty() {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "nothing to review: the working tree has no changes against HEAD"}
	}
	// What looks like a secret is not sent to the review model: a finding recorded under
	// mode warn, or one already in the tree when the run started (specs/hardening/SPEC.md
	// §9.5). Under mode stop a new finding never gets this far.
	if d.Config.Security.SecretScan.Enabled() {
		diff.Text = secretscan.Redact(diff.Text)
	}

	if diff.Truncated {
		d.stepTruncated = append(d.stepTruncated, domain.PromptDiff)
	}
	prompt := BuildReviewPrompt(ReviewPromptInput{
		Task:           run.Task.Description,
		Analysis:       d.lastAnalysis,
		Plan:           d.lastPlan,
		Implementation: d.lastImpl,
		TestResult:     d.lastTest,
		GitDiff:        diff.Text,
		DiffPartial:    diff.Count(repository.TreatFull) < len(diff.Files) || diff.Unlisted > 0,
	})

	result, ref, err := d.call(ctx, run, d.Config.Actions.Review.ProviderModel, domain.ActionReview, prompt)
	if err != nil {
		return d.failureOutcome(ref, result), err
	}

	review, err := parseReviewStrict(result.Output)
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "malformed review output", Err: err}
	}

	review.Diff = &domain.ReviewDiffSummary{
		Bytes:      len(diff.Text),
		Files:      len(diff.Files) + diff.Unlisted,
		Shown:      diff.Count(repository.TreatFull),
		Summarized: diff.Count(repository.TreatSummarized),
		Withheld:   diff.Count(repository.TreatWithheld),
		Truncated:  diff.Truncated,
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
	// The diff summary is the orchestrator's record, set after parsing; a model that
	// writes one of its own must not fail the review over it.
	review.Diff = nil
	if err := review.Validate(); err != nil {
		return domain.ReviewResult{}, err
	}
	return review, nil
}
