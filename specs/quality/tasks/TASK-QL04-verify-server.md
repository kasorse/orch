# QL04 — `internal/verify`: the stdio MCP server

**Size** M (3–4 d) · **TODO** Q2 · **Depends on** QL03 · **Blocks** QL05

## Goal

A small MCP server, run as `orchestrator mcp-verify --dir <dir>`, serves exactly two tools,
`run_tests` and `run_check`. Each call checks the guard, then runs a configured command in the
sandbox, and records the call. Its only input is `verify.json` (`SPEC.md` §6.1–§6.2,
§6.4–§6.7).

## Files

- `internal/verify/spec.go`: the `verify.json` type (`version: 1`), read and write, strict
  decode
- `internal/verify/jsonrpc.go`: line-delimited JSON-RPC 2.0 over an `io.Reader`/`io.Writer`,
  1 MiB line limit
- `internal/verify/server.go`: `initialize`, `notifications/initialized`, `tools/list`,
  `tools/call`, `ping`
- `internal/verify/tools.go`: argument validation (filter regex, check enum), limits, timeout,
  guard, run, record (`calls.jsonl`, `violation`, per-call logs)
- `internal/app/app.go`: the hidden `mcp-verify` subcommand (not in the usage text)
- the guard: `action.RepoGuard` used from `verify`. If the import would cycle (`action` will
  import `verify` in QL05), move `Guard`, `GuardState` and `RepoGuard` into a small package
  (e.g. `internal/guard`) with `action` keeping aliases.

## Steps

1. `verify.json` struct with everything in `SPEC.md` §6.4: repository root, test command,
   checks, `pre_run_ref`, sandbox description, test env pairs, guard patterns and
   before-state, limits, and the output bound. Write it with mode 0600. Read it strictly
   (`DisallowUnknownFields`, known version).
2. JSON-RPC: a decoder over `bufio.Reader` with a 1 MiB limit per line, and a writer that
   writes one compact JSON object plus `\n`. Errors: -32700 parse, -32600 invalid request,
   -32601 unknown method, -32602 invalid params. Notifications get no answer.
