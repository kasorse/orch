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
