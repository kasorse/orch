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

## As built

Done 2026-10-05. Built with one addition the owner chose during the task (`SPEC.md` Q12)
and these differences:

- **`run --allow-red-tests`.** Under `require_green` the most common task — "fix the bug
  this failing test shows", "make the tests pass" — would always be refused, and the only way
  out (`test.baseline: off`) turns the check off for every run. With the flag, a red pre-run
  test is recorded and the run goes on; the first implement prompt gets the bounded output
  ("The test command already FAILS before any change, and the operator expects that"),
  until the first test step has run. The flag is valid with `run` only and recorded as
  `run.json: allow_red_tests`; a resume continues with it.
- **`pre_run_test` holds the whole `TestResult`** (command, exit code, stdout, stderr,
  duration, sandboxed), not only `{exit_code, duration, sandboxed}`: a run resumed after its
  first step but before implement must still show implement the red output, and `Restore`
  rebuilds it from `run.json`, as it does for test steps. Validation: `success` must match
  `exit_code == 0`, duration ≥ 0.
- The sandbox and guard setup needed no reordering: they already sat right before
  `engine.Run`; the pre-run test is inserted between them and the engine, inside the
  engine context (it counts toward `execution.timeout`).
- `executeTest` and `PreRunTest` share `Dispatcher.guardedTest`; a guard violation names
  the step `pre-run test`. The guard-less refusal now reads "running the test command
  requires a guard" for both.
- Logs go through `Store.AppendCallLog` (`logs/pre-run-test.{stdout,stderr}.log`, like the
  translate and decision calls), written when the suite is red or the guard tripped.
- Console: a spinner while it runs, then `pre-run test ✓ passed (1.2s)` or `⚠ failed (exit
  1, …)` with either "the run stops before any change" or "going on: --allow-red-tests".
- Integration configs default to `test.baseline: off` (the fixture's test is red until Add
  is fixed, which is what most scenarios do); scenario 26 sets `require_green` and covers:
  red → exit 4 before any call, with logs and both ways out named; `--allow-red-tests` →
  implement sees the red output and the run completes; green → recorded; `off` → skipped;
  a pre-run test that writes `.git/hooks` → guard violation; resume after the suite is fixed
  → pre-run test passes and the workflow runs from the first step.
