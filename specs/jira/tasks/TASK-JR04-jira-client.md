---
id: JR04
repo: orche
depends_on: [JR03]
jira: none
---
# JR04 — internal/jira client: request, retries, validation, JiraError, redaction

**Size** M

## Goal

`(*jira.Client).Fetch(ctx, key)` makes the one GET of `SPEC.md` §8. It returns a validated
`domain.Ticket` or an `*apperr.JiraError` whose text never contains a credential. Tests use only
`httptest`.

## Files

- `internal/jira/doc.go` (new): the package doc - it imports only `domain`, `apperr` and the
  standard library, never `config`, and it is read-only.
- `internal/jira/client.go` (new): `Options`, `Field`, `Credentials`, `Client`, `NewHTTPClient`,
  `Fetch`, the retry loop, redaction.
- `internal/jira/response.go` (new): unexported wire types and `toTicket`.
- `internal/apperr/jira.go` (new): `JiraError`, `JiraFailureKind`.
- `internal/exitcode/exitcode.go`: the `JiraError` case next to `DecisionError`.
- `internal/jira/client_test.go`, `internal/apperr/jira_test.go`,
  `internal/exitcode/exitcode_test.go`.

## Steps

1. `apperr.JiraError{Key, Kind, StatusCode, Attempts int; Body, Detail string; Err error}` with
   kinds `auth`, `not_found`, `redirect`, `timeout`, `connection`, `rate_limited`,
   `unavailable`, `invalid_response`, `cancelled`. `Transient()` is true for timeout,
   connection, rate_limited and unavailable. `Error()` gives the messages of `SPEC.md` §14 in the
   shape `Jira is unavailable (HTTP 503 after 2 attempts): <cause>` followed by the redacted body
   excerpt; `Detail` carries the env names or the redirect host. `exitcode.FromError`: auth,
   not_found and redirect give 2, cancelled 5, the rest 3.
2. `Credentials` from `NewCredentials(auth, email, token)`, its values behind a pointer:
   `String()`/`GoString()` give `[REDACTED]`, and a raw print of a struct holding it (a `Client`)
   shows only an address, since `fmt` cannot call `String()` on an unexported field.
   `header()` and `secrets()` (the token, the email, base64 of `email:token`) are used only for
   the request and redaction.
3. `Options{BaseURL, Auth, EmailEnv, TokenEnv string; Timeout time.Duration; MaxRetries int;
   Fields []Field; UserAgent string}`, `Field{ID, Label}`. `EmailEnv`/`TokenEnv` only name the
   variables in `auth` and `not_found` messages. `NewHTTPClient(opts, creds)` trims the trailing
   `/`; the default `User-Agent` is `orchestrator`. `CheckRedirect` returns
   `http.ErrUseLastResponse`, so a 3xx comes back unfollowed and becomes `redirect` with `Detail`
   the `Location` host (a relative `Location` resolves to Jira's own host).
4. `Fetch(ctx, key)` upper-cases the key, then:
   - URL `BaseURL + "/rest/api/2/issue/" + url.PathEscape(key)` with
     `fields=summary,description,issuetype,parent` plus each `Fields[i].ID`, in that order;
     headers `Accept: application/json`, `User-Agent`, `Authorization`.
   - Each attempt under `context.WithTimeout(ctx, Timeout)`; the body read through
     `io.LimitReader(body, 1<<20+1)`, and over 1 MiB is `invalid_response` whatever the status.
   - 200 decodes; 401 auth; 403 and 404 not_found (F7); 408 timeout (`Jira did not answer in
     time`); 429 rate_limited; 500/502/503/504 unavailable; 3xx redirect; any other status
     `invalid_response` `unexpected status N`, not retried. An attempt timeout says `Jira did not
     answer within 10s`; the run's own deadline says `... within the run's execution.timeout` and
     is not retried.
   - The retry loop has the shape of `jev.httpClient.do`: `Retry-After` in seconds or as an HTTP
     date, one longer than `Timeout` not waited for, backoff `min(250ms<<(n-1), Timeout)`, a
     cancelled or expired parent never retried, `Attempts` set; tests replace `sleep`.
   - Every error body is redacted first and then cut to 512 bytes, so a secret across the cut
     never shows half of itself; `redact` also runs over transport error text.
5. `toTicket(resp, requestedKey, opts)` applies `SPEC.md` §8.2 strictly: decode
   `struct{Key string; Fields map[string]json.RawMessage}`, then each known field; a violation is
   `invalid_response` with a JSON path in `Detail` (`fields.customfield_10035: not a text field;
   remove it from jira.fields`). An absent `description` counts as `null`; a parent's summary may
   be empty but must be a string; a custom field blank after trimming is left out like `null`.
   A different valid key in the answer is kept with `RequestedKey` set. `\r\n` becomes `\n` in
   every text. `URL = BaseURL + "/browse/" + Key`; `FetchedAt` from an injectable `now`.

## Documentation

- MVP.md §23/§24: the `JiraError` kinds and their exit codes; §26: the `jira/` package and its
  unexported wire types.
- CLAUDE.md Architecture: Jira's wire format is isolated in `internal/jira` (unexported wire
  types; it imports only `domain`, `apperr` and the standard library, never `config`).

## Acceptance

- The test server asserts the exact path, query and Basic/Bearer header.
- 200 with every field; `description: null`; no parent; a custom field absent, null, a string and
  an object (error); a moved key. The validation table goes through `Fetch` against `httptest`.
- Each status of step 4 maps to its kind; `exitcode.FromError` gives the right code per kind.
- 503 then 200 with `max_retries: 1` succeeds after 2 attempts; `max_retries: 0` fails after 1;
  `Retry-After: 1` is honoured (sleep recorded); `Retry-After` over the timeout is not waited
  for; cancellation during backoff is `cancelled` with no further request.
- A redirect to the same host and to another host is `redirect`, and the target gets no request.
- A 1 MiB + 1 body is `invalid_response`.
- No error text contains the token, the email or their base64; the test server echoes
  `Authorization` into its error body and the excerpt shows `[REDACTED]`; a token placed across
  the 512-byte cut is not visible (`TestFetch_RedactsBeforeTruncating`).
- `%v`, `%+v` and `%#v` of `Credentials`, of structs holding it and of a `Client` contain no
  secret (`TestCredentials_NeverPrinted`).
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira/`.
