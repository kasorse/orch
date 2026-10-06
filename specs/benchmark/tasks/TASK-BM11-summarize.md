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
