# DS05 — Cut proposals

**Size** M (2 d) · **Depends on** DS01, DS02 · **Blocks** DS07

## Goal

`decisions calibrate` proposes `policy.complexity.cuts` by target share and by outcome, and
refuses when size does not spread (`SPEC.md` §7.3).

## Files

- `internal/report/shape.go`: group by provider, model and `questions_hash` (not
  `policy_hash`, `SPEC.md` §7.1); the outcome table per size bin; the two proposals
- `internal/report/calibrate.go`: the YAML diff of `policy.complexity.cuts` next to the
  threshold diff
- `internal/app/app.go`: `--simple-max-hard 0.2`

## Steps

1. *Hard* = `fix_iterations > 0` or `status: stopped`. `failed` is not counted (`SPEC.md` §1.1).
2. Share rule: quantiles as in DS01, at least `--min-runs` per level.
3. Outcome rule: the highest bin edge below which the hard share exceeds `--simple-max-hard`;
   `size does not predict outcome` when the hard share does not grow with size.
4. The refusal on an interquartile range below 0.2 suppresses both proposals.

## Acceptance

- Unit: constructed sets for each rule, for the refusal, and for "does not predict"; a group
  split across two `policy_hash` values with one `questions_hash` is one group.
- Nothing is written; the command calls no provider.

## Gates

`scripts/check.sh`

## As built (2026-10-14)

Built as planned, except as listed. `scripts/check.sh` (root and `bench/`) passed, run outside
the bwrap sandbox. One earlier run failed an integration scenario on `failed to run test
command: context deadline exceeded`; the rerun passed. That flake is older than this spec:
master (`b9e9dce`) hit it in 2 of 6 runs of `test/integration` on 2026-10-14 (scenarios 56 and
2), the branch in about 4 of 10. The fixture's `go test ./...` sometimes outlasts the 30 s
`execution.timeout` the integration config gives a whole run.

- **The outcome rule, made exact.** `SPEC.md` §7.3's wording was ambiguous. As built: the medium
  cut by outcome is the highest edge `e` (steps of 0.1) such that the runs with size below `e`
  number at least `--min-runs` and at most `--simple-max-hard` of them were hard. It is refused
  with `size does not predict outcome` when the runs below the median size are hard at least as
  often as the rest, and with `no size keeps N or more simple runs at most X% hard` when no edge
  qualifies.
- **What the diff proposes.** One value per key: the target's cuts, with medium lowered to the
  outcome cut when that is lower. The diff says when it lowered it. It starts from the cuts the
  group's decisions recorded (`mixed` when they differ, `—` before DS02), and compares at two
  decimals, as it prints.
- **Hard.** `fix_iterations > 0` or `stopped`. A `failed` run with no fix iteration is left out of
  the outcome table altogether, not counted as easy: on the measured data it is the environment.
- **Refusal** (interquartile range under 0.2) suppresses both rules.
- **Flags** became one `calibrateArgs` struct (`--runs`, `--min-runs`, `--size-target`,
  `--simple-max-hard`), each at most once.
- **On the measured data** (43 claude/opus runs): about 45% of the runs with an outcome needed a
  fix at every size. With the default `--min-runs 10` the outcome rule finds no simple level
  that easy and the proposal is the target's `{medium: 1.23, complex: 1.48}`; with
  `--min-runs 3` it lowers medium to 0.90, which leaves 5 of 43 runs simple. Size spreads tasks
  for routing, but on this data it does not pick the tasks that will need a fix (SPEC.md §1.1),
  and escalation (DS06) is the lever for those. The example config keeps 1.24 / 1.48.
