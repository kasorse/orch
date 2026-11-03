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

## As built (2026-11-01)

- `Execute` is a loop of responses: a completed response with `function_call` items gets every
  output item appended to `input` unchanged (reasoning with its `encrypted_content`, messages,
  calls), then one `function_call_output` per call in order; a response without calls ends the
  call. The answer is the text of every response's message items; usage adds up (nil until a
  response reports one), cost per response. `maxTurns` (500) fails the call.
- Read calls get the read tools as `function` tools with `strict: true` (the schemas of AT06 are
  strict-ready), `instructionsRead`, `include: ["reasoning.encrypted_content"]`,
  `parallel_tool_calls: true` and `prompt_cache_key` = the first 16 bytes of
  sha256(`codex`, model, authority) in hex. A `none` call still sends none of them (its golden
  bodies are unchanged).
- A refused or unknown call's output is its message prefixed `error: ` (the Responses API has no
  is_error flag).
- Shared with the claude client and moved to `internal/provider/tools`: `Set.Call` (a refusal,
  invalid input or unknown tool becomes the model's to read), `DisplayName`, `Label` and
  `MCPServerNames`.
- Not verified without a live call (AT13): that the first-party API and LiteLLM accept back, under
  `store: false`, output items that carry their `id` (reasoning `rs_…`, message `msg_…`, call
  `fc_…`). If one refuses them, the ids are dropped there.
- The codex integration scenario now runs investigate and review over HTTP.
