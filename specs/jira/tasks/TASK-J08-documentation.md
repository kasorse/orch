# J08 — MVP.md §39 and the amendments, CLAUDE.md, README, example config

**Size** S (0.5 d) · **Depends on** J01–J07

## Goal

The normative docs describe the feature as built. `specs/jira/` becomes the design record,
as `specs/resume/` is for §37 (`SPEC.md` §15).

## Files

- `MVP.md`: new §39. Amend §2, §4, §5, §6, §7, §21, §23/§24, §27, §30, §38.3 (env scrub)
  and §38.5 (not protected).
- `CLAUDE.md`: decision-flow diagram, invariants, run artifacts
- `README.md`: usage, token setup, what is sent where
- `orchestrator.example.yaml`: a commented `jira` section
- `specs/jira/README.md` and `SPEC.md`: status → implemented, "As built" in each task that
  deviated

## Steps

1. **§39 Task from a Jira ticket**: the normative summary of `SPEC.md` §6–§14 (CLI and
   `ParseRef`, config and when credentials are checked, the one GET and its validation,
   rendering, order of operations, persistence, resume never re-fetches, security, errors
   and exit codes). Link to `specs/jira/`.
2. §2: the Jira REST API is the second permitted HTTP integration: read-only, one GET per
   `run --jira`. §30: replace "GitHub PR integration / GitLab integration" with
   "GitHub/GitLab integration; Jira beyond reading one issue for `run --jira` (§39)".
3. §4 `--jira`. §5/§6/§7 the `jira` section, its validation, and the env check only with
   `--jira` (plus `validate`). §21 `task.source`, `ticket.json`. §23/§24 `JiraError`
   kinds and exit codes. §27 the task block and the resume line.
4. §38.3: `ScrubbedEnv` also removes the credential variables named by config
   (`jira.token_env`, `jira.email_env`, `decision.jev.api_key_env`). §38.5: ticket text is
   operator-equivalent input written by anyone with edit rights on the ticket, and it is
   sent to the decision and translation providers.
5. CLAUDE.md: add `→ fetch Jira issue if --jira (internal/jira; failure → exit, no run)` at
   the top of the decision-flow diagram. Add one invariant bullet: "Jira: read-only, one
   GET after the fail-fast chain and before the run exists, never on resume; credentials
   env-only, sent only to `jira.base_url`, redacted, scrubbed from child processes". Add
   `ticket.json` and `task.source` to "Run artifacts". Mention the scrub in the Jev-key
   sentence.
6. README: usage examples. Cloud setup (API token from id.atlassian.com, `JIRA_EMAIL`,
   `JIRA_API_TOKEN`, `auth: basic`) and Data Center setup (PAT, `auth: bearer`). How to
   find a custom field id (`/rest/api/2/field`). The "not protected" line.
7. Example config: the `jira` block from `SPEC.md` §7, commented out, with a one-line
   comment per key.

## Acceptance

- Every CLI flag, config key, error kind and run field that J01–J07 added is described in
  MVP.md and appears nowhere only in code.
- `orchestrator validate` passes on `orchestrator.example.yaml` with the `jira` block
  uncommented and the variables set. Check it by hand, and note the check in "As built".
- No stale "one HTTP integration" statement is left: `grep -n "HTTP" MVP.md CLAUDE.md
  README.md` is reviewed.

## As built

Done 2026-09-30. As planned, with these differences:

- **MVP.md**:
  - **§39** has ten subsections: CLI and `ParseRef`, config and credentials, the request,
    response validation, rendering, order of operations, persistence and resume, security,
    errors and exit codes, and not covered.
  - **Amended**: §2 (two HTTP exceptions), §4, §5 (a commented `jira` block in the example
    config), §6, §7, §21 (`ticket.json` in the tree, `task.source` in the saved list),
    §23, §24, §27 (the ticket block and the resume source line, after the resume
    overview), §30 and §38.5.
  - **Not in the task's list**: §26 (the `jira/` package, and that its wire types stay
    unexported) and §29 (a "Jira scenarios" paragraph like the resume and decision ones).
  - **Env scrub**: the text went into §38.2, not §38.3, because that is where MVP.md
    describes `ScrubbedEnv`.
  - **§27 example**: the run ID uses the short style of the rest of §27, because
    `SPEC.md` §13's `20260930-101204-a1b2c3d4` is not what the code produces (IDs are
    UUIDs).
- **CLAUDE.md**:
  - The intro points to §39 and `specs/jira/`.
  - The Architecture paragraph notes that `internal/jira` keeps its wire types private and
    never imports `config`.
  - The flow diagram gains the `[--jira]` fetch line, and the text under it names
    `app.taskFromJira`.
  - The fail-fast bullet mentions `DependencyOptions`, and the guard bullet mentions the
    credential scrub.
  - The Jev-key sentence now says the key is scrubbed from child processes.
  - The new Jira invariant is its own bullet.
  - Out of scope, the test harness (`FAKE_ENV_DIR`, the fake Jira, the `internal/jira`
    golden files) and run artifacts are updated.
- **README**:
  - Prerequisites and the configuration bullets say the credentials are needed only by
    `run --jira`.
  - The security section mentions the scrubbed credential variables and has a "not
    protected" bullet for ticket text.
  - "What is sent to Jev" notes that the rendered ticket is the request text, and a new
    "What is sent to Jira" section follows it.
  - Usage adds the synopsis line and a `validate` note, plus a new "Taking the task from a
    Jira ticket" section: examples, the config block, Cloud and Data Center token setup,
    finding custom field ids via `/rest/api/2/field`, resume, and failures.
  - A Jira paragraph is added under "Reading a failure", `ticket.json` and `task.source`
    under run artifacts, and the limitations are updated.
- **`orchestrator.example.yaml`**: the commented `jira` block, one comment per key.
  `TestLoad_ExampleConfigIsValid` still passes with it commented out.
- **Checked by hand**: the example config with the `jira` block uncommented, validated
  with `go build` and `orchestrator --config … validate`:
  - with `JEV_API_KEY`, `JIRA_EMAIL` and `JIRA_API_TOKEN` set: `✓ configuration valid`,
    exit 0;
  - with the two Jira variables unset: both named (`jira.email_env names JIRA_EMAIL, which
    is not set`, `jira.token_env names JIRA_API_TOKEN, which is not set`), exit 2.
- **HTTP review**: `grep -n "HTTP" MVP.md CLAUDE.md README.md` was reviewed. The remaining
  mentions are status codes, the Jev client, and the three places that now say the HTTP
  integrations are Jev and one Jira read (MVP.md §2 and §30, CLAUDE.md out of scope,
  README limitations).
- **Status**: `specs/jira/README.md` and `SPEC.md` now say **implemented**.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
