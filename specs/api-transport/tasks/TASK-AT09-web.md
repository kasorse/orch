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

## As built, claude (2026-11-01, before AT06)

The owner moved claude's half ahead of the tool loop: `actions.research` is required and only
the claude CLI supported `web`, so `providers.claude.transport: api` failed `ValidateAuthorities`
on research (AT05 As built). Built in `internal/provider/anthropic`:

- `Supports(web)`; `New(baseURL, webTools, prices)` takes `providers.claude.web_tools`.
- With web tools: `web_fetch_20260209` and `web_search_20260209` (`max_uses: 10`, sorted by name),
  and a system text naming them and the absence of any repository. Without: no `tools`, a system
  text that says there is no web access and asks the model to say where its knowledge may be old.
- The call became a loop of responses for `pause_turn` only (the server's own tool loop): the
  assistant's blocks go back unchanged, no message is added, at most 4 resumptions (5 requests),
  then the call fails. Usage, cost and `Turns` add up over the responses; the answer is the text
  of all of them. AT07 extends the same loop with `tool_use`.
- `server_tool_use` blocks count in `call.tools` as `WebSearch`/`WebFetch` (the CLI's names), and
  are reported live as `WebSearch <query>` once their input is complete. `domain.CallReport` got
  `web_tools` (the names offered; validated on load like the other names).
- A `web_search_tool_result` holding an error object is carried as it came, not a failure.
- Older models (before Opus/Sonnet 4.6) need the basic tool variants; they are not chosen
  automatically: such a route gets a 400 from the API. Left for codex: OpenAI's `web_search`.
