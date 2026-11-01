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
