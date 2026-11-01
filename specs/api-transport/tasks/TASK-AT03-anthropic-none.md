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
