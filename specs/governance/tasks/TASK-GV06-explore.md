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

## As built (2026-10-13)

Built as planned, except as listed. The code and its tests were written without being able to
run anything: `scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run
for this task. It is the operator's to run, and GV06 is not ticked in `README.md` or TODO.md
G5.3 until it passes.

- **Where it is called.** In `app.decideWithProvider`, on `res.Decision` right after
  `Classify`, not in `Run`: `res.Trace` (decision.json), the announced table and `run.json`'s
  decision then all carry the explored stage, with no second copy of the decision to keep in
  step. `decideManual` and `resumeTask` never reach it.
- **The hash.** `DecisionPolicy.Explore` is `json:"-"`, so it is not in `policy_hash`: it
  changes no threshold, and explored runs stay in the calibration group of the thresholds they
  probe. `TestPolicyHash_PinnedEncoding` is unchanged.
- **The draw.** `fnv.New64a` over `seed + "/" + stage`, `% 1000`, compared as a float with
  `rate × 1000`. The tests find a run id below and one above the limit by searching, using the
  same hash, instead of pinning two literal ids: no literal was computed here. Pin two ids
  from a run of the tests if the draw should be frozen against a later change.
- **Review screen.** An explored stage reads `explored` (on) or `explored · off` (deselected);
  the decided-stage table reads `on (explored)`; the plain decision block already prints the
  reason. `Decision.WithEdit` clears `explored` on a stage the operator turns off or forces, so
  an edited decision never trips the persistence rule.
- **Persistence.** `explored` needs `enabled` and not `forced`, in `decision.json` and in
  `run.json`'s embedded decision (which had no decision checks before).
- **Lowering.** `lowerProposal` needs `--min-runs` baseline runs (P at or above the threshold)
  of which at least one paid off, then takes the lowest edge `t` for which the explored runs
  with P in `[t, threshold)` number at least `--min-runs` and paid off at least as often as the
  baseline. Edges below the lowest explored run are not tried, so the proposal is never lower
  than something that ran. Stages forced on below the threshold are not explored and are
  ignored. `Proposal` rendering already prints `From` then `To`, so a lowering reads
  `- testing: 0.50` / `+ testing: 0.40`.
- **Not done: the app-level test.** No test runs `decideWithProvider` with a decision
  provider that answers a chosen probability (the Jev client and the CLI provider cannot be
  injected there), so "run.json and decision.json both record `explored`" and "a `--stage` run
  never does" are covered by construction and by scenario 56 of GV07, not by a test here.
- **Older binaries** reject a `decision.json` with `explored: true` (strict decoding). The
  field is `omitempty`, so only runs that explored are affected.

**Gates passed 2026-10-13:** `scripts/check.sh` (root and `bench/`, outside the sandbox) passed over the
whole phase, GV01–GV08, before the merge into `master`.
