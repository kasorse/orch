# Spec: Take the task from a Jira ticket

Status: **proposed**. The design questions were answered on 2026-09-30 (§18). Defaults
chosen without asking are marked "default" in §18 and can still be changed before work
starts.
Scope: a new `--jira <KEY|URL>` flag on `orchestrator run`. It fetches one issue over the
Jira REST API (read-only) and renders it into the task text, and the rest of the run is
unchanged. The decision flow, workflow construction, transitions, static routing,
authorities, guards and what is trusted (exit codes + git diff) stay the same. `resume`
never contacts Jira.
This adds a second HTTP integration next to Jev, so MVP.md §2 and §30 must be amended
(§15). When this is implemented, MVP.md gets a new §39 as the normative home, as §37 is
for `specs/resume/` and §38 for `specs/permissions/`.

---

## 1. Problem

Tasks already live in Jira. To run one today, the operator copies the summary and
description into `orchestrator run "<task>"` by hand. That is slow and loses structure
(sections such as *Steps to reproduce* / *Expected result*, the `{{code}}` spans, the parent
epic). It also leaves nothing in `run.json` that records which ticket the run was for.

Example ticket (Jira Cloud, `yourcompany.atlassian.net`), as REST v2 returns it:

```
key:         PMC-2789
issuetype:   Bug
parent:      PMC-2663 "Tags & Labels" (Epic)
summary:     FE[catalog]: Tags/Labels disable reason not shown on hover in the editor
description: Precondition:\nWhile implementing PMC-2766 (locking {{Tags}}/{{Labels}} ...
             *Impact*: ... Steps to reproduce: ... Actual result: ... Expected result: ...
```

## 2. What exists

| piece | state | where |
|---|---|---|
| task input | one positional string, `strings.Join(rest[1:], " ")` | `internal/app/app.go:70` |
| `domain.Task` | `description`, `created_at` | `internal/domain/task.go` |
| task use | every prompt reads `run.Task.Description`; the decision reads it as `classification.Request.Text` | `internal/action/*.go`, `app.go:264` |
| HTTP client pattern | Jev: `net/http`, per-attempt timeout, retries on 408/429/5xx with `Retry-After`, key only passed to the constructor and redacted from errors | `internal/decision/jev/client.go` |
| secret-in-config rejection | literal `decision.jev.api_key` is an error | `internal/config/decision.go:322` |
| env scrub | removes only parent-session variables; **everything else is inherited** by provider CLIs **and the test command** | `internal/execution/env.go:15` |
| run load validation | strict, field by field | `internal/persistence/validate.go` |

## 3. Findings

| # | Finding | Where |
|---|---|---|
| F1 | **Credentials in the environment reach model-written code.** `ScrubbedEnv` keeps every variable except the parent-session ones. The test command runs code a model wrote, and without `test.sandbox: bwrap` it has network access, so it can read `JIRA_API_TOKEN` (and, today, `JEV_API_KEY`) and send it anywhere. Provider CLIs have no shell, so they cannot read it, but the test command can. | `internal/execution/env.go` |
| F2 | **The ticket author writes the prompt.** Anyone with edit rights on the ticket controls text that `implement` (`edit` authority) acts on. Per §18 Q6 this is accepted and treated like typed text. It must still be **visible** before anything runs and persisted verbatim, so the operator can see what drove the run. | — |
| F3 | **Credentials must go only to the configured host.** A browse URL on the command line, or an HTTP redirect, must never make the client send `Authorization` to a different host. Go drops the header on cross-host redirects, but a same-host redirect (e.g. to a login page) would still be followed. | — |
| F4 | **Cloud v3 returns ADF JSON; v2 returns wiki markup.** Endpoint `/rest/api/2/issue/{key}` exists on both Cloud and Data Center and returns `description` and text custom fields as wiki-markup strings. No ADF renderer is needed. | — |
| F5 | **Parent differs by deployment.** Cloud puts both sub-task parents and epics in `fields.parent`. Data Center uses `parent` only for sub-tasks, and the epic is a per-instance "Epic Link" custom field that holds only a key. | — |
| F6 | **A moved issue answers under a new key.** `GET /issue/OLD-1` returns `key: NEW-9`. | — |
| F7 | **404 means "missing" or "not visible to you".** Jira does not tell the two apart. | — |

