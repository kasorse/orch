---
id: AT11
depends_on: [AT07, AT08]
---
# AT11 — MCP stdio client: `orch` verify and `extensions.mcp`

**Size** M

## Goal

SPEC.md §5.3. Both API clients become `provider.MCPSupporter`.

## Files

- `internal/provider/tools/mcp.go`: parse `Extensions.MCPConfig`; start each server (argv, cwd,
  `execution.ScrubbedEnv()` passed in by the client, the server's `env`); JSON-RPC over stdio with
  bounded messages; `tools/list` filtered by `Extensions.MCPTools`; `tools/call`; kill on return.
- Tests against a fake MCP server built in the test (`go build` of a testdata program); an
  integration scenario: implement with `actions.implement.verify` calling `mcp__orch__run_tests` over
  HTTP, `calls.jsonl` read back as today.

## Acceptance

- A server that fails to start is in `CallReport.MCPFailed` and the call goes on without it.
- A tool name not allowlisted is not offered, and a call to it is an error result.
- `scripts/check.sh` passes.

## As built (2026-11-02)

- The client is its own package, `internal/provider/mcp`, not `tools/mcp.go`: it runs commands,
  and `tools` must import neither `os/exec` nor `net` (its test says so). It imports only the
  standard library; the clients pass it `execution.ScrubbedEnv()` and the working directory.
- `mcp.Start(ctx, doc, allow, dir, env)` starts each stdio server (sorted by name), runs
  `initialize` (protocol 2025-06-18, which the `orch` server lists), `notifications/initialized`
  and `tools/list` with its cursor (20 pages at most), all within 30 s, and keeps the tools whose
  `mcp__<server>__<tool>` name the allowlist covers and both APIs accept (`[A-Za-z0-9_-]{1,64}`).
  `Session.Call` sends `tools/call`, joins the text blocks (other content becomes a marker,
  `structuredContent` the text when there is none), bounds the result at 64 KiB and passes
  `isError` on. A request the server sends meanwhile is answered "method not found"; a message
  between requests is dropped. `Close` closes stdin and kills a server after 2 s.
- A URL server (`type: http`) is reported in `call.mcp_failed`, not reached: no HTTP transport for
  MCP here (a `shortcut:` comment marks it).
- Both API clients are `provider.MCPSupporter`, which `action` checks before it gives implement
  the verify server; MCP tools go to OpenAI as function tools with `strict: false` sent
  explicitly (the Responses API takes an absent strict as true) and to Anthropic as client tools.
- The fake server of the package's tests is the test binary itself (re-executed with
  `FAKE_MCP_SERVER`); the integration scenario uses the real `orch` server, as scenario 36 does.
