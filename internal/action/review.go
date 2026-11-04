package action

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/kasorse/orch/internal/apperr"
	"github.com/kasorse/orch/internal/domain"
	"github.com/kasorse/orch/internal/projectctx"
	"github.com/kasorse/orch/internal/provider"
	"github.com/kasorse/orch/internal/repository"
	"github.com/kasorse/orch/internal/secretscan"
	"github.com/kasorse/orch/internal/workflow"
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
// research/plan/implement, a malformed review has no actionable severity to safely
// resume on, so it ends the run as a hard error rather than being treated as a
// retryable "fail" (MVP.md §19, mirroring the classifier's strictness).
func (d *Dispatcher) executeReview(ctx context.Context, run *domain.Run) (workflow.StepOutcome, error) {
	// The runs directory is the orchestrator's own bookkeeping, not a change to review.
	var exclude []string
	if d.RunsDir != "" {
		exclude = append(exclude, d.RunsDir)
	}
	limits := d.Config.Limits.Prompt
	// A review run reviews merge_base..head, and its worktree is at head: the diff is taken
	// against the merge base, not against HEAD (specs/delivery/SPEC.md §12.2).
	base := ""
	if t := run.ReviewTarget; t != nil {
		base = t.MergeBase
	}
	diff, err := d.Repo.ReviewDiff(ctx, repository.ReviewDiffOptions{
		Exclude:   exclude,
		Denied:    projectctx.DenyMatcher(d.Config.Context.ExtraDeny),
		Summarize: append(slices.Clone(DefaultDiffSummarize), limits.DiffSummarize...),
		MaxBytes:  limits.DiffBytes(),
		Base:      base,
	})
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "failed to collect the diff to review", Err: err}
	}

	if diff.Empty() {
		if run.ReviewTarget != nil {
			return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "nothing to review: the worktree has no changes against the merge base " + run.ReviewTarget.MergeBase}
		}
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
		Clarification:  run.Task.Clarification.Render(),
		Acceptance:     run.Task.Acceptance,
		Plan:           d.lastInvestigation,
		Implementation: d.lastImpl,
		TestResult:     d.lastTest,
		GitDiff:        diff.Text,
		DiffPartial:    diff.Count(repository.TreatFull) < len(diff.Files) || diff.Unlisted > 0,
	})

	// The base model answers first, then each extra reviewer, one after another with the same
	// prompt (specs/quality/SPEC.md §7.5). A failed call or a malformed answer from any of
	// them fails the step: a resume reruns every reviewer.
	pm, source := d.route(run, domain.ActionReview)
	result, ref, err := d.call(ctx, run, pm, source, domain.ActionReview, prompt, provider.Extensions{})
	if err != nil {
		return d.failureOutcome(ref, result), err
	}
	criteria := reviewCriteria(run.Task.Acceptance, d.lastInvestigation)
	first, err := parseReviewStrict(result.Output, criteria)
	if err != nil {
		return workflow.StepOutcome{}, &apperr.WorkflowError{Msg: "malformed review output", Err: err}
	}
	review := first
	rawStdout := result.Output

	if extra := d.Config.Actions.Review.Models; len(extra) > 0 {
		answers := []domain.ReviewerAnswer{{Model: pm.Model, Result: first}}
		sum := newStepCalls(d.stepUsage, d.stepCall)
		for _, model := range extra {
			// Extra reviewers run on the action's base provider and effort, not on the
			// effort by_complexity routed the base review to (specs/quality/SPEC.md §7.5).
			xpm := d.Config.Actions.Review.ProviderModel
			xpm.Model = model
			d.stepUsage, d.stepCall = nil, nil
			xresult, xref, err := d.call(ctx, run, xpm, domain.ModelSourceBase, domain.ActionReview, prompt, provider.Extensions{})
			sum.add(model, d.stepUsage, d.stepCall)
			d.stepUsage, d.stepCall = sum.usage, sum.report()
			if err != nil {
				return d.failureOutcome(xref, xresult), err
			}
			answer, err := parseReviewStrict(xresult.Output, criteria)
			if err != nil {
				return workflow.StepOutcome{}, &apperr.WorkflowError{
					Msg: fmt.Sprintf("malformed review output from the extra reviewer %s", model), Err: err}
			}
			answers = append(answers, domain.ReviewerAnswer{Model: model, Result: answer})
			rawStdout += "\n" + xresult.Output
		}
		review = mergeReviews(answers)
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
		RawStdout:    rawStdout,
	}, nil
}

// parseReviewStrict decodes and validates the review. criteria are the plan's acceptance
// criteria as the prompt numbered them: with n of them the answer has exactly n verdicts,
// indexes 1..n each once, and without any it has none (specs/quality/SPEC.md §7.1). The
// model echoes only the index; Criterion is filled from criteria.
func parseReviewStrict(output string, criteria []string) (domain.ReviewResult, error) {
	obj := extractJSONObject(output)
	if obj == "" {
		return domain.ReviewResult{}, errNoJSONObject
	}
	var review domain.ReviewResult
	if err := json.Unmarshal([]byte(obj), &review); err != nil {
		return domain.ReviewResult{}, err
	}
	// The diff summary and the reviewers are the orchestrator's record, set after parsing; a
	// model that writes its own must not fail the review over it, nor have it kept (a
	// reviewers list inside an answer would nest in mergeReviews and break the saved run).
	review.Diff = nil
	review.Reviewers = nil
	if err := review.Validate(); err != nil {
		return domain.ReviewResult{}, err
	}
	// Validate has checked the range and uniqueness of the indexes, so exactly n verdicts
	// are exactly 1..n.
	if len(review.Criteria) != len(criteria) {
		return domain.ReviewResult{}, fmt.Errorf("the task and the plan have %d acceptance criteria and the review has %d verdicts",
			len(criteria), len(review.Criteria))
	}
	slices.SortFunc(review.Criteria, func(a, b domain.CriterionVerdict) int { return a.Index - b.Index })
	for i := range review.Criteria {
		review.Criteria[i].Criterion = criteria[review.Criteria[i].Index-1]
	}
	return review, nil
}
