# J03 — `ParseRef`, `Render`, golden files

**Size** S (0.5 d) · **Depends on** J05 · **Blocks** J06

## Goal

The operator's `--jira` value becomes a key without any network access. A validated
`domain.Ticket` plus the operator's notes becomes the task text, deterministically, within
`max_bytes` (`SPEC.md` §6.1, §9).

## Files

- `internal/jira/ref.go` (new): `ParseRef`
- `internal/jira/render.go` (new): `Render`
- `internal/jira/ref_test.go`, `render_test.go`, `internal/jira/testdata/*.golden`

## Steps

1. Key validation uses `domain.ValidTicketKey` (J05), the one definition of
   `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$`, shared with J02's response check and J05's load
   validation.
2. `ParseRef(ref, baseURL string) (string, error)`:
   - If it contains no `://`, trim it, upper-case it, and check it with `domain.ValidTicketKey`.
   - Otherwise `url.Parse`. Scheme, hostname and port (with defaults 443/80 made
     explicit) must equal `baseURL`'s. The path, with the base's own path prefix removed
     (DC context path, e.g. `/jira`), must be `/browse/<KEY>` with an optional trailing
     `/`. The query and fragment are ignored.
   - Errors are `*apperr.ConfigError` with the messages in `SPEC.md` §6.1. The error text
     shows the host, never the full URL, in case it carries userinfo.
3. `Render(t domain.Ticket, notes string, maxBytes int) (string, error)`. Sections are
   written in this order, and absent ones are omitted:
   ```
   <Key>: <Summary>
   Type: <Type>
   Parent: <Parent.Key> — <Parent.Summary>
   Source: <URL>

   Description:
   <Description>

   <Field.Label>:
   <Field.Value>              (for each non-empty field, in config order)

   Operator notes:
   <notes>
   ```
   - Values are verbatim except for `TrimRight` of each line's trailing whitespace and
     trimming blank lines at both ends of each section. No markup conversion.
   - `notes` are trimmed, and when empty they are omitted.
   - The output ends with exactly one `\n`.
   - `len(out) > maxBytes` → `*apperr.ConfigError`: `PMC-2789 renders to 51203 bytes, more
     than jira.max_bytes (32768); raise the limit or trim the ticket`. Never truncate.

## Acceptance

- `ParseRef` table: `PMC-1`, ` pmc-1 `, `A_B2-10`, `pmc-0` (bad), `PMC` (bad),
  `1PMC-1` (bad); `https://h/browse/PMC-1`, `…/browse/PMC-1/`,
  `…/browse/PMC-1?focusedCommentId=5#c`; a DC base `https://h/jira` with
  `https://h/jira/browse/PMC-1` (ok) and `https://h/browse/PMC-1` (bad); a different host,
  scheme or port (bad); `…/jira/software/projects/PMC/boards/1?selectedIssue=PMC-1` (bad);
  `https://user:pw@h/browse/PMC-1`, where the error does not contain `pw`.
- Golden files (`go test ./internal/jira -update`): `full.golden` (every section),
  `minimal.golden` (key, summary, type, source), `no_description.golden`,
  `notes.golden`, `crlf.golden`.
- Rendering the same input twice gives byte-identical output.
- A max_bytes boundary test: exactly at the limit passes, and one byte over fails.

## As built

_Filled in when the task is done._

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
