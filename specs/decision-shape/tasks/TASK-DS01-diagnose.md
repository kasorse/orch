# DS01 — `decisions calibrate`: the shape of the decisions

**Size** S (1 d) · **Depends on** — · **Blocks** DS02, DS05 (through gate O3)

## Goal

Show, on the decisions that exist today, whether the stage answers repeat the size signal and
whether size spreads at all (`SPEC.md` §7.2). Its output is the evidence for gate O3 and the
source of DS02's example cuts.

## Files

- `internal/report/shape.go` (new): `Shape(entries []RunEntry, target [3]float64) []ShapeGroup`,
  a pure function; `size` computed from the primary distribution as in `SPEC.md` §5.1 (the same
  helper DS02 moves into `internal/policy` or `domain`; `report` imports only `domain`)
- `internal/report/calibrate.go`: render the shape section before the threshold proposals
- `internal/app/app.go`: `--size-target 50/35/15` on `decisions calibrate` (three integers
  summing to 100, each > 0)

## Steps

1. Group like calibrate does (`skipOrder`: manual, user, mock and no-shadow are skipped), by
   provider and model. Before DS02 there is no `questions_hash`; the group key leaves it out.
2. Per group: runs; a histogram of size in bins of 0.1; median and interquartile range; the
   level shares under the decision's own policy; Pearson's r of each stage probability with
   size, `redundant with size` at r ≥ 0.8; the quantile cuts for the target (midpoint between
   neighbours), or `not enough data` under `--min-runs` per level.
3. Print `size does not separate tasks` when the interquartile range is below 0.2.

## Acceptance

- Unit: a constructed set where planning = size gives r = 1 and the mark; one with constant
  size gives the refusal and no cuts; quantiles of 20 known sizes match the hand values.
- Run it on `bench/results` and on the local `.orchestrator/runs`: it must reproduce the table of
  `SPEC.md` §1.1, which a one-off script computed before this task (O3 already passed on it).

## Gates

`scripts/check.sh`
