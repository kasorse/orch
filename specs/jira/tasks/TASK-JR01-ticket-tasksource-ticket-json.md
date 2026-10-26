---
id: JR01
repo: orche
depends_on: []
jira: none
---
# JR01 — Ticket, TaskSource, load validation, ticket.json

**Size** S

## Goal

The domain can represent a fetched issue and where a task came from. `run.json` records the
source and still loads strictly. The run directory keeps an audit copy of the issue that nothing
reads back (`SPEC.md` §8.3, §11).

## Files

- `internal/domain/ticket.go` (new): `Ticket`, `TicketParent`, `TicketField`, `ValidTicketKey`.
- `internal/domain/task.go`: `Task.Source *TaskSource`, `TaskSource`, `TaskSourceJira = "jira"`.
- `internal/persistence/filesystem.go`: `SaveTicket`.
- `internal/persistence/validate.go`: the source checks.
- `internal/domain/ticket_test.go`, `internal/persistence/*_test.go`.

## Steps

1. `domain.Ticket` as in `SPEC.md` §8.3, with JSON tags `key`, `requested_key,omitempty`, `url`,
   `type`, `summary`, `description`, `parent,omitempty`, `fields,omitempty`, `fetched_at`;
   `TicketParent{Key, Summary}` and `TicketField{ID, Label, Value}`.
2. `ValidTicketKey(s string) bool` is the one definition of `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`. The
   client (JR04) and `ParseRef` (JR05) use it. `ticketKeyCases` in `ticket_test.go` is the
   reference list of keys that JR05's `ParseRef` tests repeat (Go test tables cannot cross
   packages).
3. `TaskSource{Kind, Key, RequestedKey, URL string; FetchedAt time.Time; Notes bool}` with the
   tags of `SPEC.md` §11.1. `Task.Source` goes between `description` and `created_at`, with
   `omitempty`.
4. `validateRun`: when `run.Task.Source != nil`, `kind == "jira"`, `ValidTicketKey(key)`,
   `requested_key` empty or valid, `url` an absolute `http` or `https` URL with a host (so
   `file:///etc/passwd` is refused as well as `not a url`), and a non-zero `fetched_at`.
   Messages are prefixed `task.source:`, e.g. `run.json: task.source: key "pmc-1" is not an
   issue key`. `LoadRun` uses `DisallowUnknownFields`, so the fields must exist before any run
   carrying them is loaded: that is why this task comes first.
5. `(*Store).SaveTicket(runID string, t domain.Ticket) error` writes `ticket.json` through the
   existing write-tmp-then-rename `writeJSON`. It is on no load path and not in
   `action.RunDirPatterns` (it is not a resume input, `SPEC.md` §11.2); the method comment says
   both.

## Documentation

- MVP.md §21: `ticket.json` in the run directory tree (an audit record, never read back) and
  `task.source` in the list of what `run.json` saves, with its fields.
- CLAUDE.md "Run artifacts": `task.source` for a `run --jira` task and `ticket.json` (the fetched
  issue, an audit record never read back).

## Acceptance

- `NewRun` with a sourced task, then `SaveRun`, then `LoadRun` gives an equal task (`fullRun`
  carries a source with a moved key and notes through `TestLoadRun_RoundTrip`).
- A `run.json` in the shape written before this change (no `source`) still loads
  (`TestLoadRun_WithoutTaskSource`).
- An unknown source field, `kind` `github` or empty, key `pmc-1` or `../x`, a bad `requested_key`,
  `url` `not a url` or `file://...`, and a zero `fetched_at` are each refused as corrupt.
- `SaveTicket` writes `ticket.json` that decodes strictly to the input.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira/`.
