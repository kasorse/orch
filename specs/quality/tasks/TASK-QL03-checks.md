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