## 4. Principles

- **J1: The ticket is only a way to write the task.** The rendered text becomes
  `Task.Description` and the run carries on exactly as if it had been typed: translation,
  decision, workflow, prompts, resume. No stage learns about Jira.
- **J2: Read-only, one request, before the run exists.** A single GET after the fail-fast
  chain and before `store.NewRun`. A fetch failure creates no run directory. There is
  nothing to resume, because a run without a task means nothing.
- **J3: Resume never re-fetches.** The task persisted in `run.json` is the task (MVP.md
  §37, R1). A ticket edited after the run started does not change the run.
- **J4: Credentials are configuration, handled like the Jev key.** They come only from the
  environment and are named by config. A literal secret in the file is rejected. They are
  passed only to the client constructor, sent only to `jira.base_url`'s host, redacted
  from every error, and scrubbed from the environment of every child process (F1).
- **J5: Deterministic rendering.** The same issue JSON always renders the same text. No
  LLM summarises, cleans or converts the ticket.
- **J6: Strict response validation.** Anything unexpected is an `invalid_response`, never
  guessed at.

## 5. Non-goals

- Writing to Jira: comments, transitions, worklogs, links (§18 Q5). A later spec.
- Comments, linked issues, sub-tasks, attachments and images (§18 Q4).
- JQL, boards, sprints, or picking "the next ticket".
- The Data Center "Epic Link" custom field (F5). On DC only a sub-task's parent is shown.
- Converting wiki markup to Markdown.
- OAuth 2.0 (3LO) or any interactive login.
- `--jira` on `resume` or `validate`.

## 6. CLI

```
orchestrator run --jira PMC-2789
orchestrator run --jira pmc-2789 "only the FE part; leave pim-qa alone"
orchestrator run --jira https://yourcompany.atlassian.net/browse/PMC-2789 --mode plan
orchestrator run --jira=PMC-2789
```

- `parseArgs` gains `--jira X` / `--jira=X`, in any position, like `--mode`. It is valid
  only with `run`. With any other command it is a usage error (`ConfigError`, exit 2).
- With `--jira`, the positional text is optional and becomes **operator notes** (§9). With
  no positional text, the task is the ticket alone. Without `--jira`, `run` behaves as it
  does today.
- `--jira` given twice: usage error.

### 6.1 Resolving the reference (`jira.ParseRef`)

Runs after `config.Validate`, before any request, because the URL form needs
`jira.base_url`. Any failure is a `ConfigError` (exit 2).

| input | result |
|---|---|
| `PMC-2789`, `pmc-2789` | key `PMC-2789` (upper-cased) |
| `https://<base host>/browse/PMC-2789` (optionally a trailing `/`, a query or a fragment, which are ignored) | key `PMC-2789` |
| URL whose scheme+host+port differs from `jira.base_url` | error: `--jira URL host "x.example" does not match jira.base_url "yourcompany.atlassian.net"` |
| any other URL path (boards, `selectedIssue=`, `/issues/?jql=`) | error: `pass the issue key or its /browse/<KEY> URL` |
| anything not matching `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` after upper-casing | error naming the expected form |

The URL form is only a convenience for giving the key. The request always goes to
`jira.base_url`, never to the URL the operator pasted (F3).

## 7. Configuration

A new optional top-level section. Config stays `version: 2` because the section is
additive.

```yaml
jira:                              # needed only for run --jira
  base_url: https://yourcompany.atlassian.net   # DC: including any context path, e.g. https://jira.corp/jira
  auth: basic                      # basic: Cloud (email + API token) | bearer: Data Center PAT
  email_env: JIRA_EMAIL            # basic only: variable holding the account email
  token_env: JIRA_API_TOKEN        # variable holding the API token (basic) or the PAT (bearer)
  timeout: 10s                     # one HTTP attempt
  max_retries: 1                   # 0-3; 408/429/5xx only, honours Retry-After
  max_bytes: 32768                 # cap on the rendered task
  fields:                          # extra text fields, rendered in this order when non-empty
    - id: customfield_10035
      label: Acceptance criteria
```

