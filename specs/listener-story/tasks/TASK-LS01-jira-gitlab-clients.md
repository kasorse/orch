---
id: LS01
repo: orch
depends_on: []
jira: none
---
# LS01 — `internal/jira` and `internal/gitlab`: links, the link write, the children search, the group's projects

**Size** M

## Goal

SPEC §4 (links in the search), §5.1 (the catalog), §8.3 (the link write), §9 (the children
search): the client calls. Nothing calls them yet.

## Files

- `internal/domain`: `TicketRef{Key, Status}`, `TicketHead.BlockedBy`, `Project{Path, Name,
  Description, DefaultBranch}`.
- `internal/jira/search.go`: `issuelinks` in `searchFields`; `toTicketHead` reads the inward side
  of each link into `BlockedBy` with the link type's name kept for the caller's filter (Cloud and
  Data Center shapes; a link without a status is kept with `Status: ""`).
- `internal/jira`: `Link(ctx, linkType, inward, outward)` (`POST /rest/api/2/issueLink`, 201), the
  write rules of `Create` (never retried once sent, `MayExist`); `Children(ctx, parent, field,
  reporter)` over the search (`parent = KEY` or `cf[<id>] = KEY`, `AND reporter = <id>`), keys only.
- `internal/gitlab`: `GroupProjects(ctx, group)` (SPEC §5.1): paginated GET, strict decoding, a
  project outside the group dropped, the client's read-only/no-redirect/redaction rules.
- Tests: search answers with links (Cloud, Data Center, no status); `Link` 201/400/5xx after
  sending; `Children` JQL for both link kinds; `GroupProjects` two pages, a foreign project, a
  redirect refused, the GET-only assertion still holding.

## Acceptance

- A search answer with an "is blocked by" link gives `BlockedBy` with the blocker's key and status.
- `Link` is never sent twice in one call, and a 5xx after sending is not retried.
- `GroupProjects` follows the next page only within `gitlab.base_url`.
- `scripts/check.sh` passes.

## As built (2026-11-13)

Run `093499ef-879a-4d8c-9fb0-b98aab6c045e` in orch: completed, 0 fix iterations, tests passed, no review.
Branch `story/listener-story/LS01` on `spec/listener-story` @ c1987cb, commit ec5c381.
Cost $1.48. Implementation summary: Add Jira issue links, link write, children search and GitLab group projects.
Conflicts with spec/listener-story: none.
