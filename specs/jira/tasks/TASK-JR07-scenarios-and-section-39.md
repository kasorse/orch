---
id: JR07
repo: orche
depends_on: [JR06]
jira: none
---
# JR07 — Integration scenarios 19a–19h and MVP.md §39

**Size** M

## Goal

The feature is covered black-box through `app.Run`, with a fake Jira on loopback and the existing
fake executables, with no network and no real token (`SPEC.md` §16). MVP.md gets §39, the
normative summary of the whole feature, which exists only once every task is done.

## Files

- `test/integration/jira_test.go` (new): `fakeJira`, `startJira`, `jiraIssue`, `unsetenv`,
  `assertNoRuns`.
- `test/integration/scenario19_jira_run_test.go`, `scenario19_jira_refusals_test.go`,
  `scenario19_jira_resume_test.go`, `scenario19_jira_scrub_test.go` (new).
- `internal/testutil/fakeexec`: `FAKE_ENV_DIR` dumps `env` to `<dir>/<action>.env`.

## Steps

1. `fakeJira`, in the shape of `fakeJev` (`test/integration/jev_test.go`): `startJira(t,
   issues)` serves canned v2 JSON per key on `httptest.NewServer` (loopback `http`, which JR02
   allows); records the path, query and `Authorization` of every request under a mutex; checks
   Basic auth against the `JIRA_EMAIL`/`JIRA_API_TOKEN` it sets with `t.Setenv`; can fail the
   first requests (`fail ...int`); `Requests()` returns them. `jiraIssue` builds a v2 body (a
   moved issue is a body whose `key` differs); `unsetenv` really unsets a variable after
   `t.Setenv`; `assertNoRuns` checks no run directory exists. The config uses JR02's
   `configOptions.jira`.
2. Prompts are read from `FAKE_PROMPTS_DIR` (set by `decisionEnv`), not `FAKE_ARGS_DIR`, which
   replaces the prompt with `<prompt>`.
3. Scenarios, each under `--mode auto`:
   - **19a** `run --jira PMC-1`, Jev `scriptBugfixMedium`: exit 0; every prompt contains `PMC-1:
     <summary>` and the description; the Jev request body has the rendered text; the exact
     `fields=` query (two custom fields, one `null`); `task.description` equals the expected
     rendering; `task.source.key == "PMC-1"`; `ticket.json` exists; stdout has the whole ticket
     block ending right before `Run:`; neither the token nor the email is in stdout, stderr or
     `ticket.json`; the server saw exactly 1 request.
   - **19b** `run --jira PMC-1 "only the FE part"`: the implement prompt's whole `Task:` section
     ends with `Operator notes:\nonly the FE part`; `source.notes == true`.
   - **19c** 404, **19d** `JIRA_API_TOKEN` unset (the message names it), **19e** `--jira
     https://evil.example/browse/PMC-1`: each through `assertRefusedBeforeRun` - exit 2, no run
     directory, no provider call, no Jev request; 19d and 19e make 0 Jira requests.
   - **19f** a Jira run failed at implement the way `failedAtImplement` does, then `resume <id>`
     with `JIRA_API_TOKEN` empty: it succeeds, the server count stays 1, the second line of the
     resume header is `Task from PMC-1, fetched ...`, the persisted task is unchanged.
   - **19g** `test.command` is `env > "$ENV_OUT"` and `FAKE_ENV_DIR` is set: neither the test
     command nor any provider the workflow calls has a `JIRA_API_TOKEN`, `JIRA_EMAIL` or
     `JEV_API_KEY` line or value, and `PATH` is still inherited. With the `ScrubCredentials` call
     commented out, 19g fails for every one of them (checked once by hand).
   - **19h** a config with a `jira` section and no Jira variables, plain `run "task"`: exit 0, 0
     requests, no `task.source`, no `ticket.json`, no ticket block.
   - **retry** 503 then 200 with `max_retries: 1`: exit 0, 2 requests.

## Documentation

- MVP.md: a new **§39 Task from a Jira ticket**, the normative summary of `SPEC.md` §6–§14 in ten
  subsections: CLI and `ParseRef`, config and credentials, the request, response validation,
  rendering, order of operations, persistence and resume, security, errors and exit codes, not
  covered; it links `specs/jira/`. §29 gains a "Jira scenarios" paragraph like the resume and
  decision ones.
- CLAUDE.md: the intro points to MVP.md §39 with its design record in `specs/jira/`; the test
  harness paragraph names the fake Jira (`test/integration/jira_test.go`) and `FAKE_ENV_DIR`.
- Review `grep -n "HTTP" MVP.md CLAUDE.md README.md`: no statement that there is only one HTTP
  integration is left.

## Acceptance

- 19a–19h and the retry case pass under `go test ./test/integration/...` with the server bound to
  `127.0.0.1` only; `go test -race ./test/integration/...` is clean.
- Scenarios 1–18 pass unchanged: their files have no diff, apart from `helpers_test.go` and
  `fakeexec`.
- Every CLI flag, config key, error kind and run field JR01–JR06 added is described in MVP.md.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira/`.

## As built (2026-10-26)

Built by hand on 2026-09-30, before this spec was rewritten as a story, as task J07 (2d593df) and J08 of the old
`specs/jira` plan: commit 3cadcf9 on `master` ("Document run --jira as built: MVP.md §39, CLAUDE.md, README, example config (J08)"). Marked done by hand on 2026-10-26:
no story run built it.
