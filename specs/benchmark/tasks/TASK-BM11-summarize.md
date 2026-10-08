# BM11 — `orchbench summarize`

**Size** M (1.5 d) · **TODO** B2 · **Depends on** BM04 (the result line format from BM10 can
be fixed first and shared) · **Blocks** BM12

## Goal

Turn `results.jsonl` into the tables B4 decides on (`SPEC.md` §10, §13.6, example §13.11).

## Files

- `bench/internal/summary` + test, golden files
- `bench/cmd/orchbench`: the `summarize` subcommand

## Steps

1. Read lines; ignore `infra_error` in rates but count them; keep the latest line per cell.
2. Per config: pass rate (mean over reps, min–max of the per-rep rates), cost per task (mean,
   median), cost per solved task, time, LLM calls, fix iterations; failures by `fail_reason`;
   mean `tests_reset`.
3. Per config × category, × language (`--by` grouping ready for B5, default language).
4. Paired vs `--baseline` (default A): per task majority over reps → win/tie/loss; the
   difference of pass rates with a bootstrap 95% interval over tasks (10 000 resamples,
   `--seed`, `math/rand/v2` with a fixed source).
5. Decision overhead of B (share of cost and time, calls per run); B vs C cost.
6. Stage choice (B, B′) vs `expected_stages`: exact match count, per-stage precision and
   recall; complexity distribution.
7. Translation: per `pair` — same stages chosen?, both passed?
8. A language below the threshold (10 tasks) is shown and marked `insufficient data`.
9. Markdown to `<out>/summary.md` or stdout; works on a partial file during a run.

## Acceptance

- Golden output for a fixed `results.jsonl` covering every section.
- Bootstrap: same seed → same interval; known small cases checked by hand.
- Partial input (some cells missing) → the report says how many cells it is based on.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Written 2026-10-07. The four gates above passed in `bench/` (`scripts/check.sh` runs them), and
BM11 is ticked in `README.md`. Differences from the plan, and why:

- **`bench/internal/results` holds the record and the reader only.** BM10 was not built, so the
  schema-1 `Record` of SPEC §9 is defined here, with `Read`/`ReadFile`; BM10 adds the writer and
  the manifest to the same package. `pair` is a new field of the record (SPEC §9, §17.3): the
  task file has it, but the example line had no place for it.
- **The reader is tolerant of a run in progress.** Blank lines are skipped; the latest line per
  task, config and repetition wins (`run --redo` appends). A last line that has no newline and
  does not parse is the line `run` is writing: it is dropped and counted (`Records.Partial`).
  The command copies `Partial` and the number of lines a redo replaced (`Records.Replaced`) into
  `Coverage`, and the header says so when either is non-zero, so a report taken during a run
  states what it left out.
  Any other bad line, a schema other than 1, a missing task, config or repetition, or an unknown
  `result` is an error naming the line. A missing file is a usage error (exit 2), a malformed one
  exit 1.
- **Rates leave out `infra_error`; the cell count does not.** The header says how many cells the
  report is based on, and `partial: N of M cells` when the grid of tasks seen × configs seen ×
  the largest repetition seen has holes.
- **Majority over repetitions is more than half of the non-infra ones** (`passes*2 > n`): two
  repetitions with one pass each are not a pass, so that task is a tie. The pass-rate difference
  uses the per-task rates, so it still counts the half.
- **The bootstrap** resamples the per-task differences with
  `rand.New(rand.NewPCG(seed, 0x6f72636862656e63))` (`math/rand/v2`), 10 000 times, and takes the
  2.5th and 97.5th percentile at index `floor(q*(n-1))` of the sorted means. The seed alone
  decides the interval. A task without a result on either side is left out and counted in the
  table (`tasks without <baseline>`, `tasks without config`). A baseline that has no cell at all
  gives a note instead of a table.
- **Stage choice counts cells, not tasks:** `exact match x/N cells` over the non-infra cells that
  have `expected_stages`; stages are compared as sets in the workflow's order. Precision and
  recall print `—` when their denominator is 0.
- **Translation** takes each pair once from either side's `pair`, English task first (by `lang`).
  B's stages for a side are the set chosen most often over its repetitions, a tie going to the
  lexically smaller list. A pair whose other task has no cell is counted, not tabulated.
- **`--by`** (`language`, `category`, `contributor`) is a flag SPEC §13.6 did not have; the
  category table is always shown, and not repeated under `--by category`. A group below 10 tasks
  is shown with its numbers and marked `insufficient data` (`summary.Options.MinTasks`).
- **Output** is written to `<out>/summary.md` through a temporary file and a rename, so a reader
  never sees half a report. `--stdout` writes nothing to disk. The command needs no program
  (`needs` is empty).
- **Tests:** `internal/summary` has a golden `summary.golden.md` for `testdata/results.jsonl`
  (four tasks, one en/ru pair, every `fail_reason`, an `infra_error`, a replaced line and a
  missing cell; the threshold is 3 there, to show a group both ways). `go test ./internal/summary
  -update` rewrites it. The golden file was worked out by hand from the input, and
  `go test ./internal/summary` checks it against the renderer. Bootstrap cases (every task
  a win, identical sides, one win and one loss) are checked by hand in the same package.
