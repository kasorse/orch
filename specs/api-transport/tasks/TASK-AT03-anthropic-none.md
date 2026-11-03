---
id: AT03
depends_on: [AT02]
---
# AT03 — Anthropic Messages client, authority `none`

**Size** M

## Goal

SPEC.md §4.1 for a call without tools: the request, the stream, the answer, usage and the report,
the errors. `Supports` is true only for `none` in this task.

## Files

- `internal/provider/anthropic/`: unexported wire types; `Client` implementing `provider.Client`,
  `CostReporter` (model-aware, with AT05), `SetAuth(key, baseURL, prices)`; the fixed system text for
  `none`; `max_tokens`, `output_config.effort`, no `thinking`, no betas; stop reasons `end_turn`,
  `max_tokens`, `refusal`; `Usage` and `CallReport` as SPEC §5.2 and §7; `Stream` gets text deltas.
- Tests against recorded streams: a plain answer, effort, a refusal, `max_tokens`, 401 →
  `FailureAuth`, 429 → retry, the usage mapping.

## Acceptance

- The request body of each case matches a golden file (`-update`), with no `anthropic-beta` header.
- `scripts/check.sh` passes.

## As built (2026-11-01)

- `anthropic.Client` with `SetAuth(key, baseURL, prices)`, `Endpoint()` (for the model check in
  AT05), `Supports` (none only) and a model-aware `ReportsCost(model)`, which becomes the
  `CostReporter` method in AT05. Headers: `x-api-key` and `anthropic-version: 2023-06-01`, nothing
  else.
- No `cache_control` on a call without tools, unlike SPEC §4.1's three breakpoints: a one-turn
  call has nobody to read the cache, and a write costs 1.25× input (the translator's and the
  decision's prompts differ from their first bytes). The breakpoints come with the loop (AT07).
- The stream is assembled into generic content blocks (start block plus deltas: text, thinking,
  signature, `input_json_delta`, citations), so AT07 can send the assistant's content back as it
  came. Unknown event types are skipped; a known one with a wrong shape fails the call.
- `apperr` got `FailureAPI` (`api`) and `ProviderError.Endpoint`: "exit (the CLI ran and exited
  non-zero)" would have been wrong for an HTTP answer. A ProviderError with an endpoint prints
  `endpoint:` and no `exit code:`; any ProviderError with `APIStatus` prints `http status:` (the
  LLM decision provider already reads `APIStatus`). MVP.md §23 says so.
- A cut-off call (timeout, a broken stream, an error event) keeps the usage streamed so far with
  `Partial: true`, and its cost is computed from those tokens, not left 0: the budget should count
  what is known to have been spent. Output tokens are a lower bound there.
- An `error` event inside the stream is reported as the API's error (type and message), before
  the check for a missing `message_stop`.
