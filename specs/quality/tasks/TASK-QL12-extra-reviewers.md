# QL12 — Extra reviewers

**Size** M (2 d) · **TODO** Q6.5 · **Depends on** QL02 · **Blocks** QL13

## Goal

`actions.review.models: [opus]` runs one or more extra reviews in the same step, one after
another, on the action's provider. Go merges their answers, and the merged result decides
(`SPEC.md` §7.5).

## Files

- `internal/config/config.go`, `validator.go`: `ActionConfig.Models []string` (accepted only on
  `review`, ≤ 4, non-empty names, no duplicates of the base model)
- `internal/action/review.go`: loop over the models, strict parse per answer, merge
- `internal/action/reviewmerge.go` (new): the merge (union of issues, criteria AND)
- `internal/domain/artifact.go`: the review artifact's `reviewers`
- `internal/domain/call.go`: `CallReport.ExtraCalls`
- `internal/action/context.go`: usage summed over the calls of one step
- `internal/persistence/validate.go`; `internal/report` (`summary.md`: "reviewers: sonnet,
  opus")

## Steps

1. Config validation as above. `ValidateDependencies` is unchanged (same provider), and the
   effort of extra reviewers is the base effort.
2. `executeReview`: build the prompt once, then call the base model and each extra model in
   order. Parse each answer strictly (QL02's criteria validation included). A malformed answer
   from any reviewer fails the step.
3. Merge: issues de-duplicated by (severity, file, normalised description) using the
   normalisation from QL08 (equal word sets). Criteria: `met` only if all reviewers say met,
   and the evidence of the first that says not met. `summary` is the base reviewer's, and
   `summary_short` too.
4. The artifact keeps `reviewers: [{model, result}]` and the merged result.
   `executions[].review_result` is the merged result. `call` is the first call's report, and
   `call.extra_calls` holds `{model, api_ms, input_tokens, output_tokens}` per extra call.
   Usage is the sum.
5. A resume of an interrupted review step reruns every reviewer (today's rule: a failed step
   is retried as a whole).

## Acceptance

- Unit: merge table (an issue only in the second reviewer appears; the same issue from both
  appears once; criteria AND).
- A malformed second answer fails the step even when the first is valid.
- Integration (QL13): the fake CLI answers `review_1.json` and `review_2.json`; the merged
  high issue from the second reviewer sends the run back to implement.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-10)

Built as planned, except:

- **Gates:** `scripts/check.sh` passed on the final code, outside the sandbox (`go build`,
  `go vet`, `go test`, `gofmt -l`, in the root module and in `bench/`). QL12 is ticked in
  `README.md` and `TODO.md`.

- **Config:** `ActionConfig.Models` and `config.MaxExtraReviewers`; the checks are
  `extraReviewerProblems` in `validator.go` (more than 4, an empty name, a repeat of the base
  model, of a `by_complexity` model of review or of an earlier entry, and the key on any action
  but `review`). A null entry (`models: [~]`) is refused at load by `ActionConfig.UnmarshalYAML`:
  yaml.v3 would drop it from a `[]string` without a word.
- **Effort and model:** extra reviewers run on the action's base provider and base effort
  (`Actions.Review.ProviderModel` with the extra model), never on the effort `by_complexity`
  routed the base review to.
- **Model-written `reviewers`:** `parseReviewStrict` clears `reviewers` like `diff`; both are the
  orchestrator's record, and a list inside an answer would otherwise nest in `mergeReviews`.
- **Artifact:** `reviewers` is a field of `domain.ReviewResult` (`[]ReviewerAnswer{Model,
  Result}`, base model first), so the artifact, `run.json` and the resume check that the two agree
  all see the same value. A single review writes none. `Validate` bounds it (`MaxReviewers`) and
  refuses a reviewer that has reviewers of its own; `persistence/validate.go` calls
  `ValidateReviewers`.
- **Merge:** `mergeReviews` in `reviewmerge.go`. `Status` of the merged result is its `Decide()`.
  The word-set normalisation is a copy of `workflow.normalizeWords` (unexported, and `action`
  should not reach into it): lower case, letters and digits, words of 3+ characters.
- **Calls:** `stepCalls` in `context.go` sums the usage; `ExtraCall.InputTokens` is every input
  token, cached or not (`Usage.PromptTokens`). A failed extra call is still counted, so the
  step's usage is what was spent. `ComputeOutcome` counts each extra call in `Provider calls`.
  The raw stdout logged for the step is the answers joined by a newline.
- **Report:** `summary.md` gets a `Reviewers | sonnet, opus` row, only when the last review had
  extra reviewers.
- **Tests:** `reviewmerge_test.go` (merge table, criteria AND, the step with two reviewers,
  usage and `extra_calls`, a malformed second answer, the second answer's criteria, a failed
  extra call), `config.TestLoad_ReviewModels`, `TestValidate_ReviewModels`,
  `TestParseReviewStrict_DropsModelWrittenReviewers`,
  `TestExecuteReview_ModelWrittenReviewersDoNotNest`,
  `TestExecuteReview_ExtraReviewersUseTheBaseEffort`, the null and by_complexity cases of
  `TestValidate_ReviewModels`, `domain.TestReviewResult_ValidateReviewers`, `TestCallReport_ExtraCalls`,
  `TestComputeOutcome_CountsExtraReviewerCalls`, `report.TestRunSummary_Reviewers`, three
  `persistence` corrupt-state cases. The integration scenario stays in QL13; the example config
  has a commented `models` line, the rest of the documentation is QL14's.
