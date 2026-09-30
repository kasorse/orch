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

_Filled in when the task is done._

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
