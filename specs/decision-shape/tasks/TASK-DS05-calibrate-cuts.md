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
