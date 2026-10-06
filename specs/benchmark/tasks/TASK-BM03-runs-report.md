# BM03 — `orchestrator runs report`

**Size** M (1.5 d) · **TODO** B3 · **Depends on** BM02 · **Blocks** BM14

## Goal

One local report over run directories: how runs ended, what they cost, by complexity and
by stage set, and the existing choice-vs-score comparison (`SPEC.md` §11.3, example §13.12).
It answers B3's "done when": ten runs show cost and success by complexity and stage set.

## Files

- `internal/report/runs.go` (new) + test, golden files under `internal/report/testdata/`
- `internal/report/decisions.go`: unchanged logic, reused as a section
- `internal/app/app.go`: subcommand `runs report [--dir <path>]`; `decisions report` stays
  and prints only the decisions section; usage string; `internal/persistence` loader for
  run directories found recursively under a path

## Steps

1. `report.SummarizeRuns(entries)` over `{RunID, Run, Trace}` (imports only `domain`):
   counts by status; by complexity (runs, completed share, mean cost, decision share of
   cost); by stage set (the workflow's actions joined); totals. `Outcome` from `run.json`
   or, when absent, `domain.ComputeOutcome`.
2. Rendering as plain text, like `RenderDecisions`; then the decisions section.
3. `--dir <path>`: walk recursively, every directory holding a `run.json` is a run (the
   bench layout `runs/<task>/<config>/rep<N>/`); default `.orchestrator/runs/` of the
   current repository. Unreadable runs are listed, not fatal (as today).
4. No config needed, nothing called (same as `decisions report`).

## Acceptance

- Golden: empty dir; mixed statuses; manual runs (complexity `—`); old runs without
  `outcome` and without `usage`; unreadable run listed.
- `decisions report` output byte-identical to before for the existing golden.
- `runs report --dir` over a nested tree finds every run.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty ·
`go test ./internal/report -update` only when the golden output changes on purpose
