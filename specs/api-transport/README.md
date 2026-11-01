# The API transport: claude and codex over HTTP

Status: **in progress** (2026-11-01): AT01 done (1 of 14). The owner's
answers are `SPEC.md` §0 (O1–O12).

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 14 implementation tasks in three phases |

## The problem in one paragraph

Every model call starts a CLI process. With an API key the orchestrator can call the Anthropic
Messages API and the OpenAI Responses API itself: `providers.<p>.transport: api`, a `base_url` for
the operator's LiteLLM gateway (which reaches Bedrock), full model IDs, streaming, explicit prompt
caching, retries, and cost from the tokens × the published price. A call with tools becomes a tool
loop run in Go, and the boundary the CLI flags enforce today becomes Go code (`SPEC.md` §5):
confined read and edit tools, no shell, the deny list, the MCP servers started by the adapter, the
operator's skills offered through a `skill` tool. Routing, the decision, the guard and resume stay
as they are.

## Ground rules

- A task is done when its acceptance holds, `scripts/check.sh` passes, its row below and
  `TODO.md` P13 are ticked, this `Status:` line is current, and the task file has an `## As built`.
- Each task updates the documentation its own change makes out of date (CLAUDE.md, "Tasks from
  specs/*/tasks"); AT14 adds only what needs every task done.
- Tests need no network and no key: `httptest` servers replay recorded streams. AT13 is live and
  left to the operator.

## Tasks

| done | task | title | size | depends on | phase |
|---|---|---|---|---|---|
| [x] | [AT01](tasks/TASK-AT01-config.md) | `transport`, `base_url`, `prices`, `web_tools`: schema and validation | S | — | 1 |
| [ ] | [AT02](tasks/TASK-AT02-httpapi.md) | `httpapi`: SSE, retries, redaction, cost table, the model check | M | AT01 | 1 |
| [ ] | [AT03](tasks/TASK-AT03-anthropic-none.md) | Anthropic Messages client, authority `none` | M | AT02 | 1 |
| [ ] | [AT04](tasks/TASK-AT04-openai-none.md) | OpenAI Responses client, authority `none` | M | AT02 | 1 |
| [ ] | [AT05](tasks/TASK-AT05-wiring.md) | wiring: registry by transport, fail-fast, `provider_cli`, cost by model | M | AT03, AT04 | 1 |
| [ ] | [AT06](tasks/TASK-AT06-tools-read.md) | `tools`: confinement, `read_file`, `grep`, `glob` | M | AT01 | 2 |
| [ ] | [AT07](tasks/TASK-AT07-anthropic-loop.md) | Anthropic tool loop, caching breakpoints, authority `read` | M | AT03, AT06 | 2 |
| [ ] | [AT08](tasks/TASK-AT08-openai-loop.md) | OpenAI tool loop, encrypted reasoning, authority `read` | M | AT04, AT06 | 2 |
| [ ] | [AT09](tasks/TASK-AT09-web.md) | authority `web`: server web tools, `web_tools` | S | AT07, AT08 | 2 |
| [ ] | [AT10](tasks/TASK-AT10-edit.md) | authority `edit`: the text editor tool, `apply_patch` (V4A) | L | AT07, AT08 | 3 |
| [ ] | [AT11](tasks/TASK-AT11-mcp.md) | MCP stdio client: `orch` verify and `extensions.mcp` | M | AT07, AT08 | 3 |
| [ ] | [AT12](tasks/TASK-AT12-skills.md) | skills under `user_settings`: discovery and the `skill` tool | M | AT07, AT08 | 3 |
| [ ] | [AT13](tasks/TASK-AT13-live.md) | live checks: first-party endpoints and the gateway (operator) | S | AT09–AT12 | 3 |
| [ ] | [AT14](tasks/TASK-AT14-documentation.md) | MVP.md §52, the summary sections | S | all | 3 |
