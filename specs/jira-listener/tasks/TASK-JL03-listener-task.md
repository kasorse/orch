---
id: JL03
repo: orche
depends_on: [JL02]
jira: none
---
# JL03 — run --listener-task and failure_kind in --output json

**Size** S

## Goal

The child side (SPEC.md §7.2): a run whose task comes from a file the listener wrote, with no
Jira call, and a JSON line that says when a failure was a credential.

## Files

- `internal/app/app.go`: the hidden `--listener-task <file>` next to `--story-task`, refused with
  `--stage`, `--jira`, `--story-task`, task text and `--inplace`, and outside `run`.
- `readListenerTask`: a size cap (2 MiB) and strict JSON (`{v: 1, key, url, fetched_at,
  description, scrub_env}`; key by `domain.ValidTicketKey`, url absolute https, `fetched_at`
  non-zero, description non-empty and at most 1 MiB, `scrub_env` names matching
  `^[A-Z_][A-Z0-9_]*$`, at most 16).
- The `scrub_env` names go to `execution.ScrubCredentials` before any provider is constructed.
- `domain.TaskSource` gains `Listener bool` (`json:"listener,omitempty"`), validated on load
  (only with kind `jira`); the run records `{kind: jira, key, url, fetched_at, listener: true}`.
- The run builds no Jira client and needs no `jira` section; `options.jira_write` is empty
  whatever the config says; the clarify step runs as in any run.
- `--output json`: `failure_kind`, always present: `auth` for `FailureAuth` and a rejected
  decision key, else `""`.
- Tests in `internal/app` and `test/integration` with the fake executables.

## Acceptance

- A `--listener-task` run records the source with `listener: true`, runs clarify, and makes no HTTP request: a fake Jira server fails the test on any request.
- A `jira.write` in the service's config writes nothing for such a run; `options.jira_write` is empty in run.json.
- An invalid task file (unknown key, bad key, http url, oversized) is a ConfigError, exit 2, before any provider call.
- A `scrub_env` name is absent from the fake claude's recorded environment (`FAKE_ENV_DIR`).
- `failure_kind` is `auth` for a fake claude reporting the wrong `apiKeySource`, and `""` for a red test.
- `run`, `run --jira` and `run --story-task` behave and print as before.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira-listener/`, nor MVP.md, CLAUDE.md, TODO.md, README.md
(JL06 documents §50.10's field and §39.7's flag).
