# QL11 — `review.fix_medium`

**Size** M (2 d) · **TODO** Q6.3 · **Depends on** QL02 · **Blocks** QL13

## Goal

When the operator turns it on, a review that passes but reports `medium` issues sends the run
back to implement once, as a fix iteration. The rule is recorded on the run so that a resume
follows it (`SPEC.md` §7.3).

## Files

- `internal/config/config.go`, `validator.go`: `review.fix_medium` (new top-level `review`
  section)
- `internal/domain/run.go`: `Run.Options{ReviewFixMedium bool}` (`json:"options,omitempty"`),
  set when the run is created
- `internal/workflow/transitions.go`: `OutcomeFixable`; `Next(review, OutcomeFixable)`;
  `isBackwardEntryToImplement` unchanged (review → implement already counts)
- `internal/workflow/resume.go`: `outcomeOf(exec, run)` derives `OutcomeFixable`; `Counters`
  counts it
- `internal/workflow/engine.go`: the engine uses the same derivation as `outcomeOf` instead of
  the executor's raw outcome for review, so live and resume agree
- `internal/action/restore.go`, `prompts.go`: medium issues in the retry prompt
- `internal/app/app.go`: record `options` at run creation; `internal/persistence/validate.go`

## Steps

1. `run.json: options.review_fix_medium`, written once when the run is created and never
   changed by a resume.
2. `outcomeOf` for a review becomes: `Decide() == fail` → failure; otherwise, if
   `run.Options.ReviewFixMedium`, the review has at least one `medium` issue, no earlier
   review in this run was `OutcomeFixable`, and the fix iterations so far are below
   `MaxFixIterations` → `OutcomeFixable`; otherwise → success. `outcomeOf` needs the run and
   the limits; pass them explicitly.
3. `Next(review, OutcomeFixable)`: implement if the workflow has it, else done.
4. The engine derives the review step's outcome with the same function after persisting the
   step. One function, two callers.
5. Retry prompt: "A code review passed but noted issues worth fixing. Fix them without
   changing behavior the task did not ask for:" plus the medium issues.

## Acceptance

- Unit: `outcomeOf` table: option off; on with medium; on with medium but already used; on
  with medium but no iterations left; low issues only.
- Engine test: review(pass + medium) → implement → test → review(pass + medium) → done, with
  `fix_iterations == 1`.
- Resume after the first review enters implement; a resume with the config option now off
  still enters implement (the option comes from the run).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
