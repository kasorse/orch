---
id: EB03
repo: orche
depends_on: [EB02]
jira: none
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

- `embed.go` at the module root exports exactly `Options`, `Listen` and `Embedded`; a test like `internal/story/imports_test.go` asserts the package imports only `internal/app`, `internal/exitcode` and the standard library.
- Test: `Listen` with a missing configuration returns a `ConfigError` before any request (an `httptest` server that must not be hit); `Listen` with `Options.Dir` runs the listener there (the state directory appears under `Dir`).
- Test: `Embedded` returns false for an unknown first word and the CLI's exit code for `version` and for `git-credential get` with the fake remote's input (or `runGitCredential`'s unit input if the root package cannot reach the fake without a cycle).
- `go doc .` shows the two functions and `Options` with the §4.3 statements; CLAUDE.md's summary list has a line for this spec; MVP.md §53 has the package and `Embedded`; README.md has a paragraph "As a library"; `scripts/check.sh` passes.