### 7.1 Validation (`config.Validate`, whenever the section is present)

- `base_url`: absolute `https://` URL with no query or fragment. `http://` is accepted
  only for a loopback host (`localhost`, `127.0.0.1`, `::1`), so tests can use a plain
  `httptest` server. Credentials are never sent in clear text to a real host.
- `auth` ∈ `basic | bearer`, required.
- `token_env` required. `email_env` required for `basic` and rejected for `bearer`. Both
  must be valid environment variable names.
- A literal `token`, `api_token`, `password`, `pat` or `email` key is an error whatever
  `auth` is: `name an environment variable via jira.token_env / jira.email_env instead`
  (captured like `apiKeyLiteralSet` in `config/decision.go`).
- `timeout` > 0 (default 10s), `max_retries` in 0–3 (default 1), `max_bytes` in
  1024–262144 (default 32768).
- `fields[].id` matches `^customfield_[0-9]+$`. System fields are fixed (§8.2) and cannot
  be added. `label` is non-empty and single-line. Ids and labels are unique.
  `Summary`/`Description`/`Parent`/`Operator notes` are reserved labels.
- Problems are aggregated with every other config problem, as today.

### 7.2 Dependencies (`config.ValidateDependencies`)

`ValidateDependencies` gains an options argument, `DependencyOptions{Jira bool}`:

- `run --jira`: `Jira: true`. A missing section is an error (`--jira needs a jira section
  in the configuration`). Each named variable must be set and non-empty. Problems are
  aggregated with the executable checks, and nothing is sent anywhere yet.
- `run` without `--jira`: the Jira env vars are **not** checked. Having the section
  configured must not break runs that don't use it.
- `orchestrator validate`: checks the variables whenever the section is present, so
  `validate` answers "would `--jira` work here?".

The chain order is unchanged: Load → Validate → ValidateDependencies → ValidateAuthorities
→ `ParseRef` → the fetch.

## 8. Fetching the issue (`internal/jira`)

A new package in the shape of `internal/decision/jev`. Transport types are unexported. It
exports `Client`, `NewHTTPClient(Options, Credentials)`, `ParseRef`, `Render` and the
`domain`-level result. It imports only `domain` and `apperr` (plus the standard library).

### 8.1 Request

```
GET {base_url}/rest/api/2/issue/{KEY}?fields=summary,description,issuetype,parent,<fields[].id...>
Accept: application/json
User-Agent: orchestrator/<version>
Authorization: Basic base64(email:token)     # auth: basic
Authorization: Bearer <token>                # auth: bearer
```

- The key is path-escaped even though `ParseRef` already restricts it.
- `http.Client{CheckRedirect: refuse}`: **any** redirect is an error (`kind: redirect`,
  showing only the status and the `Location` host). A redirect from the REST API means a
  proxy or login page, never data (F3).
- The response body is read through `io.LimitReader(1 MiB)`. A larger body is an
  `invalid_response`.
- Timeout per attempt is `jira.timeout`. The whole fetch runs under the run's
  `execution.timeout` context (`decideCtx`), which starts before the fetch.
- Retries (`max_retries`) on 408, 429, 500, 502, 503 and 504, and on transport errors
  other than cancellation. `Retry-After` (seconds or HTTP date) is honoured, but one
  longer than `timeout` is not waited for. Otherwise exponential backoff capped at
  `timeout`. This duplicates Jev's ~40-line loop instead of extracting a shared package,
  because the two error types differ and one more importer does not justify an
  abstraction (CLAUDE.md, out-of-scope rule).

### 8.2 Response validation

Decoded into an unexported struct. Anything else fails with `invalid_response` and the
path of the offending field:

