# JL01 — internal/jira: search, comments, /myself, the discussion section

**Size** M

## Goal

The Jira reads the listener needs (SPEC.md §3.1, §7.1), in `internal/jira` with its rules
(unexported wire types, imports `domain`, `apperr`, the standard library only).

## Files

- `internal/jira/search.go`: `(*Client).Search(ctx, jql string, max int) ([]domain.TicketHead, error)`;
  Cloud (`auth: basic`) `GET /rest/api/2/search/jql` with `nextPageToken`, Data Center `GET
  /rest/api/2/search` with `startAt`; fields `summary,status,labels,components,reporter,issuetype`;
  strict validation (§39.4 style; labels ≤ 100, reporter `accountId` or `name`), 1 MiB per page,
  at most `max` issues.
- `internal/jira/comments.go`: `Comments(ctx, key) ([]domain.TicketComment, error)` (ordered by
  created, paged, at most 100; `{id, author account, created, body}`), `Myself(ctx)
  (account string, error)`.
- `Fetch` gains `status`, `labels`, `reporter` in `domain.Ticket` (not in the rendered text).
- `Render` gains the "Discussion since the orchestrator's questions" section (SPEC §7.1) from
  `[]domain.TicketComment` and the bot account; existing callers pass none and render as today.
- `internal/jira/jql.go`: `BuildJQL(projects, label, statuses, reporters)`: Go's quoting.
- Golden files and tests; `go test ./internal/jira -update` for the new goldens.
- MVP.md §39.3/§39.4/§39.5/§39.10: the new endpoints and the section, "listener only".

## Acceptance

- Both search endpoints page correctly against a fake server; a body over 1 MiB, a wrong type, a
  redirect are errors with the same kinds and redaction as `Fetch`.
- `BuildJQL` output is fixed by a test; a value with `"` or `\` is impossible (JL02 refuses it)
  and `BuildJQL` panics on one (programmer error).
- The discussion holds the bot's last `[orchestrator]` question comment and all later ones,
  without author names; no such comment → no section; size counts toward `max_bytes`.
- `scripts/check.sh` passes.
