# QL02 — Review verdict per criterion

**Size** S (1–1.5 d) · **TODO** Q6.1, Q6.2 · **Depends on** QL01 · **Blocks** QL11, QL12, QL13

## Goal

When the plan has acceptance criteria, review returns a checked verdict for each one, and an
unmet criterion fails the review like a `high` issue. Review is told to read the code around
the change (`SPEC.md` §7.1–§7.2).

## Files

- `internal/domain/artifact.go`: `CriterionVerdict`, `ReviewResult.Criteria`, `Decide`,
  `Validate`
- `internal/action/review.go`: `parseReviewStrict` (validation against the plan's count),
  `executeReview`
- `internal/action/prompts.go`: `BuildReviewPrompt` (criteria list, verdict shape, the
  Read/Grep paragraph), `BuildImplementPrompt` (unmet criteria on retry)
- `internal/action/restore.go`: `applyReview` carries the unmet criteria into the retry state
- `internal/persistence/validate.go`: validation of `review_result.criteria`
- `internal/report`: `summary.md` shows met/total

## Steps

1. Add `CriterionVerdict{Index int; Criterion string; Met bool; Evidence string}` with json
   tags `index`, `criterion`, `met`, `evidence`, and `Criteria []CriterionVerdict`
   (`omitempty`) on `ReviewResult`.
2. Review prompt: when the plan has N > 0 criteria, list them `1.`…`N.`, add `"criteria":
   [{"index": 1, "met": true, "evidence": "file:line or why not"}]` to the shape, and require
   exactly one entry per criterion. Without criteria, the shape is unchanged.
3. `parseReviewStrict(output, n int)`: when n > 0, require exactly n entries with indexes
   1…n, each once, and evidence of 1…500 bytes. When n = 0, require none. Go fills
   `Criterion` from the plan. Any violation is "malformed review output", as today.
4. `Decide()`: an unmet criterion fails the review. `Validate()` checks the shape above for
   persisted data (index range, no duplicates, evidence size).
5. Retry prompt after a failed review: "Unmet acceptance criteria:" with the criterion and
   the evidence, before the issues.
6. Add the Read/Grep paragraph (`SPEC.md` §7.2) to the review prompt, for every review.
7. Update the golden files from QL01.

## Acceptance

- Unit: a review with 3 criteria and 2 verdicts is malformed; index 0, index 4 or a
  duplicate is malformed; empty evidence is malformed; 3 verdicts with one `met: false` and
  no issues give `Decide() == fail`.
- `outcomeOf` on resume sees the persisted criteria and returns the same failure.
- The retry prompt lists the unmet criterion (golden).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-09)

Done as planned, except:

- **Gates:** `scripts/check.sh` passed (`go build`, `go vet`, `go test` including the
  prompt golden comparison, and `gofmt -l`), so the hand-written goldens, including the new
  `implement_retry_review_criteria`, match the code. QL02 is marked ✓ in `README.md`.
- **`ReviewResult.ValidateCriteria()`** holds the shape check (range, duplicates, evidence
  size); `Validate()` calls it. The persistence load calls `ValidateCriteria` only, so a
  hand-written `run.json` whose review has no `status` is not newly refused. It also refuses
  a `review_result` on a step that is not review.
- **`planCriteria`** (prompts.go) is the plan's non-blank criteria, the list the prompt
  numbers; the review prompt and `executeReview` both use it, so index `N` is the same
  criterion in both.
- **`ReviewResult.UnmetCriteria()`** feeds `pendingUnmetCriteria` (set and cleared with
  `pendingReviewIssues`) and the report. `summary.md` and the resume history show
  `N/M criteria met` next to the issue count when the review has verdicts.
- **Retry prompt:** with unmet criteria the "FAILED" line names them, "Unmet acceptance
  criteria:" lists `- <index>. <criterion>` and `  evidence: <evidence>` (each on one
  line), and the issues follow under "Review issues:". An issues-only retry is unchanged.
- **Integration and fake-CLI fixtures** whose plans have criteria must return verdicts;
  the full set is QL13's.