| field | rule |
|---|---|
| `key` | string matching the key regex. If it differs from the requested key (a moved issue, F6), it is accepted, both keys are recorded, and a warning is printed. |
| `fields.summary` | non-empty string |
| `fields.description` | string or `null` (an issue can have no description) |
| `fields.issuetype.name` | non-empty string |
| `fields.parent` | absent/`null`, or `{key, fields.summary}` with a valid key and a string summary |
| `fields.<customfield_N>` | absent, `null` or string. Any other JSON type (select, user, number, array) is `invalid_response`: `jira field customfield_10035 is not a text field; remove it from jira.fields` |

Unknown extra fields in the JSON are ignored (Jira always adds `self`, `id`, `expand`).

### 8.3 Result

```go
// domain/ticket.go
type Ticket struct {
    Key          string        // as answered
    RequestedKey string        // as asked; differs only for a moved issue
    URL          string        // {base_url}/browse/{Key}
    Type         string
    Summary      string
    Description  string        // wiki markup, verbatim; "" when null
    Parent       *TicketParent // Key, Summary
    Fields       []TicketField // Label, ID, Value, in jira.fields order; only non-empty ones
    FetchedAt    time.Time
}
```

## 9. Rendering the task (`jira.Render`)

`Render(t Ticket, notes string) (string, error)` returns the text that becomes
`Task.Description`. It is plain, deterministic, and has no trailing whitespace on any line:

```
PMC-2789: FE[catalog]: Tags/Labels disable reason not shown on hover in the editor
Type: Bug
Parent: PMC-2663 — Tags & Labels
Source: https://yourcompany.atlassian.net/browse/PMC-2789

Description:
Precondition:
While implementing PMC-2766 (locking {{Tags}}/{{Labels}} to read-only ...
...

Acceptance criteria:
...

Operator notes:
only the FE part; leave pim-qa alone
```

- Sections with no content (no parent, empty description, empty custom field, no notes)
  are left out entirely.
- Wiki markup is passed through verbatim (F4). Models read it, and conversion would be an
  unaudited transform (J5). `\r\n` is normalised to `\n`, and nothing else changes.
- The operator's notes come **last** and are labelled, so the operator's own words are
  the final thing each stage reads.
- If the rendered text exceeds `jira.max_bytes`, `Render` returns an error (`ConfigError`,
  exit 2): `PMC-2789 renders to 51 203 bytes, more than jira.max_bytes (32 768); raise
  the limit or trim the ticket`. It never truncates, because a silently dropped
  *Expected result* is worse than a refusal (§18, default D2).
- Golden tests: `go test ./internal/jira -update`.

## 10. Order of operations (`runTask`)

```
parseArgs                              --jira value, notes = positional text (may be empty)
loadAndValidateConfig(Jira: true)      the whole fail-fast chain, env vars included (§7.2)
jira.ParseRef(ref, cfg.Jira.BaseURL)   ConfigError on a bad key/URL — still no network
decideCtx starts (execution.timeout)
repository.Open, IsDirty               local failures before the network, as today
reporter.FetchingTicket(key, host)
client.Fetch(decideCtx, key)           the one HTTP request (plus retries)
jira.Render(ticket, notes)
reporter.Ticket(ticket, rendered)      the full task, printed before anything is decided (F2)
store.NewRun(Task{Description: rendered, Source: &TaskSource{...}})
store.SaveTicket(run.ID, ticket)       ticket.json, audit only
... unchanged: lock, RunHeader, decision, workflow, approval, executeWorkflow
```

The fetch, rendering and printing sit in one helper in `app` (`taskFromJira`), so
`runTask` gains about five lines. Everything after `store.NewRun` is untouched.

## 11. Persistence and resume

### 11.1 `run.json`

`domain.Task` gains an optional source:

