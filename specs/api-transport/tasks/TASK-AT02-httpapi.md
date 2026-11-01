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

## As built (2026-11-01)

- `Endpoint{BaseURL, Header, Key, HTTPClient, Backoff}` with `Do(ctx, method, path, body)`: the
  body bytes are resent on each retry; a 2xx comes back with its body unread, so a stream is never
  retried once it started. `StatusError{Status, Type, Code, Message, RetryAfter, ResetsAt}` parses
  the common `{"error": {...}}` envelope of both providers; `Kind(err)` gives the adapters the
  failure kind (auth, rate_limit, timeout, cancelled), so the classification is in one place.
- Retried: 408, 429, 500, 502, 503, 504, 529 and an `overloaded_error` type on any status (a
  gateway may rewrite the status), and a connection error. 401/402/403, `authentication_error`,
  `permission_error`, `billing_error` and `insufficient_quota` are never retried.
- `Price` got one long-context tier (`LongAbove`, `Long`): both pricing pages have one (Claude
  Haiku 5.5 over 100K prompt tokens, GPT over 272K), and without it a long prompt would be priced
  low. The built-in table is dated `PricesTaken` (2026-11-01): Anthropic's from the claude-api
  reference (cached 2026-10-06), OpenAI's from its pricing page. An OpenAI model whose page lists no
  cache-write price is billed no extra for one (write = input). Gateway names are not in the table.
- `CheckModels` follows Anthropic's `has_more`/`last_id` paging (`limit=1000&after_id=` from the
  second page on, so the first request has no query an OpenAI-shaped endpoint could refuse), with a
  50-page bound. It takes the provider name for its messages.
- Not here: converting `config.ModelPrice` into `Price` (AT05: httpapi must not import config).
