---
id: AT04
depends_on: [AT02]
---
# AT04 — OpenAI Responses client, authority `none`

**Size** M

## Goal

SPEC.md §4.2 for a call without tools. `Supports` is true only for `none` in this task.

## Files

- `internal/provider/openai/`: unexported wire types; `Client` with `store: false`,
  `include: ["reasoning.encrypted_content"]`, `instructions`, `reasoning.effort`,
  `prompt_cache_key`; `status: incomplete`/`failed`; `insufficient_quota` → `FailureAuth`; the usage
  mapping of SPEC §7 (cached and cache-write tokens taken out of `input_tokens`).
- Tests against recorded streams, golden request bodies.

## Acceptance

- Nothing in the package assumes `api.openai.com`: a test runs every case against a `base_url`
  with a path prefix (`/litellm/v1`), as a gateway has.
- `scripts/check.sh` passes.

## As built (2026-11-01)

- `openai.Client` mirrors `anthropic.Client` (`SetAuth`, `Endpoint`, `Supports` none only,
  `ReportsCost(model)`); the key goes only in `Authorization: Bearer`.
- The answer, the usage and the model come from the terminal event's `response` object
  (`response.completed`/`incomplete`/`failed`), not from assembled deltas: it carries the whole
  output, and its items stay as raw JSON for AT08 to send back. Text deltas only feed
  `Stream`/`Activity`. The stream reading stops at the terminal event (or `[DONE]`).
- Not sent for a call without tools: `include: ["reasoning.encrypted_content"]` (nothing is
  replayed in one turn) and `prompt_cache_key` (one turn writes a cache nobody reads; the
  instructions alone are under the 1024-token minimum). Both come with the loop (AT08). No
  `max_output_tokens`: the model's own cap, unlike Anthropic, which requires one.
- `response.failed` with `rate_limit_exceeded` is a `FailureRateLimit` without a reset time (the
  run waits `execution.rate_limit_wait`); other failed codes and `incomplete` are `FailureAPI` with
  the code or reason.
- A call cut off has no usage: the Responses API reports it only in the terminal event, so a
  timeout mid-stream counts nothing toward the budget. The model's name before the end comes from
  `response.created`.
- The stream's event shapes were taken from the payloads the API reference shows (the full event
  reference is too large to read here); AT13's live check confirms them.
