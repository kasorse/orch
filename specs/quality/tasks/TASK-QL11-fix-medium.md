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

## As built (2026-10-10)

Done as planned, except:

- **Gates:** `scripts/check.sh` passed (`go build`, `go vet`, `go test`, `gofmt -l`). QL11 is
  marked ✓ in `README.md`. The edits of the review round that followed (the derived outcome
  reported to the observer, the engine failing on an unreadable history, their tests) were
  written after that run; `scripts/check.sh` is to be run once more on them.
- **Signatures:** `outcomeOf(exec, run, limits)` and, because it needs the limits, `Entry(run,
  limits)` and `Counters(run, limits)` (the callers in `app/resume.go` and the engine pass
  `limitsView(cfg)` / `e.Limits`). The history before the step is walked once by a small
  `tally` (fix iterations spent, fixable already used), shared by `outcomeOf` and `Counters`,
  so there is no recursion. `OutcomeFixable` also needs `implement` in the workflow.
- **Engine:** the engine takes the derived outcome only for a review that carries a
  `ReviewResult`; one without it (a scripted executor in the tests) keeps the executor's
  outcome. The derived outcome is what the observer is told too, so `ConsoleReporter` prints a
  fixable review as a pass followed by "review.fix_medium is on: fixing the medium issues." and
  the "Returning to implementation" notice (`pretty`: the "↻ Returning to implement" line). If
  `outcomeOf` cannot read the history (a completed test or review without its result) the run
  fails with "cannot derive where to continue", as `Entry` and `Counters` do on a resume, so
  the live run and a resume never take different paths.
- **`run.json`:** `Run.Options` is tagged `json:"options,omitzero"` (not `omitempty`, which
  never omits a struct), so a run with every option off writes no `options` key.
- **Config:** `review.fix_medium` is `config.ReviewConfig.FixMedium` (a plain bool, strict
  parsing already refuses a non-boolean and unknown `review` keys); there is nothing to add to
  `validator.go`, and `persistence/validate.go` needed nothing (a bool inside a strictly
  decoded struct). `app.runOptions(cfg)` sets it right after `store.NewRun`.
- **Prompt:** `Dispatcher.pendingMediumIssues` (set by `applyReview` for a passing review,
  cleared with the other pending context) feeds `ImplementPromptInput.MediumIssues`. An
  implement after a passing review happens only through `OutcomeFixable`, so no flag is needed.
- **Tests:** `workflow.TestOutcomeOf_FixMedium`, `TestNext_ReviewFixable`,
  `TestCounters_FixableReviewIsAFixIteration`, `TestEntry_FixMedium` (including a run whose
  option is off while the limits are unchanged), `TestEngine_FixMedium`,
  `TestEngine_FixMediumOffIgnoresMediumIssues`; `action.TestRestore_MediumIssuesArePending`;
  `config.TestLoad_ReviewFixMedium`; `app.TestRunOptions_ReviewFixMedium`;
  `domain.TestReviewResult_MediumIssues`, `TestRun_OptionsJSON`;
  `workflow.TestEngine_FixMediumObserverSeesTheDerivedOutcome`,
  `TestEngine_UnreadableHistoryEndsTheRun`; `logging.TestStepFinished_ReviewFixable_ExplainsTheReturnToImplement`;
  `app.TestResumeEntry_UsesTheRunsFixMediumNotTheConfigs` (the `Entry` call `resumeTask` makes,
  with a config whose `review.fix_medium` differs from the run's options). The integration scenario stays
  in QL13; MVP.md, CLAUDE.md and README.md are QL14's.