```go
type Task struct {
    Description string      `json:"description"`
    Source      *TaskSource `json:"source,omitempty"`
    CreatedAt   time.Time   `json:"created_at"`
}

type TaskSource struct {
    Kind         string    `json:"kind"`                     // "jira"
    Key          string    `json:"key"`
    RequestedKey string    `json:"requested_key,omitempty"`  // only when moved
    URL          string    `json:"url"`
    FetchedAt    time.Time `json:"fetched_at"`
    Notes        bool      `json:"notes"`                    // operator notes were appended
}
```

`Description` stays the complete rendered text, so every prompt, the decision and resume
read the same field as today with no change.

`persistence.validateRun` adds these checks: when `source` is present, `kind` must be
`jira`, `key` (and `requested_key`, if set) must match the key regex, `url` must parse as
an absolute URL, and `fetched_at` must be non-zero. These fields are displayed only and
never used to make a request (J3), but they are validated anyway, because the run
directory is model-writable (specs/resume/SPEC.md G6).

### 11.2 `ticket.json`

`<run-dir>/ticket.json`: the validated `Ticket` (§8.3), written once after `NewRun`. It is
an audit record and is **never read back** by resume or anything else. It is not added to
`action.RunDirPatterns`, since only resume inputs are protected (MVP.md §38.3), and it is
not one of them.

### 11.3 Resume

No code path changes. `resume` builds no Jira client and needs no `jira` section or env
vars. The resume header gains one line when `task.source` is present:

```
Task from PMC-2789, fetched 2026-09-30 10:12 (not re-fetched)
```

## 12. Security

| concern | measure |
|---|---|
| Token in config/VCS | Env only. Literal keys rejected (§7.1). |
| Token in errors/logs | `Credentials` is a struct with an unexported field and a `String()` that prints `[REDACTED]`. Every error string is passed through `redact`, which removes the token, the email, and the base64 `email:token` value. Response bodies in errors are truncated to 512 bytes and redacted. |
| Token to a foreign host | Request URL built only from `base_url` (§6.1). No redirects (§8.1). `https` only, except loopback (§7.1). |
| Token in child processes (F1) | `execution.ScrubbedEnv` gains the names from `jira.token_env`, `jira.email_env` **and `decision.jev.api_key_env`**, which is the same existing gap. They are removed for every provider CLI and for the test command. Git keeps the full environment as today, because git never runs model code before the fingerprint check. The list is still exact names, never prefixes (as `env.go` requires). |
| Ticket text as prompt injection (F2) | Accepted, the same as typed text (§18 Q6). Mitigations: the full rendered task is printed before the decision (§13). `run.json` and `ticket.json` keep it verbatim. Authority is unchanged: no step gains anything because the task came from Jira. The implement guard, clean-worktree rule and sandbox apply as always. |
| Ticket text sent to third parties | The rendered task goes to the decision provider (Jev or an LLM) and the translation provider, the same as typed text. Wiki mentions render as `[~accountid:…]`, which are opaque ids, not names. This goes in the README's "What is not protected" list. |
| Oversized/hostile response | 1 MiB read cap, strict decoding (§8.2), `max_bytes` on the rendered task. |

## 13. Console output

```
Validating configuration...
✓ configuration valid

Fetching PMC-2789 from yourcompany.atlassian.net...
✓ PMC-2789 (Bug) fetched in 0.3s

Task (from PMC-2789):
  | PMC-2789: FE[catalog]: Tags/Labels disable reason not shown on hover in the editor
  | Type: Bug
  | Parent: PMC-2663 — Tags & Labels
  | ...
  | Operator notes:
  | only the FE part; leave pim-qa alone

Run: 20260930-101204-a1b2c3d4  (.orchestrator/runs/20260930-101204-a1b2c3d4)
Deciding the workflow...
```

- The task block is always printed in full, in every mode, `--mode auto` included, so it
  shows up in CI logs. It reuses `logging.NewPrefixWriter(…, "  | ")`.
- Moved issue: `Warning: PMC-1 has moved to NEW-9; using NEW-9`.
- Golden file for the block: `go test ./internal/logging -update`.

## 14. Errors and exit codes

A new `apperr.JiraError{Key, Kind, Status, Attempts, Body, Err}` with `Error()` phrased for
the operator:

