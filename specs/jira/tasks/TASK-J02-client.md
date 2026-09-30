# J02 — `internal/jira` client: request, retries, validation, `JiraError`, redaction

**Size** M (1.25 d) · **Depends on** J01, J05 · **Blocks** J06

## Goal

`(*jira.Client).Fetch(ctx, key)` makes the one GET in `SPEC.md` §8. It returns a validated
`domain.Ticket` or a `*apperr.JiraError` whose text never contains a credential. Tests use
only `httptest`.

## Files

- `internal/jira/client.go` (new): `Options`, `Field`, `Credentials`, `Client`,
  `NewHTTPClient`, `Fetch`, retry loop, redaction
- `internal/jira/response.go` (new): unexported wire types and `toTicket` validation
- `internal/jira/doc.go` (new): package doc (what it may import, why it is read-only)
- `internal/apperr/jira.go` (new): `JiraError`, `JiraFailureKind`
- `internal/exitcode/exitcode.go`: the `JiraError` case
- `internal/jira/client_test.go`, `response_test.go`, `internal/apperr/jira_test.go`,
  `internal/exitcode/exitcode_test.go`

## Steps

1. `apperr.JiraError{Key, Kind, StatusCode, Attempts int; Body, Detail string; Err error}`
   with kinds `auth`, `not_found`, `redirect`, `timeout`, `connection`, `rate_limited`,
   `unavailable`, `invalid_response`, `cancelled`. `Transient()` is true for timeout,
   connection, rate_limited and unavailable. `Error()` produces the messages of
   `SPEC.md` §14, where `Detail` carries the env names or the redirect host. In
   `exitcode.FromError`: auth, not_found and redirect → 2; cancelled → 5; the rest → 3.
   Put the case next to `DecisionError`.
2. `Credentials`: built with `NewCredentials(auth, email, token string)`. Its fields are
   unexported, and `String()`/`GoString()` return `[REDACTED]`, so `%v`/`%#v` can't leak
   it. It exposes `header() string` and `secrets() []string` (the token, the email, and the
   base64 of `email:token`), used only for redaction.
3. `Options{BaseURL, Auth string; Timeout time.Duration; MaxRetries int; Fields []Field;
   UserAgent string}` with `Field{ID, Label}`. `NewHTTPClient(opts, creds) *Client` trims
   the trailing `/` from `BaseURL`. Its `http.Client` has `CheckRedirect` returning a
   sentinel error, which `attempt` maps to `redirect`, keeping only the `Location` host.
4. `Fetch(ctx, key)`:
   - URL: `BaseURL + "/rest/api/2/issue/" + url.PathEscape(key)`, with
     `fields=summary,description,issuetype,parent` plus each `Fields[i].ID`, in that order.
   - Headers: `Accept: application/json`, `User-Agent`, `Authorization` from the
     credentials.
   - Each attempt uses `context.WithTimeout(ctx, Timeout)`. Read the body through
     `io.LimitReader(body, 1<<20+1)`, and a body over 1 MiB is `invalid_response`.
   - Status mapping: 200 → decode. 401 → auth. 403 and 404 → not_found (F7). 408 →
     timeout. 429 → rate_limited. 500/502/503/504 → unavailable. Other 4xx →
     invalid_response with the status. 3xx → redirect.
   - Retry loop: copy the shape of `jev.httpClient.do`, including `Retry-After` as
     seconds or an HTTP date, a `Retry-After` longer than `Timeout` not waited for,
     backoff `min(250ms<<(n-1), Timeout)`, a cancelled or expired parent never retried,
     and `Attempts` set. Tests replace `sleep`.
   - Every error `Body` is truncated to 512 bytes, then passed through `redact`.
     `redact` also runs over `Err.Error()` text taken from transport errors.
