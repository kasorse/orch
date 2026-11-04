---
id: EB02
repo: orche
depends_on: [EB01]
jira: none
---
# EB02 — the module path (O6): `go.mod`, every import, `bench/boundary_test.go`

**Size** S

## Goal

SPEC.md §3: the module becomes `github.com/kasorse/orch`. Mechanical, one commit,
nothing else in it. Stacked on EB01 so the import blocks it edits are renamed once.

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

- `go.mod` names `github.com/kasorse/orch` and `grep -rn '"orchestrator/' --include=*.go .` is empty; the commit contains nothing but the rename and the documents that spell the path.
- `go build ./...`, `go vet ./...`, `go test ./...` and `gofmt -l .` (empty) pass; `bench/boundary_test.go` refuses the new prefix and passes (`cd bench && go test ./...`, by hand, noted in the commit if the sandbox skips it).
- CLAUDE.md's bench paragraph, README.md's build section and MVP.md where an import path is quoted spell the new path.
