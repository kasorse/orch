---
id: AT07
depends_on: [AT03, AT06]
---
# AT07 — Anthropic tool loop, caching breakpoints, authority `read`

**Size** M

## Goal

SPEC.md §4.1 (the loop, `pause_turn`, the three breakpoints), §5 (the turn cap), §5.2. `Supports`
becomes true for `read`.

## Files

- `internal/provider/anthropic/`: the loop; tool results in one user message; content appended
  verbatim; `Activity`/`Stream` lines; `Tools`, `Turns`, partial usage on a cancelled context.
- The fixed system text for `read`.
- Tests: a two-turn recorded conversation, parallel tool calls, an error result, `pause_turn`, the
  turn cap, the breakpoints' positions in each request body (golden), cache tokens summed.
- An integration scenario: an investigate step over HTTP.

## Acceptance

- The `tools` list and `system` are byte-identical on every turn of a call.
- `scripts/check.sh` passes.

## As built (2026-11-01)

- The response loop of the web work (pause_turn) now also runs `tool_use`: each tool_use block of a
  response is run in order (`runTools`), all results go back in one user message, a refused or
  unknown tool as `is_error`, and the assistant's blocks are sent back unchanged. `maxTurns` (500)
  fails the call; pause_turn's limit counts consecutive pauses only.
- Read calls get `read_file`, `grep`, `glob` (`clientTool`: name, description, input_schema; no
  `strict` flag, since a gateway to Bedrock may refuse it and Go decodes the input strictly) and
  `systemRead`. The root is made from `Request.WorkingDir` and `Request.DenyRead` per call.
- Cache breakpoints: the user blocks are `*userText`/`*toolResult`, so `marshal` sets
  `cache_control` on the last block of the first, the previous and the last user message for one
  request and removes it after: the history never carries more than three, and the 20-block
  lookback is covered whatever the number of parallel results. A call without client tools gets
  none (the `none`/`web` goldens are unchanged).
- `call.tools` names the tools as the CLI does (Read, Grep, Glob); Activity and `--verbose` show
  `Read <path>`, `Grep <pattern>`, `Glob <pattern>`.
- Until AT11, the servers of `Extensions.MCPConfig` are reported in `call.mcp_failed` (the call
  runs without them), as a server that failed to start is today. `UserSettings` is not acted on
  until AT12.
- `ProviderError` of an API call prints `prompt: N bytes` without "on stdin".
- The integration scenario now runs investigate and review on claude over HTTP (implement on the
  codex CLI).
