---
id: RV02
depends_on: []
---
# RV02 — `internal/jira`: web links, read and write the description

**Size** S

## Goal

SPEC §4 (the links) and §8 (the description): three client methods. Nothing calls them yet.

## Files

- `internal/jira`: `RemoteLinks(key) ([]RemoteLink{URL, Relationship, Title})`
  (`GET …/remotelink`), `Description(key)` (`GET …?fields=description`), `SetDescription(key,
  wiki)` (`PUT /rest/api/2/issue/{key}`, 204); the same no-redirect, strict-decoding,
  redaction, never-retried-once-sent rules as `Comment` (`write.go`).
- `AppendUpdate(old, date, text) string` (pure): the empty line, `Update <date>: <text>`, the
  minimal escaping and the 8 KiB cut of SPEC §8.
- Tests: fake Jira for each call (Cloud and Data Center bodies), golden for `AppendUpdate`
  (`-update`), a description with markup at a line start.
- Docs: MVP.md §39.10 (the description write and the web links read are no longer "not covered",
  for `revise`), §54.6.

## Acceptance

- `RemoteLinks` returns the links of a body like PAY-31's (three `merge request` links).
- `SetDescription` is never sent twice in one call, and a 5xx after sending is not retried.
- `AppendUpdate` of an empty description is `Update <date>: <text>` with no leading empty line.
- `scripts/check.sh` passes.

## As built (2026-11-11)

- `internal/jira/revise.go`: `RemoteLinks`, `Description`, `SetDescription`, `AppendUpdate`, over the
  write-back's `exchange`; `apperr.JiraOpLinks` and `JiraOpDescription` with their headlines.
- `AppendUpdate` escapes nothing (SPEC §8 said "minimally"): the requirement is the operator's
  own text in their own ticket, and on Jira Cloud the backslashes of `jira.Escape` show up
  literally (seen in a listener comment, 2026-11-11), so escaping would garble it. No golden file:
  a table test covers the cases.
- `SetDescription` refuses a text over Jira's 32 767 characters before sending (not in the SPEC):
  cutting the old description to fit would destroy a person's text.
- MVP.md §39.10 and §54.9.
