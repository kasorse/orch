# BM10 — `orchbench run`: cells, results, resume, budget

**Size** M (2 d) · **TODO** B2 · **Depends on** BM07, BM08, BM09 · **Blocks** BM12, BM13

## Goal

One command that runs every task × config × repetition, continues where it stopped, and
never spends more than it was allowed (`SPEC.md` §7.1, §7.3, §9, §13.5, §13.8).

## Files

- `bench/internal/results` + test: result line type (schema 1, all fields of §9 incl.
  `language`, `category`, `lang`, `contributor`), append-only writer, reader, manifest
- `bench/internal/run` + test: planning, scheduling, the cell
- `bench/cmd/orchbench`: the `run` subcommand and its flags

## Steps

1. Load tasks (`--tasks`, `--ids`, `--limit`), refuse any whose hash is not in
   `validated.json`; load configs; `--max-cost` required.
2. Manifest: create `<out>/manifest.json` (the `orchestrator version` output and the
   binary's sha256 — `version` may not name a commit until TODO P9 —, the claude CLI version, configs and their hashes, contributor) or compare with the existing
   one; a different build or CLI → refuse unless `--allow-mixed`.
3. Plan: cells in task order, configs interleaved per task, reps outermost per task
   (`task1: A1 B1 C1 D1 A2 …`); skip cells with a `pass`/`fail` line unless `--redo`;
   `--dry-run` prints the plan and the count.
4. Cell: workspace (BM05) → setup → runner (BM09) → final check (BM07) or answer check →
   copy the run directory to `<out>/runs/<task>/<config>/rep<N>/` → logs → result line →
   remove the workspace (unless `--keep-workspaces`). An infra error is retried once at once,
   then written as `infra_error`.
5. Budget: before each cell, the sum of `cost_usd` in `results.jsonl` (this invocation and
   earlier) is compared with `--max-cost`; at or above it no new cell starts, running ones
   finish, exit 3.
6. `-j N` with a worker pool; the writer serialises lines; order of lines may differ from
   the plan, the reader does not care.
7. Ctrl-C: cancel the context, kill the cells' process groups, write no line for unfinished
   cells, exit 130.
8. Progress line per cell (§13.10) and the total.

## Acceptance

- Planning: order, skip, `--redo`, larger `--reps`, new config on an existing `--out`.
- Manifest mismatch refused; `--allow-mixed` accepted and recorded.
- Budget stop at the right cell with exit 3; a second invocation with a higher limit
  continues.
- Interrupt mid-cell leaves no partial line; the next run redoes that cell.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