3. `initialize`: answer `protocolVersion` (the client's, if it is in the server's list, else
   the server's newest), `capabilities: {"tools": {}}`, and `serverInfo: {name: "orch",
   version}`. Check the current MCP specification for the version strings and the
   `tools/list`/`tools/call` result shapes, and record in this task's "As built" which
   protocol versions it lists.
4. `tools/list`: `run_tests` with `{"type":"object","properties":{"filter":{"type":"string",
   "pattern":"^[A-Za-z0-9_./:-]{1,200}$"}},"additionalProperties":false}`. `run_check` with
   `name` as an `enum` of the check names, only when there are checks.
5. `tools/call`, in this order: count against the limit; validate arguments (unknown
   argument → refused); if a violation was already recorded, refuse; `Changed(before)`, and
   on any change write `violation` and refuse; run through `testexec.Runner` with the
   sandbox from the spec, the env pairs, `ORCH_BASE_REF`, and `ORCH_TEST_FILTER` only if a
   filter was given; apply `call_timeout`; bound the output with `BoundTestOutput`; append
   one JSON line to `calls.jsonl`; write the full output to
   `dir/call-<K>.{stdout,stderr}.log`; answer with the text block of `SPEC.md` §6.1.
6. The subcommand: `mcp-verify --dir <abs dir>`. It refuses a relative dir, a missing or bad
   `verify.json`, or a sandbox description that is empty (verify never runs unsandboxed).
   It serves until EOF, then exits 0.

## Acceptance

- A table test of request → response for every method, a malformed line, a 2 MiB line, an
  unknown method, and a notification.
- A fuzz test of the filter: only strings matching the regex are accepted; `;`, a space, `$`,
  a backtick, a quote, a newline, an empty string and 201 bytes are refused.
- With a fake runner: the 9th call with limit 8 is refused and nothing runs; a timeout gives
  exit -1 and "timed out"; a guard change writes `violation`, and that call and every later
  one are refused without running.
- `calls.jsonl` lines decode into the record type of `SPEC.md` §6.8.
- `orchestrator mcp-verify` without `--dir` exits 2 with a usage error.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-10)

Built as planned, except as listed.

- **Gates:** the change was written without being able to run anything. The four gates are
  `scripts/check.sh`, which is this repository's `test.command`, so the test step that follows
  the change runs them; if it reports a failure, read it first: the code, the moved tests and
  the new tests were all checked by reading only. Nothing was run against a real `bwrap`: the
  tests use a fake runner and guard, plus the real guard on a real git repository for the
  refusal path (`internal/app/mcpverify_test.go`).
- **MCP protocol versions:** the server lists `2025-11-25`, `2025-06-18`, `2025-03-26` and
  `2024-11-05`, newest first (`supportedVersions` in `server.go`). `initialize` echoes the
  client's version when it is in the list, else answers `2025-11-25`. The subset used
  (`initialize`, `notifications/initialized`, `tools/list`, `tools/call`, `ping`; a `tools/call`
  result of `content: [{type: text, text}]` and `isError`; a tool entry of `name`,
  `description`, `inputSchema`) has the same shape in all four. These strings come from memory
  of the published specification revisions, not from a fetch (no network while this was
  written): check them against the current spec, and against what the installed `claude`
  sends in `initialize` (QL06), before relying on the newest.
- **The guard moved to `internal/guard`** (`action` will import `verify` in QL05).
  `guard.Guard`, `guard.State` (was `GuardState`), `guard.RepoGuard`, `guard.NewRepoGuard`,
  `guard.CheckBaseline` and `guard.GitViolation` live there, with a read-only
  `State.Patterns()`. `action/guard.go` keeps aliases (`Guard`, `GuardState`, `RepoGuard`,
  `NewRepoGuard`, `gitViolation`) and thin `CheckBaseline`; `BaselineViolation` and
  `RunDirPatterns` stay in `action`. The persisted JSON (`patterns`, `git_fingerprint`,
  `protected`) is unchanged and still decoded strictly; a test pins its three keys. Only the
  text of a decode error changed (`invalid guard state`, was `invalid artifact`). The guard
  tests moved with it; `guardRepo` and `put` stay in `action/guard_test.go` because the diff
  stat and review tests use them.
- **`BoundTestOutput` and `Omitted` moved to `testexec`** (`internal/testexec/bound.go`, with
  `bound_test.go` and the fuzz test); `action/bound.go` keeps `Omitted` and `BoundTestOutput`
  as an alias and a variable, so `prompts.go` is untouched.
- **The environment question:** `testexec.Runner` gained `BaseEnv []string`. Nil keeps today's
  behaviour (`execution.TestEnv()`); non-nil replaces it, and an empty non-nil slice is an
  empty base, never "inherit". `Sandbox.env` takes that base (TMPDIR still pinned to `/tmp`).
  `RunOptions.Env` is unchanged and still `ORCH_*` only. The server sets `BaseEnv` from
  `verify.json`'s `test_env`, because its own environment is whatever `claude` gave an MCP
  child, not the orchestrator's. `ReadSpec` refuses any `ORCH_*` name in `test_env`, so
  `ORCH_TEST_FILTER` can only come from a validated argument. `Sandbox` got JSON tags
  (`home`, `repo_root`, `git_dirs`, `ro_binds`, `rw_binds`) for `verify.json`.
- **`verify.json`** has no separate `patterns` field: the guard state already carries the
  patterns it was taken with, and the server uses `before.Patterns()`. `ReadSpec` also
  requires an owner-only regular file (no symlink, mode `& 0o077 == 0`), absolute sandbox
  paths, valid unique check names, `max_calls` 1..50 and `call_timeout > 0`.
- **A guard error fails closed.** If `Changed` returns an error (not a change), the call is
  refused and `violation` is written, like a change. A transient git failure therefore ends
  verify for the rest of the step, and QL05 will fail the step on the marker. This is the
  safe choice; an operator may find it surprising. A cancelled session during the check is
  the exception: it refuses the call and writes nothing.
- **Refused calls count and are recorded.** Every `tools/call` of an offered tool counts
  against `max_calls`, a refusal included, and gets a `calls.jsonl` line with `refused` set.
  The file holds at most 50 lines (`domain.MaxVerifyCalls`); later refused calls are
  counted but not recorded. A refusal's `refused` field is a fixed string and never echoes
  model input, and `filter` and `check` are recorded only when valid, so a refused `run_check`
  with an unknown name has no `check`. QL05's validation of `verify_calls` ("`check` set iff
  `run_check`") must allow that for refused entries.
- **A restarted server keeps the step's state:** `NewTools` counts the lines already in
  `calls.jsonl` and treats an existing `violation` file as a violation.
- **Timeout:** `call_timeout` is a `context.WithTimeout` per run. `OSProcessExecutor` kills
  the process group, and `Runner` turns the deadline into an error that discards the partial
  output, so a timed-out call answers `exit code: -1` with the one line `timed out after
  <d>; ...` as its only output, and the call logs hold that line. A cancelled session is not
  a timeout: the call is refused with `failed to run`.
- **The answer** is the SPEC §6.1 block plus a `check:` line for `run_check`; `filter:` only
  when one was given. The output block is bounded stdout, then bounded stderr, or
  `(no output)`. `output_bytes: 0` keeps everything, as the prompts do.
- **Call logs:** `call-<K>.{stdout,stderr}.log` are written in full (O_EXCL, no symlink,
  0600) only for calls that ran. `K` is the call's number, so it is also the line number in
  `calls.jsonl` up to 50. A log or `calls.jsonl` write failure goes to stderr and does not
  fail the call.
- **JSON-RPC:** a line over 1 MiB is skipped without being kept and answered `-32700` with a
  null id; the next line is read normally. `"id": null`, a batch, a non-2.0 message and a
  non-object are `-32600`. A message without an id is a notification and is never answered
  or run, a `tools/call` sent that way included. A client response is ignored. An unknown
  tool name, or `run_check` without checks, is `-32602` and does not count against the limit.
- **`mcp-verify`** parses its own `--dir` (`internal/app/mcpverify.go`); a missing, relative
  or repeated `--dir`, or any other argument, is a `ConfigError` (exit 2). It also opens the
  repository with `repository.Open`, so a `repo_root` that is not a git tree fails to start.
  The usage text is unchanged.
