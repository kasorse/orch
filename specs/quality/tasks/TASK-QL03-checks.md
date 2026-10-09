# QL03 — Deterministic checks in the test step

**Size** M (2–3 d) · **TODO** Q5 · **Depends on** — · **Blocks** QL04, QL09, QL13

## Goal

Operator-defined checks (build, vet, lint, SAST) run before `test.command` inside the test
step and the pre-run test. Each one is an exit-code gate under the same sandbox, environment
and guard (`SPEC.md` §5).

## Files

- `internal/config/config.go`: `CheckConfig{Name, Command}`, `Config.Checks`
- `internal/config/validator.go`: name regex, uniqueness, ≤ 16, non-empty command, requires
  `test.command`
- `internal/domain/artifact.go`: `CheckResult`, `TestResult.Checks`, `TestResult.FailedCheck`
- `internal/action/testaction.go`: `guardedTest` becomes a sequence with a guard compare
  after each command
- `internal/testexec/runner.go`, `sandbox.go`: an extra env pair (`ORCH_BASE_REF`) per run
- `internal/action/prompts.go`: retry text for a failed check
- `internal/app/app.go`: `preRunTest` records `failed_check`; per-check logs
- `internal/persistence/validate.go`: `checks`, `failed_check`
- `internal/report`: `summary.md` shows the failed check
- `orchestrator.example.yaml`

## Steps

1. Config and validation as in `SPEC.md` §5.1.
2. `Runner.Run` gains an options argument (or a `RunSpec`) with extra env pairs. The sandbox
   adds them with `--setenv`, and the plain run appends them to `TestEnv()`. Extra env pairs
   are only `ORCH_*` names set by Go (QL04 adds `ORCH_TEST_FILTER`).
3. The test sequence: snapshot once; for each check, then `test.command`: run with
   `ORCH_BASE_REF=<pre_run_ref>`, then `Changed(before)` (fingerprint first), and stop on a
   violation or a non-zero exit. Build `TestResult` as in §5.3: the top-level fields are the
   failing command's or `test.command`'s, `Checks` lists what ran, and `FailedCheck` is set
   when a check failed.
4. Logs: `logs/<NNN>-test.<check>.{stdout,stderr}.log` for each check, and the existing test
   logs for `test.command`. The pre-run test uses the `pre-run-test.<check>.*` names.
5. The pre-run test runs the same sequence. A red check refuses the run unless
   `--allow-red-tests`, with the message naming the check. `PreRunFailure` carries the
   failing check's result to the first implement.
6. Retry prompt (`SPEC.md` §5.4): name the failed check and say the tests did not run.
7. Validation of persisted `checks` and `failed_check`.

## Acceptance

- Unit (fake runner and guard): with checks `a, b`, `b` failing: `a` and `b` run, the test
  command does not, `failed_check: "b"`, and the guard is compared twice. With a violation
  after `a`: `b` does not run and the step fails with the guard error.
- `ORCH_BASE_REF` reaches the command with and without the sandbox (the sandbox case under
  the existing bwrap test helper).
- Config: a duplicate name, a bad name, 17 checks, or checks without `test.command` are each
  rejected.
- A red check in the pre-run test refuses the run; with `--allow-red-tests` the first
  implement prompt shows the check's output.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-09)

Done as planned, except:

- **Gates:** the change was written without being able to run anything, so the four gates
  (`scripts/check.sh`) and the two new golden files are for the operator's run to confirm. The
  goldens `implement_retry_check` and `implement_red_prerun_check` were written by hand
  from the prompt code; if they differ, read the diff and run `go test ./internal/action
  -update`. The live sandbox test (`go test -tags security ./test/security/...`) was updated
  for `ORCH_BASE_REF` and not run either.
- **Shared names live in `domain`:** `domain.CheckNameRE` and `domain.MaxChecks`, so
  `persistence` (which does not import `config`) and QL04 use the same ones; the config
  validator uses them too.
- **`Runner.Run(ctx, command, dir, RunOptions{Env})`:** a pair must match `^ORCH_[A-Z0-9_]+=`,
  or `Run` returns an `ExecutionError` and nothing runs. Unsandboxed, the pairs are appended to
  `TestEnv()`. Under bwrap they are `--setenv KEY VALUE` after the isolation flags and before
  `--chdir`; the bwrap process's own environment stays the allowlist.
- **`ORCH_BASE_REF` is always set**, empty when `pre_run_ref` could not be recorded, so every
  command sees the same environment. The example config says so.
- **`guardedTest` returns `(*TestResult, []CheckOutput, error)`.** `CheckOutput` is the raw
  output of each check, carried to the log writers through `StepOutcome.CheckLogs`; it is not
  persisted. A failed check's output is in both its own log and the step's usual
  `<NNN>-test.*.log`, since the top-level fields are the failing check's. On a guard
  violation the partial result (what ran so far) is returned with the error.
- **Logs:** `Store.AppendCheckLog` writes `logs/<NNN>-test.<check>.<stream>.log` and refuses a
  name that does not match `CheckNameRE`. The pre-run test uses
  `AppendCallLog("pre-run-test.<check>", …)`.
- **Persistence:** `validateChecks` runs on `pre_run_test` and on every execution's
  `test_result`. It requires a failed check to be the last entry (the sequence stops there)
  and `failed_check` to name it. The existing top-level `success == (exit_code == 0)` check
  stays on `pre_run_test` only; execution results were not checked for it before, and adding
  it could refuse a run that resumes today.
- **Report:** `summary.md` shows `failed (check vet, exit 1)` in the steps table and
  `failed (check vet)` for "Tests (last run)"; the console's pre-run line names the check.
- **Integration:** `scenario26_checks_test.go` covers the refusal and `--allow-red-tests`
  with a check that fails while the fixture's test fails. "vet failed → implement fixed it →
  all green" as a full scenario is QL13's.
- **Not done:** `init` proposing checks (TODO.md Q5.5) is not in the spec's tasks.