| kind | cause | message (abridged) | exit |
|---|---|---|---|
| `auth` | 401 | `Jira rejected the credentials in JIRA_EMAIL/JIRA_API_TOKEN` | 2 |
| `not_found` | 404, or 403 on an issue | `PMC-2789 does not exist, or the account behind JIRA_EMAIL cannot see it` (F7) | 2 |
| `redirect` | any 3xx | `Jira answered with a redirect to <host>; check jira.base_url` | 2 |
| `unavailable` | 5xx/429/transport, after retries | `Jira is unavailable (503 after 2 attempts)` | 3 |
| `timeout` | per-attempt or run deadline | `Jira did not answer within 10s` | 3 |
| `invalid_response` | §8.2 violations, >1 MiB body | names the JSON path | 3 |
| `cancelled` | Ctrl-C | — | 5 |

`exitcode.FromError` gets a `JiraError` case, mirroring `DecisionError`. A fetch error is
returned directly, because no run exists yet to `failRun` (J2).

## 15. Documentation changes (last task)

- **MVP.md** §2 (a second permitted HTTP integration: the Jira REST API, read-only, one
  GET per run), §4 (`--jira`), §5 (the `jira` section), §6 (its validation), §7 (env
  check only with `--jira`), §21 (`task.source`, `ticket.json`), §24 (`JiraError`), §27
  (the task block), §30 (replace "GitHub PR integration / GitLab integration" with
  "GitHub/GitLab integration; Jira beyond reading one issue for `run --jira` (§39)"), and a
  new **§39 Task from a Jira ticket** as the normative summary of this spec.
- **CLAUDE.md**: the decision-flow diagram gains the optional fetch step. The invariants
  gain "Jira: read-only, one GET before the run exists, never on resume, credentials
  env-only and scrubbed from child processes". The Jev key bullet mentions the scrub.
- **README**: usage, Cloud vs DC token setup, what is sent where, the "not protected"
  line from §12.
- **orchestrator.example.yaml**: the commented `jira` section.

## 16. Tests

No network, no real Jira. `httptest` servers only.

- **`internal/jira` unit tests**
  - `ParseRef`: keys, lower case, URLs with matching and mismatched host/port/scheme, a DC
    context path, `/browse/KEY/`, query and fragment, rejected board/JQL URLs, invalid keys.
  - Client: Basic and Bearer headers, the exact `fields=` query, a 200 with a
    `null` description, custom field string/null/object, a moved issue, and each error
    kind in §14. Also 429 with `Retry-After` (seconds and date), `Retry-After` > timeout,
    a 5xx retried then succeeding, `max_retries: 0`, cancellation not retried, redirect
    refused (same host and cross host), a 1 MiB+ body, and the token, email and base64
    value absent from every error string.
  - `Render`: golden files for a full ticket, a minimal one, no description, notes, `\r\n`,
    and the `max_bytes` refusal.
- **config**: section shape, https/loopback, literal secrets, `email_env` vs `auth`,
  field id and label rules, defaults, and the env check only with `Jira: true` and in
  `validate`.
- **execution**: `ScrubbedEnv` removes the configured Jira and Jev variable names and
  nothing else. Table-driven on `scrub`.
- **persistence**: `task.source` round-trips. Load rejects an unknown `kind`, a bad key and
  a zero `fetched_at`. Old runs without `source` still load.
- **integration** (`test/integration`, fake Jira on `httptest` loopback, fake executables):
  1. `run --jira PMC-1 --mode auto`: every prompt recorded in `FAKE_ARGS_DIR` contains
     the rendered task. `run.json` has `task.source` and `ticket.json` exists. Exit 0.
  2. With operator notes: the notes are last in the prompt.
  3. 404: exit 2, no run directory created, no provider called.
  4. Missing `JIRA_API_TOKEN`: exit 2 before any HTTP request (the fake server counts 0
     requests) and before any provider call.
  5. `--jira` with a foreign-host URL: exit 2, 0 requests.
  6. `resume` of a Jira run: 0 requests to the fake server, and no `jira` env needed.
  7. The test command's environment (recorded by a fake test script) has no
     `JIRA_API_TOKEN` / `JEV_API_KEY`.
  8. `run` without `--jira` and with a `jira` section but no env vars: works as today.

