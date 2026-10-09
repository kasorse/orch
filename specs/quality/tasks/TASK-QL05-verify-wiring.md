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
