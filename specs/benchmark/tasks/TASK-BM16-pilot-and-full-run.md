# BM16 — Pilot, then the full run

**Size** M (mostly waiting; 2–4 days of wall time) · **TODO** B2 · **Depends on** BM13,
BM14, BM15 · **Blocks** BM17

## Goal

B2's "done when": one command runs 30 tasks × the configs (A, B, C, D and B′ — Q10) × 3
repetitions and produces the summary (`SPEC.md` §13.10–§13.11).

## Steps

1. **Pilot** in the container: 5 tasks across languages and categories (incl. one question
   and one ru pair), configs A, B, C, D (+ B′ — Q10), 1 rep, `--max-cost` from a rough guess.
   Fix what breaks (infra errors are bugs in setup, tasks or the runner, not results).
2. From the pilot: cost and time per cell per config → the budget for the full run (`--max-cost`
   with a 30% margin), agreed with the owner before starting.
3. **Full run**: 30 × 5 × 3, sequential (or `-j` if the rate limits seen in the pilot allow).
   Re-run until no `infra_error` is left.
4. `orchbench summarize`; commit `bench/results/<date>-full/summary.md` (only it).
5. `orchestrator runs report --dir <out>/runs` attached to the summary as an appendix.

## Acceptance

- `summary.md` with every section of §10, based on all cells, no `infra_error` left (or each
  remaining one explained).

## Gates

None beyond the commands above; the money spent is recorded in the summary.
