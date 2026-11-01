---
id: AT02
depends_on: [AT01]
---
# AT02 — `httpapi`: SSE, retries, redaction, cost table, the model check

**Size** M

## Goal

SPEC.md §3, §7 (cost), §8, §9 (the model check): the shared, provider-neutral part.

## Files

- `internal/provider/httpapi/`: an `http.Client` that follows no redirect; `Do` with the retry
  policy of §8 (429 with `retry-after`, 5xx/529 backoff with jitter, no retry after the first
  event, the reset time for `FailureRateLimit`); an SSE reader (`event:`/`data:` lines, bounded line
  size); a redactor for the key; `Price`/`Cost` with the built-in table (first-party models, the
  date taken from the providers' pricing pages) and the config's `prices`; the
  `x-litellm-response-cost` header; `CheckModels(ctx, baseURL, auth, ids)` over `GET /v1/models`
  with pagination (`has_more`/`last_id`, or no paging).
- Tests with `httptest`: each retry case, the deadline, a redirect refused, the key never in an
  error, a broken stream, cost from the table, from `prices`, from the header.

## Acceptance

- Imports only the standard library, `domain`, `apperr` (a test checks it).
- `scripts/check.sh` passes.
