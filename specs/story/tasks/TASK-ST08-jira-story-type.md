# ST08 — `run --jira` refuses a story issue type

**Size** S (0.5 d) · **Depends on** ST02 · **Blocks** ST10

## Goal

An issue whose type is in `story.jira_issue_types` is not run as one task (`SPEC.md` §12.1).

## Files

- `internal/app/app.go` (`runTask`, after `taskFromJira` and before `store.NewRun`): the ticket's
  type checked (case-insensitive); a match is a `ConfigError` (exit 2) with the
  `story plan --jira <KEY>` command; no run is created
- `domain.Ticket.Type` already holds the issue type (`internal/domain/ticket.go`)

## Acceptance

- Integration (fake Jira): an Epic with `jira_issue_types: [Epic]` → exit 2, no run directory;
  the same issue without the setting → an ordinary run.

## Gates

`scripts/check.sh`
