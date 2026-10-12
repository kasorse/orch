# GV06 — `policy.explore`: opt-in stage exploration

**Size** S (1–2 d) · **TODO** G5.3 · **Depends on** GV05 · **Blocks** GV07

## Goal

When `policy.explore` is configured, a stage just below its threshold is turned on for a fixed
share of runs, chosen by the run id. Each explored stage is recorded, so calibration gets the
counterfactual it otherwise lacks (`SPEC.md` §7.4).

## Files

- `internal/config/decision.go`: `PolicyConfig.Explore *ExploreConfig {band, rate}`; the
  validator checks `band` is in `(0, 0.2]` and `rate` is in `(0, 0.5]`
- `internal/domain`: `DecisionPolicy.Explore`; `StageOutcome.Explored`
- `internal/policy/explore.go` (new): `Explore(dec domain.Decision, p domain.DecisionPolicy,
  seed string) domain.Decision`, a pure function that imports only `domain`
- `internal/app/app.go`: call `Explore` with the run id after the decision and before
  `workflow.Build`; never for `manual`, never on resume
- `internal/report/calibrate.go`: the lower rule reads explored stages
- `internal/persistence/validate.go`: `explored` only with `enabled` and not `forced`

## Steps

1. Write `Explore`. For each stage that is off, not forced, and has
   `threshold − band ≤ P < threshold`: if `fnv64(seed + "/" + stage) % 1000 < rate × 1000`,
   turn it on, with `explored: true` and the reason `explored (P …, threshold …)`.
2. Wire it in `app`. The review screen shows explored stages like forced ones, with the
   reason.
3. Implement the lower rule of calibrate.

## Acceptance

- Unit: a fixed seed below the rate turns the stage on; one above it does not. A stage outside
  the band is never turned on. A stage that is on is never turned off. The same inputs give
  the same output.
- `persistence`: `explored` on a disabled stage → corrupt.

## Gates

`scripts/check.sh`
