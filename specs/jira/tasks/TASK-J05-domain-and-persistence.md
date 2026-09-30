# J05 — `Ticket`, `TaskSource`, load validation, `ticket.json`

**Size** S (0.5 d) · **Depends on** — · **Blocks** J02, J03, J06

## Goal

The domain can represent a fetched issue and where a task came from. `run.json` records
the source and still loads strictly. The run directory keeps an audit copy of the issue
that nothing reads back (`SPEC.md` §8.3, §11).

## Files

- `internal/domain/ticket.go` (new): `Ticket`, `TicketParent`, `TicketField`,
  `ValidTicketKey`
- `internal/domain/task.go`: `Task.Source *TaskSource`, `TaskSource`,
  `TaskSourceJira = "jira"`
- `internal/persistence/filesystem.go`: `SaveTicket`
- `internal/persistence/validate.go`: source checks
- `internal/domain/ticket_test.go`, `internal/persistence/*_test.go`

## Steps

1. `domain.Ticket` as in `SPEC.md` §8.3, with JSON tags (`key`, `requested_key,omitempty`,
   `url`, `type`, `summary`, `description`, `parent,omitempty`, `fields,omitempty`,
   `fetched_at`). `TicketParent{Key, Summary}` and `TicketField{ID, Label, Value}`.
2. `ValidTicketKey(s string) bool` is the one definition of
   `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`. J02 and J03 use it.
3. `TaskSource{Kind, Key, RequestedKey, URL string; FetchedAt time.Time; Notes bool}` with
   tags as in `SPEC.md` §11.1. `Task.Source` goes between `description` and `created_at`,
   with `omitempty`.
4. `validateRun`: when `run.Task.Source != nil`, `kind == "jira"`, `ValidTicketKey(key)`,
   `requested_key` empty or valid, `url` parses as an absolute http(s) URL, and
   `!fetched_at.IsZero()`. Messages follow the file's style (`task.source.key "x" is not
   an issue key`). Since `LoadRun` uses `DisallowUnknownFields`, the new fields must be
   declared before any run carrying them is loaded. That is this task, so it goes first.
5. `(*Store).SaveTicket(runID string, t domain.Ticket) error` writes `ticket.json` through
   the existing write-tmp-then-rename `writeJSON`. It is not added to any load path, and
   not added to `action.RunDirPatterns` (it is not a resume input, `SPEC.md` §11.2).
   Document both in the method comment.

## Acceptance

- `NewRun` with a sourced task → `SaveRun` → `LoadRun` gives an equal task.
- A `run.json` written before this change (no `source`) still loads. Keep or add a fixture
  in the persistence tests.
- `kind: "github"`, `key: "pmc-1"`, `key: "../x"`, `url: "not a url"` and a zero
  `fetched_at` → each is `corrupt`.
- `SaveTicket` writes valid JSON equal to the input when decoded.
- `ValidTicketKey` table shared with J03's `ParseRef` cases.

## As built

Done 2026-09-30. As planned, with these differences:

- **Location**: `TaskSource` and `TaskSourceJira` are in `internal/domain/task.go`. The
  ticket types and `ValidTicketKey` are in `internal/domain/ticket.go`.
- **URL check**: `task.source.url` must be an absolute `http` or `https` URL with a host.
  So `file:///etc/passwd` is refused as well as `not a url`.
- **Messages** are prefixed `task.source:` by `validateRun`, e.g. `run.json: task.source:
  key "pmc-1" is not an issue key`.
- **Shared key table**: Go test tables cannot cross packages, so `ticketKeyCases` in
  `internal/domain/ticket_test.go` is the reference list. J03's `ParseRef` tests should
  cover the same keys, which `ParseRef` upper-cases first.
- **Tests**: `fullRun` now carries a source with a moved key and notes. That puts the
  source through the strict round trip in `TestLoadRun_RoundTrip`.
  `TestLoadRun_RefusesCorruptState` gains nine source cases: an unknown field, the kind
  (`github`, empty), a lower-case key, a `../x` key, the requested key, the URL (`not a
  url`, `file://`) and a zero `fetched_at`. `TestLoadRun_WithoutTaskSource` loads a
  hand-written run.json in the shape used before this change. `TestSaveTicket` decodes
  `ticket.json` strictly and compares it with the input.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
