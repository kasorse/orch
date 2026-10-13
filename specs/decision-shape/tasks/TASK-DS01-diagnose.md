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

## As built (2026-10-13)

Built as planned, except as listed. `scripts/check.sh` (root and `bench/`) passed on
2026-10-13, run outside the bwrap sandbox.

- **Where.** `report.Shape(entries, target, minRuns)` in `internal/report/shape.go`, rendered
  by `RenderCalibration` after the per-group threshold sections and before the threshold diff.
  `Calibrate` keeps its signature: `app.decisionsCalibrate` fills `Calibration.Shape` and
  `SizeTarget`, so a report built without them prints no shape section.
- **Grouping** is by provider, model and `questions_hash` already (DS05's grouping): traces
  before DS02 have no hash and form one legacy group per model. The skip reasons are
  calibrate's, now in one function (`calibrationSkip`) both use.
- **Size** is computed from the primary distribution with `ComplexityDecision.Size()`, so
  legacy traces, which recorded no size, get one too.
- **Cuts on ties.** The plan put each cut midway between the two sizes at the share. On the
  measured data four runs sit at 1.45 exactly where the complex share falls, and that midpoint
  (1.45) would put all four in complex, 23% instead of 15%. The cut now moves to the nearest
  gap between different sizes (`gapCut`): equal sizes always land in one level.
- **Correlation** is computed for every stage that has a probability in the group, over the
  runs that have one; under 3 runs, or with a constant side, it prints `—`.
- **`--size-target S/M/C`**: three positive integers summing to 100, at most once.
- **Reproduces `SPEC.md` §1.1.** On `.orchestrator/runs` (43 claude/opus runs): sizes 0.47 /
  1.08 / 1.25 / 1.42 / 1.78, interquartile range 0.35, levels 4 / 29 / 10, r(planning) +0.94 and
  r(review) +0.83 marked redundant, cuts medium 1.23, complex 1.48. On `bench/results/go/out`
  (22 claude/haiku runs): interquartile range 0.14, every run medium, cuts refused. The medium
  cut prints 1.23 where the example has 1.24: the midpoint of 1.22 and 1.25 is 1.235, and
  either value separates the same runs.
