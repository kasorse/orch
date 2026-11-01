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
