---
id: JR05
repo: orche
depends_on: [JR04]
jira: none
---
# JR05 — ParseRef, Render, golden files

**Size** S

## Goal

The operator's `--jira` value becomes a key without any network access, and a validated
`domain.Ticket` plus the operator's notes becomes the task text, deterministically, within
`max_bytes` (`SPEC.md` §6.1, §9).

## Files

- `internal/jira/ref.go` (new): `ParseRef`.
- `internal/jira/render.go` (new): `Render`.
- `internal/jira/ref_test.go`, `render_test.go`, `internal/jira/testdata/*.golden`.

## Steps

1. Keys are checked with `domain.ValidTicketKey` (JR01); the `ParseRef` tests cover the keys of
   `ticketKeyCases` in `internal/domain/ticket_test.go`.
2. `ParseRef(ref, baseURL string) (string, error)`:
   - Without `://`: trim, upper-case, `ValidTicketKey`.
   - Otherwise `url.Parse`. A value that is not a URL gives `--jira value is not a valid URL`
     without showing the value; a URL with userinfo is refused naming only the hostname. Compare
     the hostname first (ignoring case), then the scheme, then the port (443/80 made explicit),
     so a foreign host is reported as a host mismatch even when its scheme differs too. A scheme
     mismatch says `--jira URL scheme "http" does not match jira.base_url's "https"`; a host or
     port mismatch uses `SPEC.md` §6.1's wording and shows `host[:port]` only.
   - The decoded path, with the base's own path prefix removed (a Data Center context path such
     as `/jira`), must be `/browse/<KEY>` with an optional trailing `/`; the key is upper-cased.
     `/browse/PMC-1%2F..%2Fx` is refused. Any other path gives `--jira URL is not an issue link:
     pass the issue key or its /browse/<KEY> URL`. Query and fragment are ignored.
   - Errors are `*apperr.ConfigError`.
3. `Render(t domain.Ticket, notes string, maxBytes int) (string, error)` writes, leaving out
   absent sections:
   ```
   <Key>: <Summary>
   Type: <Type>
   Parent: <Parent.Key> — <Parent.Summary>
   Source: <URL>

   Description:
   <Description>

   <Field.Label>:
   <Field.Value>              (each non-empty field, in config order)

   Operator notes:
   <notes>
   ```
   - Values are verbatim except: `\r\n` becomes `\n` (here too, whatever the input's origin),
     trailing whitespace of each line is trimmed, blank lines at both ends of each section are
     dropped. No markup conversion.
   - A parent with an empty summary is `Parent: <KEY>`; an empty type line is left out.
   - Notes are trimmed as a whole, then cleaned per line; empty notes are left out.
   - The output ends with exactly one `\n`.
   - Over `maxBytes` is a `*apperr.ConfigError`: `PMC-2789 renders to 51203 bytes, more than
     jira.max_bytes (32768); raise the limit or trim the ticket`. Never truncated.

## Documentation

- CLAUDE.md Build & test: golden files of `internal/jira` are rewritten with
  `go test ./internal/jira -update`.

## Acceptance

- `ParseRef` keys: `PMC-1`, ` pmc-1 `, `A_B2-10` pass; `pmc-0`, `PMC`, `1PMC-1` fail.
- `ParseRef` URLs: `https://h/browse/PMC-1`, with a trailing `/`, and with `?focusedCommentId=5#c` pass; a DC base `https://h/jira` accepts `https://h/jira/browse/PMC-1` and refuses `https://h/browse/PMC-1`; loopback, an explicit `:443`, host case and a base with a trailing `/` pass.
- `ParseRef` refusals: another host, scheme or port; a foreign host with a different scheme reported as a host mismatch; a board URL with `selectedIssue=PMC-1`; `/browse/PMC`; `/browse/PMC-1/extra`; an encoded slash; `https://user:pw@h/browse/PMC-1` without `pw` in the error.
- Golden files: `full.golden`, `minimal.golden`, `no_description.golden`, `notes.golden`,
  `crlf.golden`. Every rendering is identical twice, ends with one newline, has no `\r` and no
  trailing whitespace.
- A text exactly at `max_bytes` passes; one byte over fails with the exact message.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira/`.

## As built (2026-10-26)

Built by hand on 2026-09-30, before this spec was rewritten as a story, as task J03 of the old
`specs/jira` plan: commit 94435c9 on `master` ("Parse the --jira reference and render the ticket into the task (J03)"). Marked done by hand on 2026-10-26:
no story run built it.
