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

## As built (2026-11-01)

- `tools.NewRoot(dir, deny)` resolves the working directory's symlinks once; `Root.Resolve(path,
  forWrite)` checks the lexical path and then the real one (`EvalSymlinks`, or the nearest existing
  parent for a file not created yet), so a symlink inside the root to `.env` or `.git/config` is as
  refused as the path itself. The deny matcher is `projectctx.DenyMatcher`, which also keeps
  `.orchestrator/` out below its top. An absolute path is refused even inside the root: the model
  never learns where the root is, and no refusal message names it.
- `ReadTools(root)`: `glob`, `grep`, `read_file`, sorted by name, each a `Tool{Name, Description,
  Schema, Run}`. Schemas are strict-ready for both APIs (`additionalProperties: false`, every
  property required, optional ones nullable); inputs are decoded with unknown fields refused.
  `read_file` numbers lines like `cat -n` (2000 lines by default, `offset`/`limit`); `grep` is RE2
  with an optional directory or file and a file-name glob; `glob` supports `**`.
- The walk (grep, glob) is `filepath.WalkDir` in Go, not `git ls-files`: no git process can run a
  planted `core.fsmonitor`. It never follows a symlink (a symlinked file or directory is not
  listed), skips `.git` and denied directories without entering them, and checks the context.
- A refusal is a `*refusal` error, which the loops (AT07, AT08) turn into an `is_error` tool result.
- A mutation (the symlink check off) fails four assertions of `TestResolve`/`TestGrep`.
