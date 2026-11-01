---
id: AT08
depends_on: [AT04, AT06]
---
# AT08 — OpenAI tool loop, encrypted reasoning, authority `read`

**Size** M

## Goal

SPEC.md §4.2 (the loop, output items appended verbatim, `function_call_output`), §5, §5.2.
`Supports` becomes true for `read`.

## Files

- `internal/provider/openai/`: the loop; the reasoning items with `encrypted_content` resent;
  every `call_id` answered; the same report fields as AT07.
- Tests against recorded streams; an integration scenario with investigate on codex.

## Acceptance

- The second request of a call starts with the first request's `instructions`, `tools` and input
  unchanged (the cache prefix), then the appended items.
- `scripts/check.sh` passes.
