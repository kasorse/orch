# S02 — Gap warnings

**Size** S (0.5 d) · **Depends on** S01 · **Blocks** S04

## Goal

A pure function names the stages a selection skips that a selected stage normally builds on
(`SPEC.md` §8). It never changes what runs.

## Files

- `internal/workflow/gaps.go` (new) + `gaps_test.go`

## Steps

1. `Gaps(wf domain.Workflow) []string`: returns warning lines in `StageOrder` order. Imports
   only `domain`.
2. Rules (code, not config): `planning` without `analysis` → warn; `development` without
   `planning` → warn (and name `analysis` only if planning is also absent: one line per stage,
   not per missing predecessor); `testing` and `review` never warn. Research is never a
   predecessor.
3. Wording as in §8.

## Acceptance

- Table test over all 63 non-empty subsets is overkill; cover: each stage alone, planning+
  development without analysis, the full chain (no warnings), `testing`+`review`
  (no warnings), `review` alone (none).
- Result is deterministic and has no duplicates.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03. `workflow.Gaps(wf)` as specified. Development with neither a plan nor
analysis gets one line naming both; with analysis but no plan, a line saying it works from the
task text and the analysis. The console prefixes each line with `Warning: `
(`logging.GapWarning`).
