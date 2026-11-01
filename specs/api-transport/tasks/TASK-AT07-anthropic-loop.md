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
