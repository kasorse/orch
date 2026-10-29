---
id: JL01
repo: orche
depends_on: []
jira: none
---
# JL01 — internal/jira: search, comments, /myself, the discussion section

**Size** M

## Goal

The Jira reads the listener needs (SPEC.md §3.1, §7.1), in `internal/jira` with its rules
(unexported wire types; imports `domain`, `apperr` and the standard library only). Only
additions: `run --jira`, `story` and their tests behave exactly as before.

## Files

- `internal/jira/search.go`: `(*Client).Search(ctx, jql string, max int) ([]domain.TicketHead, error)`.
  Cloud (`auth: basic`): `GET /rest/api/2/search/jql` paged by `nextPageToken`; Data Center
  (`auth: bearer`): `GET /rest/api/2/search` paged by `startAt`. Fields
  `summary,status,labels,components,reporter,issuetype`; strict validation in the §39.4 style
  (labels at most 100 strings, reporter `accountId` on Cloud or `name` on DC); 1 MiB per page;
  at most `max` issues; the same retries, redirect refusal and redaction as `Fetch`.
- `internal/jira/comments.go`: `(*Client).Comments(ctx, key) ([]domain.TicketComment, error)`
  (`GET /rest/api/2/issue/{key}/comment?orderBy=created`, paged, at most 100; `{id, author,
  created, body}`) and `(*Client).Myself(ctx) (string, error)` (`GET /rest/api/2/myself`: the
  `accountId` on Cloud, the `name` on DC).
- `Fetch` fills `status`, `labels` and `reporter` in `domain.Ticket`; none of them is rendered.
- `internal/jira/jql.go`: `BuildJQL(projects, label, statuses, reporters []string) string`, the
  query of SPEC §3.1 with every value in double quotes; a value holding `"`, `\` or a control
  character panics (config validation, JL02, refuses it first).
- `Render` gains the "Discussion since the orchestrator's questions" section (SPEC §7.1), from a
  new `RenderOptions{Comments, Bot}` or an extra argument; existing callers pass none and their
  output is byte-for-byte unchanged (the goldens prove it).
- Tests and goldens next to the existing ones (`go test ./internal/jira -update` for new goldens
  only; read the diff).

## Acceptance

- Search pages through both endpoints against an `httptest` server and stops at `max`; a page over 1 MiB, a wrong field type and a 3xx are errors of the same kinds and redaction as `Fetch`.
- `Comments` pages and keeps order; `Myself` returns the accountId on Cloud and the name on DC; a missing field is `invalid_response`.
- `BuildJQL` output for a fixed input equals the SPEC §3.1 shape; a value with `"`, `\` or a newline panics.
- The discussion section holds the bot's last comment starting `[orchestrator]` and every later comment, without author names; no such comment gives no section.
- The rendered size including the discussion is checked against `max_bytes` as before.
- Every existing `internal/jira` golden is unchanged; `internal/jira` still imports only domain, apperr and the standard library.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira-listener/`, nor MVP.md, CLAUDE.md, TODO.md, README.md
(JL06 documents §39's new endpoints).
