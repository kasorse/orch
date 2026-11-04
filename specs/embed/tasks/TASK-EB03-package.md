---
id: EB03
depends_on: [EB01, EB02]
---
# EB03 — the public package `orchestrator`: `Listen`, `Embedded`

**Size** M

## Goal

SPEC.md §4: one package at the module root, two exported functions, the documentation of §4.3
in its doc comment.

## Files

- `embed.go` (package `orchestrator`, module root): `Options`, `Listen`, `Embedded`. Imports:
  `internal/app`, `internal/exitcode`, the standard library. `Listen` builds the argv and calls
  `app.Run` with `Dir` passed to `app.listen` (EB01's parameter; `app.Run` gets an unexported
  way to carry it, not a flag). `Embedded` runs `app.Run` with `signal.NotifyContext` as
  `cmd/orchestrator/main.go` does, and maps the error with `exitcode.FromError`.
- `cmd/orchestrator/main.go`: unchanged, or reduced to `Embedded` plus the usage error when it
  returns false, if that is shorter and prints the same.
- `embed_test.go`: `Listen` with a missing configuration → `ConfigError` before any request
  (an `httptest` server that must not be hit); `Embedded` false for an unknown word, the CLI's
  exit code for `version`, and `git-credential get` against the fake remote of
  `test/integration` (or the unit test of `runGitCredential`, if the fake is not reachable from
  the root package without a cycle).
- A test that the package imports nothing but `internal/app`, `internal/exitcode` and the
  standard library (as `internal/story/imports_test.go`).
- Docs in the same task: CLAUDE.md summary list (a line for this spec), MVP.md §53 skeleton
  (the package and `Embedded`; EB04 completes it), README.md: one paragraph "As a library".

## Acceptance

- `go doc .` shows the two functions and `Options` with the §4.3 statements.
- The tests above; `scripts/check.sh` passes.
