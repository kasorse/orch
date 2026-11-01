---
id: AT09
depends_on: [AT07, AT08]
---
# AT09 — authority `web`: server web tools, `web_tools`

**Size** S

## Goal

SPEC.md §5.1 (`web`) and §6. `Supports` becomes true for `web`.

## Files

- Anthropic: `web_search_20260209`, `web_fetch_20260209` with `max_uses`; their result blocks
  appended verbatim; an error result block (HTTP 200, an object instead of a list) recorded, not
  fatal. OpenAI: `web_search`.
- `web_tools: false`: no tools, a plain call; `CallReport.WebTools` records what was offered.
- Tests; a research scenario with and without web tools.

## Acceptance

- A `read` or `edit` call never carries a web tool (a test over every authority).
- `scripts/check.sh` passes.
