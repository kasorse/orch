---
id: AT13
depends_on: [AT09, AT10, AT11, AT12]
---
# AT13 — live checks: first-party endpoints and the gateway (operator)

**Size** S

## Goal

SPEC.md §11, the live part, under `-tags security`: the wire works against the real endpoints, and
the facts the spec assumes hold.

## Files

- `test/security/api_test.go`: per endpoint (Anthropic first-party, OpenAI first-party, the
  operator's LiteLLM when its variables are set): a `none` call; a two-turn `read` call whose second
  request reads the cache (`cache_read_input_tokens` / `cached_tokens` > 0); an `edit` call through
  the text editor / `apply_patch` in a temporary repository; a path escape refused; `GET /v1/models`
  lists the routed IDs; whether `x-litellm-response-cost` arrives on a streamed response (recorded
  in As built, and SPEC §7 corrected if it does not).

## Acceptance

- Run by the operator with the keys; left unticked until then (CLAUDE.md, "Tasks from specs").

## As built

2026-11-04, `test/security/api_test.go` (`-run TestAPI`): one subtest per endpoint whose variables
are set (`ORCH_LIVE_ANTHROPIC_KEY`, `ORCH_LIVE_OPENAI_KEY`, `ORCH_LIVE_LITELLM_URL` with
`ORCH_LIVE_LITELLM_KEY` and `ORCH_LIVE_LITELLM_{CLAUDE,CODEX}_MODEL`). The cache check is three
requests, not two: the second carries a ~40 KiB tool result, whose last line names the file to read
next, so the third must follow it and read the prefix back.

Run by the operator against the LiteLLM gateway only (Bedrock behind it; `bedrock/gpt-6-luna` for
codex): the owner has no first-party key, so **the first-party endpoints are not checked yet** and
the task stays unticked until they are.

- Gateway, both wires: `GET /v1/models`, `none`, the read escapes refused (`.env`, `../`, an
  absolute path) pass. The cache works through the gateway: claude read 18 412 cached tokens
  (`cache_control` reaches Bedrock), codex 12 954 (the history replayed under `store: false` with
  encrypted reasoning is accepted).
- claude `edit` passes (the text editor reaches Bedrock). codex `edit` failed: the gateway drops
  OpenAI's built-in `{"type": "apply_patch"}`, and the model answered that it had no tool to edit
  with. Fixed by the owner's choice: the built-in tool on `api.openai.com` only, and behind any
  other `base_url` a strict function tool `apply_patch` of the same operation (`tools.Root.PatchTool`,
  SPEC §5.1). Re-run on the gateway 2026-11-04: passes.
- `x-litellm-response-cost` does not come on a streamed response, as a header or a trailer, on
  either wire. SPEC §7, MVP.md §52.3 and the README now say a gateway route is priced from `prices`
  only.