5. `toTicket(resp, requestedKey, opts)` applies `SPEC.md` §8.2 strictly. Decode with
   `json.Decoder` into `struct{Key string; Fields map[string]json.RawMessage}`, then decode
   each known field. Violations → `invalid_response` with a JSON path in `Detail`
   (`fields.customfield_10035: not a text field; remove it from jira.fields`). Compare the
   response key with the requested key case-sensitively after upper-casing the request.
   A different valid key is kept with `RequestedKey` set. Normalise `\r\n` to `\n` in
   every text value. `URL = BaseURL + "/browse/" + Key`. `FetchedAt` comes from an
   injectable `now`.

## Acceptance

- Request: the exact path, query, and Basic/Bearer header (asserted by the test server).
- 200 with every field; with `description: null`; with no parent; with a custom field
  absent, null, a string, and an object (error); with a moved key.
- Each status in step 4 maps to its kind. `exitcode.FromError` returns the right code per
  kind.
- 503 then 200 with `max_retries: 1` → success, 2 attempts. `max_retries: 0` → an error
  after 1 attempt. `Retry-After: 1` is honoured (sleep recorded). `Retry-After` > timeout
  → no wait, error. Cancellation during backoff → `cancelled`, no further request.
- Redirect to the same host and to another host → `redirect`, and the target server gets
  0 requests carrying `Authorization`.
- A 1 MiB + 1 body → `invalid_response`.
- For every error case, `err.Error()` contains none of the token, the email or their
  base64 value. The test server echoes the `Authorization` header into its error body to
  prove redaction.
- `fmt.Sprintf("%v %#v", creds, creds)` contains no secret.

## As built

Done 2026-09-30. As planned, with these differences:

- **Redirects**: `CheckRedirect` returns `http.ErrUseLastResponse` rather than a custom
  sentinel. `Do` then hands back the 3xx response without following it, and `attempt`
  maps it to `redirect`, with `Detail` set to the `Location` host. A relative `Location`
  resolves to Jira's own host. The redirect tests (same host and another host) show that
  the target gets no request at all.
- **`Options.EmailEnv` / `TokenEnv`**: added so that `auth` and `not_found` messages can
  name the variables (`JIRA_EMAIL/JIRA_API_TOKEN`, or `JIRA_PAT` for bearer). The client
  has no other source for those names. They are used only in messages.
- **`Credentials`** holds its values behind a pointer. `fmt` cannot call `String()` on an
  unexported field, so printing a `Client` would otherwise show the token. Now a raw print
  shows an address. `TestCredentials_NeverPrinted` covers `%v`, `%+v` and `%#v` of the
  value, of structs holding it (exported and unexported) and of a `Client`.
- **Redaction order**: the body is redacted **before** it is truncated to 512 bytes, the
  reverse of step 4. Truncating first could cut a secret in half and leave the first half
  visible. `TestFetch_RedactsBeforeTruncating` puts the token across the cut.
- **Statuses** not listed in step 4 (other 2xx, 1xx, 501, 505 and so on) are
  `invalid_response` with `unexpected status N`, and are not retried. A body over 1 MiB
  is `invalid_response` whatever the status.
- **Timeout details**: an attempt timeout gives `Jira did not answer within 10s`. HTTP
  408 gives `Jira did not answer in time`. The run's deadline gives `… within the run's
  execution.timeout`, which is not retried.
- **Message shape**: the status and attempts are added in parentheses, e.g. `Jira is
  unavailable (HTTP 503 after 2 attempts)`, then `: <cause>`, then the redacted body
  excerpt.
- **Response rules**: an absent `description` counts as `null`. The parent's summary may
  be empty but must be a string. A custom field that is blank after trimming is left out,
  the same as `null`.
- **`Fetch`** upper-cases the key it is given. The default `User-Agent` is `orchestrator`;
  J06 passes `orchestrator/<app.Version>`.
- **Tests**: the response-validation table lives in `client_test.go` and goes through
  `Fetch` against `httptest`, not through `toTicket` directly, so every rule is checked
  over the real HTTP path. There is no separate `response_test.go`. The status tests use
  a server that echoes `Authorization` back in the error body, and each asserts that the
  body excerpt shows `[REDACTED]`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
