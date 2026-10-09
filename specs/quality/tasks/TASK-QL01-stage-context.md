# QL01 — Stage context in prompts

**Size** S (1–1.5 d) · **TODO** Q1 · **Depends on** — · **Blocks** QL02, QL13

## Goal

Every stage gets everything earlier stages found that it can use: files, risks, steps and the
plan's acceptance criteria. A retried `implement` sees what the run has changed so far
(`SPEC.md` §4).

## Files

- `internal/domain/artifact.go`: `PlanArtifact.AcceptanceCriteria`
- `internal/action/prompts.go`: `BuildPlanPrompt`, `BuildImplementPrompt`, `BuildReviewPrompt`,
  the plan's JSON shape and rule text
- `internal/action/plan.go`: `parsePlan` caps the criteria (8 × 300 bytes, UTF-8 safe)
- `internal/action/implement.go`: the retry diff stat into `ImplementPromptInput`
- `internal/action/diffstat.go` (or a new `retrydiff.go`): render a numstat as `git diff --stat`
- `internal/action/review.go`: pass the plan's steps, risks and criteria
- `internal/action/prompts_golden_test.go` (new), `internal/action/testdata/prompts/*.golden`

## Steps

1. Add `AcceptanceCriteria []string` (`json:"acceptance_criteria,omitempty"`) to
   `PlanArtifact`. Check that `restore.go`'s `decodeStrict` accepts an old plan artifact
   without the field and a new one with it.
2. Plan prompt: add the field to the JSON shape, and add a short rule saying what a criterion
   is, at most 8, none rather than vague ones (wording in `SPEC.md` §4.2). Add analysis
   `affected_files` and `risks` after the analysis block.
3. `parsePlan`: keep the first 8 non-empty entries, each cut to 300 bytes on a rune
   boundary.
4. Implement prompt: add analysis and plan `affected_files` and `risks`, and the plan's
   `acceptance_criteria`. Omit empty lists. Use one helper `writeList(b, heading, items)`.
5. Retry diff stat: when `PreviousImplementation != nil`, compute
   `DiffNumstat(<pre_run_ref tree>, WorktreeTree())` and render it like `git diff --stat`.
   Fence it as `diffstat` and bound it by `limits.prompt.max_diff_bytes`, keeping whole lines
   and adding `… N more files`. Put it in a new field `ImplementPromptInput.RunDiffStat
   string`, set by the dispatcher. The prompt builder stays pure. A git failure here is not
   fatal: leave the field empty and log a warning.
6. Review prompt: add the plan's steps and risks, and the criteria as a numbered list. The
   verdict format for them comes in QL02. In this task the review only sees them.
7. Golden files for the six prompts and their main variants, written with the nonce fixed
   through `var nonce` (`fence.go`). The `-update` flag follows `internal/jira/render_test.go`.
   Keep the existing assertion tests.

## Acceptance

- The golden files show analysis and plan files and risks in the plan and implement prompts,
  and criteria in implement and review.
- A plan answer with 12 criteria, one of them 1 KB long, keeps 8, each at most 300 bytes.
- A retry prompt with a 2 000-file stat stays within `max_diff_bytes` and ends with
  `… N more files`.
- Resuming a run whose plan artifact has no `acceptance_criteria` works (unit test on
  `Restore`).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-09)

Done as planned, except:

- **No logger in the dispatcher, so a new `Dispatcher.OnWarning` hook.** The git failure
  warning goes through it, wired in `app.go` next to `OnSecret` to a new
  `ConsoleReporter.Warning`. It is nil-safe (`Dispatcher.warn`).
- **`executeImplement` builds its prompt after the guard snapshot and `WorktreeTree`**, not
  before, so the tree is read once and the git calls keep their order. The prompt build has no
  side effect that depended on running first.
- **The stat is rendered in `retrydiff.go`** (`renderDiffStat`), reusing `diffStat` for the
  totals. When the bound cuts it, the file lines are the first ones, the totals line still
  counts every file, and the last line is `… N more files` (always "files").
  `Dispatcher.runsExclude` is shared with `stepDiff`.
- **`review.go` is unchanged**: it already passed `d.lastPlan`.
- **`writeNumberedList`** next to `writeList`, for the review's criteria; both share
  `writeItems`, and both skip blank items.
- **The golden files were written by hand**, because the change was made without being able
  to run `go test ./internal/action -update`. Run it once and check that `git status` shows no
  change under `testdata/prompts/`.
- **TODO.md Q1 stays `[~]`**: its "done when" also asks for an integration scenario, which is
  QL13.
