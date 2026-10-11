# QL05 — Verify in the implement step

**Size** M (2–3 d) · **TODO** Q2 · **Depends on** QL04 · **Blocks** QL06, QL13

## Goal

`actions.implement.verify: true` gives the implement session the `orch` MCP server. The
config is checked in the fail-fast chain, the calls are recorded in `run.json`, and a
`violation` fails the step (`SPEC.md` §6.3–§6.4, §6.6, §6.8).

## Files

- `internal/config/config.go`: `ActionConfig.Verify` (only on `implement`; the raw struct in
  `UnmarshalYAML`), `LimitsConfig.Verify{MaxCallsPerStep, CallTimeout}`
- `internal/config/validator.go`: verify needs `test.command` and a sandbox; `verify` on
  another action is rejected; the reserved server name `orch`; no `__` in server names
  (`extensions.go`)
- `internal/action/implement.go`: create the dir, write `verify.json`, build the extension,
  read the results, fail on `violation`, move the logs, remove the dir
- `internal/action/context.go`: `call` takes an extra `provider.Extensions` to merge with
  `extensionsFor`
- `internal/action/prompts.go`: the verify paragraph (`SPEC.md` §6.1)
- `internal/domain/run.go`: `VerifyCall`, `StepExecution.VerifyCalls`;
  `internal/workflow/engine.go`: `StepOutcome.VerifyCalls` and the copy
- `internal/domain/outcome.go`: `Outcome.VerifyCalls`
- `internal/persistence/validate.go`; `internal/report` (`summary.md`)
- `internal/app/app.go`: remove leftover `orch-verify-*` dirs at start

## Steps

1. Config and validation as in `SPEC.md` §6.3. Defaults: 8 calls, 10m.
2. In `executeImplement`, after `Guard.Snapshot()` when verify is on:
   `os.MkdirTemp("", "orch-verify-")` (mode 0700). Write `verify.json` from the runner's
   sandbox, `execution.TestEnv()`, the guard's patterns and before-state, `pre_run_ref`,
   checks and limits. Resolve the executable with `os.Executable()` (resolved through
   symlinks). The MCP config is `{"mcpServers":{"orch":{"command":<exe>,"args":["mcp-verify",
   "--dir",<dir>]}}}`. Tools: `mcp__orch__run_tests`, plus `mcp__orch__run_check` when there
   are checks.
3. Merge the operator's extensions for `implement` with the built-in one into one
   `--mcp-config` document. The operator's servers cannot be named `orch` (step 1).
4. After the call and today's guard compare: if `violation` exists, fail the step with "the
   model's code changed protected files during a verify call: <paths>". Read `calls.jsonl`
   into `VerifyCalls`, with a malformed line as a step error. Move
   `call-<K>.{stdout,stderr}.log` to `logs/<NNN>-implement.verify-<K>.*`. Remove the dir in a
   `defer`, on every path.
5. Persist and validate `verify_calls` (`SPEC.md` §12) and sum `outcome.verify_calls`.
   `summary.md` gets a line like "verify: 5 calls, 3 passed".
6. At start, remove `$TMPDIR/orch-verify-*` dirs owned by the user that are older than one
   day. A live dir belongs to another running orchestrator, and its age is under the step
   timeout.

## Acceptance

- Unit: argv for implement with verify on has `--mcp-config` with the `orch` server and
  `--allowed-tools` with exactly `Edit,Write,mcp__orch__run_tests[,mcp__orch__run_check]`, and
  `--tools` is unchanged.
- Config: verify with `sandbox: none` → error; verify on `review` → unknown key;
  `extensions.mcp.orch` → error; a server named `a__b` → error.
- A fake `calls.jsonl` and `violation` in the dir make the step fail and record the calls.
- The dir is removed after success, failure and cancellation.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-10)

Built as planned, except as listed.

- **Gates:** written without being able to run anything. The four gates are
  `scripts/check.sh`, this repository's `test.command`, so the test step that follows runs
  them. The golden files `implement_verify.golden` and `implement_verify_checks.golden` were
  written by hand, not by `-update`: if the prompt test reports a difference, compare the
  paragraph text first. Nothing was run against a real `claude` or `bwrap` (QL06).
- **`verify` on another action** is rejected by `Validate`, not by the YAML decoder:
  `ActionConfig` stays one type, with the key read in `UnmarshalYAML` and remembered in an
  unexported field. The message is `actions.<a>.verify: unknown key (verify is accepted only on
  actions.implement)`, and the exit code is 2 like any config error. `actions.implement.verify`
  also needs provider `claude`.
- **`limits.verify`** has its own `UnmarshalYAML` for `call_timeout` (an explicit `0s` is
  refused, unset means 10m); `max_calls_per_step` is a pointer (unset means 8, an explicit 0 is
  out of range). Reserved names: `extensions.mcp.orch` and any server name with `__`.
- **`call` takes the built-in extension as a last parameter** and merges it after the hint is
  built, so the knowledge-graph hint names only the operator's tools. `mergeExtensions` fails
  on a server name in both documents. The tool order is the operator's (sorted) and then
  `run_tests`, `run_check`: claude does not sort, and the task asks for that order.
- **The sandbox is the test step's** (`Dispatcher.Runner.Sandbox`, set in `executeWorkflow`).
  Without it the step fails before the call, with no directory left behind. The executable is
  `Dispatcher.VerifyExecutable`, or `os.Executable()` with symlinks resolved.
- **Reading the directory** (`action/verify.go`) uses `O_NOFOLLOW|O_NONBLOCK`, regular files
  only, a 1 MiB bound on `calls.jsonl`, 512 KiB per call log (a cut is marked) and 20 lines of
  `violation`. `calls.jsonl` is decoded strictly, and each line passes the new
  `domain.VerifyCall.Validate`, the same check the loader applies, so a hostile line fails
  the step instead of making `run.json` unloadable. Order of errors after a call: the guard's
  own error or violation, then the verify `violation`, then an unreadable `calls.jsonl`. After a
  provider error the provider's error is kept and the calls that parsed are recorded.
- **Logs are not moved on disk.** They travel in `StepOutcome.VerifyLogs` (named `verify-<K>`,
  `K` = the line number) and `runObserver` writes `logs/<NNN>-implement.verify-<K>.*` through
  `AppendCheckLog`; a rename would fail across filesystems when `$TMPDIR` is a tmpfs.
- **`verify_calls` validation** (`domain.VerifyCall.Validate`, called from `persistence`):
  only on implement, at most 50, known tool, `check` only on `run_check` and required unless
  the call was refused (QL04's note), `filter` only on `run_tests`, a refused call has a short
  one-line reason and no result, and a call that ran has `success == (exit_code == 0)`.
- **Startup sweep:** `removeStaleVerifyDirs` runs at the start of `executeWorkflow` (run and
  resume). It uses `Lstat`, owner uid and a 24 h age, and ignores errors.
