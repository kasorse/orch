# JL03 — `run --listener-task` and `failure_kind` in `--output json`

**Size** S

## Goal

The child side (SPEC.md §7.2): a run whose task comes from a file the listener wrote, with no
Jira call, and a JSON line that says when a failure was a credential.

## Files

- `internal/app/app.go`: the hidden `--listener-task <file>` (next to `--story-task`, same
  refusals plus `--inplace`); `readListenerTask` with a size cap and strict validation
  (`{v: 1, key, url, fetched_at, description, scrub_env}`; key by `domain.ValidTicketKey`, url
  absolute https on any host, description ≤ 1 MiB).
- `scrub_env` names go to `execution.ScrubCredentials` before any provider is constructed.
- `domain.TaskSource` gains `Listener bool` (`json:"listener,omitempty"`), validated on load.
- `options.jira_write` is empty for such a run whatever the config says.
- `--output json`: `failure_kind` (`auth` for `FailureAuth` and a rejected decision key, else
  `""`), always present.
- MVP.md §50.10 (the field), §39.7 (the source flag); tests in `internal/app`.

## Acceptance

- A run started this way records the source, runs clarify, and makes no HTTP request (a fake
  Jira that fails the test on any request).
- A `jira.write` in the service config writes nothing.
- An invalid task file is a `ConfigError` before any provider call.
- `failure_kind: auth` for a fake claude reporting the wrong `apiKeySource`.
- `scripts/check.sh` passes.