## 17. Implementation plan and estimate

Task files: [`tasks/`](tasks/), indexed in [`README.md`](README.md).

| task | title | size | depends on |
|---|---|---|---|
| J01 | `jira` config section, validation, `DependencyOptions` | S–M | — |
| J02 | `internal/jira` client: request, retries, validation, `JiraError`, redaction | M | J01, J05 |
| J03 | `ParseRef`, `Render`, golden files | S | J05 |
| J04 | child processes lose the Jira and Jev credential variables | S | J01 |
| J05 | `domain.Ticket`, `TaskSource`, load validation, `ticket.json` | S | — |
| J06 | `run --jira`: CLI, `taskFromJira`, console output, exit codes, resume line | M | J01–J05 |
| J07 | integration scenarios 19a–19h (§16 items 1–8) | M | J06 |
| J08 | MVP.md §39 and the amendments, CLAUDE.md, README, example config | S | all |

```
J05 ──┬── J02 ──┐
      └── J03 ──┤
J01 ──┬── J02   ├── J06 ── J07 ── J08
      └── J04 ──┘
```

Estimate: 5.5–6 engineering days. Each task ends green on `go build ./...`,
`go vet ./...`, `go test ./...`, and `gofmt -l .` empty. Existing integration scenarios
1–18 keep passing unchanged.

## 18. Decisions

Answered by the operator on 2026-09-30:

| # | question | decision |
|---|---|---|
| Q1 | How is a ticket named? | `run --jira <KEY\|URL> ["notes"]`. Explicit flag, no auto-detection of keys in task text. |
| Q2 | How is it fetched? | Direct REST, read-only, `net/http`, the same pattern as Jev. Not an external CLI, and not an LLM with a Jira MCP (that would need network plus tools in one call and would put a model between the ticket and the task). |
| Q3 | Which deployments? | Cloud (Basic: email + API token) **and** Data Center (Bearer PAT), via `jira.auth`. |
| Q4 | What content? | Summary + description + parent/epic summary. Plus issue type (default D1). |
| Q5 | Write back to Jira? | No. Read-only. |
| Q6 | Trust of ticket text in `--mode auto`? | Same as typed text: shown in full, persisted verbatim, no extra gate. |
| Q7 | Extra fields (e.g. Acceptance Criteria)? | Configurable list of text custom fields with labels. |

Defaults chosen without asking (change before J01 if wrong):

| # | default | alternative |
|---|---|---|
| D1 | Include the issue type line (`Type: Bug`). One word, and it helps the decision tell a fix from a feature. | Leave it out. |
| D2 | Over `max_bytes`: **refuse** with exit 2. | Truncate with a marker and a warning. |
| D3 | Wiki markup passed through verbatim. | Convert to Markdown (more code, lossy either way). |
| D4 | REST v2 on both Cloud and DC. | v3 + an ADF renderer on Cloud. |
| D5 | Scrub `decision.jev.api_key_env` from child processes in the same change (J04), since it is the same gap. | A separate fix. |
| D6 | DC "Epic Link" not resolved. On DC only a sub-task's parent is shown. | `jira.epic_link_field: customfield_N` plus a second GET for the epic's summary. |
| D7 | A fetch failure creates no run directory. | Create a failed run for the record. |

## 19. Deferred

- Outcome comment and optional transition at the end of a run (Q5 follow-up). It needs a
  write-scoped token, an explicit flag, and a decision on what is safe to post (the final
  answer can contain repository content).
- Comments (last N, capped) and linked-issue summaries as optional sections.
- Fencing the task block in action prompts with `action.fence`, for typed and Jira tasks
  alike. That is a prompt change for every run, so it belongs in its own change.
- The DC Epic Link (D6).
