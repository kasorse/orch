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

## As built

Done 2026-10-06. Differences from the plan, and why:

- **`SummarizeRuns(entries, unreadable)`** takes the unreadable runs too, so the runs section
  lists them; the decisions section that follows gets none, so a broken run is listed once.
- **The stage set is the workflow's actions** joined with ` → ` (`implement → test →
  review`), in workflow order, not the stage names comma-joined of the `SPEC.md` §13.12
  sample: the actions are what ran. Groups are sorted by run count, then by key.
- **Every table has the same columns** — runs, completed (count and share), total cost, mean
  cost, decision share — and a `total` table closes the section. A share with no denominator
  (no runs, or a group that cost nothing) prints `—`, as do a manual run's complexity and a
  run without a workflow.
- **Complexity is the run's `decision.complexity.selected`** (after any edit on the review
  screen), `—` for `source: manual` and for a run that never got a decision.
- **`persistence.LoadRunDirs(root)`** walks with `filepath.WalkDir` (no symlinks followed;
  the root itself is resolved), stops at the first directory holding a regular `run.json`
  (what lies inside a run is its own artifacts), and validates both files with the same
  strict decoders as `LoadRun`/`LoadDecision` (`decodeRun`, `decodeDecision`, factored out).
  A directory named like a run ID must hold that run; any other directory name accepts the
  run's own ID. A bad `decision.json` makes the run unreadable, as in `decisions report`.
- Unreadable runs are listed by their path relative to `--dir`, not by ID: under a bench
  tree the directory, not the ID, says which task, config and repetition it was.
