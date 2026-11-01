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
