---
id: EB02
depends_on: []
---
# EB02 — the module path (O6): `go.mod`, every import, `bench/boundary_test.go`

**Size** S

## Goal

SPEC.md §3: the module becomes `github.com/kasorse/orch`. Mechanical, one commit,
nothing else in it.

## Files

- `go.mod`: `module github.com/kasorse/orch`.
- Every `.go` file importing `orchestrator/...` (437 at the time of writing): the new prefix
  (`gofmt -l .` must stay empty; `goimports` ordering may change in files where the prefix now
  sorts differently: accept what `gofmt` gives).
- `bench/boundary_test.go`: the prefix it refuses, and its comment.
- `scripts/`, `Dockerfile`, `examples/ci/`: wherever the old path is spelled (grep
  `"orchestrator/`).
- Docs in the same task: CLAUDE.md's architecture paragraph (`bench/` boundary) and MVP.md where
  an import path is quoted; README.md's build section.

## Acceptance

- `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` empty; `cd bench && go test
  ./...` (outside the sandbox, by hand) passes including the boundary test.
- `grep -rn '"orchestrator/' --include=*.go .` is empty.
- `go list -m` prints `github.com/kasorse/orch`.
