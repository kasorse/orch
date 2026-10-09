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
