---
id: AT14
depends_on: [AT01, AT02, AT03, AT04, AT05, AT06, AT07, AT08, AT09, AT10, AT11, AT12, AT13]
---
# AT14 — MVP.md §52, the summary sections

**Size** S

## Goal

SPEC.md §13, only what needs every task done: MVP.md §52 (the API transport as built), the §30 and
§31.1 lines, CLAUDE.md's pointer to `specs/api-transport/`, README's section, TODO.md P13 closed
with a pointer here, this README's `Status:`.

## Acceptance

- `scripts/check.sh` passes.

## As built

2026-11-03.

- MVP.md §52 (configuration, startup and persistence, the two wires, the tool table by
  authority, MCP, skills, what is not covered), a §30 entry for the providers' own APIs and
  LiteLLM as an endpoint only, and a §31.1 paragraph: the transport is static, with no fallback
  between transports and no server-side fallback.
- README: a section "Claude and Codex over their HTTP APIs (`transport: api`)" after the codex
  credentials, which no longer calls a direct API client a TODO item.
- CLAUDE.md: the pointer now names §52; "Out of scope" names the exception.
- Done before AT13, which it depends on: AT13 is the operator's live gate and changes no
  documentation unless a live fact contradicts SPEC (§7's gateway cost header). TODO.md P13 stays
  `[~]` until AT13 passes; P12, the older claude-only item, is marked as absorbed by P13.
