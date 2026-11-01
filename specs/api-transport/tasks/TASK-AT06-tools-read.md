---
id: AT06
depends_on: [AT01]
---
# AT06 — `tools`: confinement, `read_file`, `grep`, `glob`

**Size** M

## Goal

SPEC.md §5 items 1–6 and the read tools of §5.1: provider-neutral, each tool a JSON-schema
definition plus a Go function over the working directory.

## Files

- `internal/provider/tools/`: `Root` (the working directory, the deny list) with `Resolve(path,
  forWrite)` implementing items 2–4; `read_file` (`path`, `offset`, `limit`, line numbers),
  `grep` (`pattern` as RE2, `path`, `glob`), `glob` (`pattern`); the bounds of item 5; results as
  text with the cut marker.
- Table-driven tests of SPEC §11's confinement list, plus a test that the package imports no
  `os/exec` and no `net`.

## Acceptance

- Every refused case returns an error result, reads nothing and lists nothing it refuses.
- `scripts/check.sh` passes.
