# R02 — `workflow.Entry`, derived counters, `Verify`, limit pre-check

**Size** S–M (0.5–1 d) · **Depends on** R01 · **Blocks** R06

## Goal

The engine can continue a run from its history. The entry state is a pure function of
`Executions` and the existing transition table, and a fresh run is the empty-history case
(`SPEC.md` §7.1, §8, R2/R3).

## Files

- `internal/workflow/resume.go` (new) — `Entry`, `Counters`, `Verify`, `CheckLimits`
- `internal/workflow/engine.go` — enter via `Entry`
- `internal/workflow/resume_test.go`, `engine_test.go`

`internal/workflow` imports only `domain`.

## Steps

1. `outcomeOf(exec StepExecution) Outcome`: `test` → `TestResult.Success`; `review` →
   `ReviewResult.Decide()` (never `Status`); others → success. A completed test or review
   without its result is an error, not success.
2. `Entry(run) (State, why string, err error)` per the table in `SPEC.md` §7.1:
   - empty → `Steps[0]`;
   - last `failed` → `last.Action`;
   - last `completed` → `Next(last, outcomeOf(last), wf)`: a step → that step; `StateDone`
     → `StateDone`; `StateFailed` → `last.Action` (Q3).
   `why` is the human-readable reason used by R07 and recorded in `Resume.Why`.
3. `Counters(run) (totalSteps, fixIterations int)`: `len(Executions)`, and the number of
   completed `test`/`review` executions whose outcome is a failure when the workflow
   contains `implement`.
4. `Verify(run) error`: `run.Workflow` equals `Build(run.Decision.Stages)`, and every
   execution's action is a step of it. A run with no decision or workflow → error (the
   caller turns it into `not_resumable`).
5. `CheckLimits(run, limits) error`: `TotalSteps ≥ MaxSteps`, or entry is `implement`
   reached backward (the last completed step is `test`/`review`, and it failed) with
   `FixIterations > MaxFixIterations` → `LimitExceededError`. The message names
   `limits.max_steps` / `limits.max_fix_iterations`, the value used and the configured
   value.
6. `Engine.Run`: replace `current := State(run.Workflow.Steps[0].Action)` with `Entry`.
   Set the counters from `Counters` before the loop. `StateDone` from `Entry` → mark the run
   completed and return nil. No other change to the loop.

## Acceptance

- A table test over every row of §7.1: review fail with and without implement, test fail
  with and without implement, a failed step of each kind.
- Property: for scripted fake outcomes, stopping the engine after step *k* (context
  cancelled) and running a new `Engine.Run` on the persisted run gives the same sequence
  of actions as an uninterrupted run, for every *k*.
- `Counters` equals the engine's own `TotalSteps`/`FixIterations` on engine-produced
  histories.
- The existing `engine_test.go` and `transitions_test.go` pass unchanged.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
