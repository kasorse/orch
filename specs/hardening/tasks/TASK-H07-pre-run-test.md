# H07 — Pre-run test (`test.baseline`)

**Size** M (1.5 d) · **TODO** S4 · **Depends on** H04, H05 · **Blocks** H09

## Goal

A workflow that both implements and tests runs the test command once before the first step;
a red suite stops the run before `implement` is blamed for it (`SPEC.md` §8).

## Files

- `internal/config/config.go`, `validator.go`: `test.baseline`
- `internal/action/pretest.go` (new) + test: `Dispatcher.PreRunTest`
- `internal/app/app.go`: `executeWorkflow`
- `internal/domain/run.go`: `PreRunTest`
- `internal/persistence`: save logs `pre-run-test.*`, validate the field
- `internal/logging`: console line (+ golden)

## Steps

1. Config: `test.baseline: require_green | off`, empty = `require_green`.
2. `action.(*Dispatcher).PreRunTest(ctx, run) (domain.TestResult, error)`: reuse the body of
   `executeTest` (factor the guard + runner part into a helper used by both) but do not call
   `applyTest`, do not save an artifact, do not touch `run.Executions`. A guard violation
   returns the same `WorkflowError` text with step name `pre-run test`.
3. `executeWorkflow` — reorder only this part of the tail:
   ```
   approval → mode → SaveRun → restore point / resume baseline (if no executions) → pin
   → runCtx → test sandbox → dispatcher.Guard
   → [NEW] pre-run test (if SPEC §8.1 holds)
   → engine.Run
   ```
   Condition helper `needsPreRunTest(cfg, run) bool` with a table test (§8.1 cases).
4. On exit 0: `run.PreRunTest = {ExitCode, Duration, Sandboxed}`, `SaveRun`, console
   `pre-run test: passed (12.3s)`.
   On non-zero: write `logs/pre-run-test.{stdout,stderr}.log` (non-empty streams only, like
   step logs), `failRun` with the reason from `SPEC.md` §8.4, return a `WorkflowError` (exit 4).
5. Resume: a run with no executions takes the "fresh tail" path already, so the pre-run test
   runs again and overwrites `PreRunTest`. A run with executions never runs it.
6. `persistence`: `pre_run_test` optional; when present `exit_code` is an int and
   `duration` ≥ 0.

## Acceptance

- Unit (`needsPreRunTest`): implement+test → yes; test only → no; implement only → no;
  `baseline: off` → no; executions present → no; plan mode never reaches it.
- Integration: red suite (fake test command `exit 1` before implement) → exit 4, no implement
  call in the invocation log, reason as specified, logs written; same config with
  `baseline: off` → implement runs; green suite → `pre_run_test.exit_code: 0` and the normal
  flow; a test command that edits `.git/hooks` during the pre-run test → guard violation.
- Resume of the red run after the suite is fixed (fake toggled) → pre-run test passes, workflow
  runs from the first step.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
