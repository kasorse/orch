# GV05 — `policy_hash`, `orchestrator decisions calibrate`

**Size** M (3 d) · **TODO** G5 · **Depends on** — · **Blocks** GV06, GV07

## Goal

Every decision records which policy produced it. A read-only command reads the run history
and proposes testing and review thresholds, with the evidence behind each one. For the other
stages and the models it prints comparison tables only (`SPEC.md` §7.1–§7.3).

## Files

- `internal/domain/trace.go`: `DecisionTrace.PolicyHash`; `domain.PolicyHash(DecisionPolicy)
  string` (sha256 of `json.Marshal`, whose map keys are sorted)
- `internal/classification` and `internal/app`: set the hash when the trace is written; leave
  it absent for `manual`
- `internal/persistence/validate.go`: 64 hex when present
- `internal/report/calibrate.go` (new): `Calibrate(entries, runs, minRuns) Report`, plus a
  renderer
- `internal/app/app.go`: `decisions` accepts `report` and `calibrate [--runs <dir>]
  [--min-runs N]`
- `internal/report/decisions.go`: a `policy_hash` column, grouping by it

## Steps

1. Write `PolicyHash`, then a test that changing one threshold changes the hash and changing
   map order does not.
2. Write the paid-off proxies of `SPEC.md` §7.1 as pure functions over `domain.Run`:
   `testingPaidOff`, `reviewPaidOff`.
3. Group traces by decision `provider`, `model` and `policy_hash`. Skip traces with the
   existing skip reasons.
4. For each stage, compute buckets of 0.1 holding runs and the paid-off rate. Apply the raise
   rule (and the lower rule, once explored traces exist, GV06). Print `not enough data` under
   `--min-runs`.
5. Print the comparison table for research, analysis and planning: same complexity, with or
   without the stage, showing completed share and mean `fix_iterations`.
6. Print the model table: per complexity and model, the runs, completed share, mean
   `fix_iterations` and mean cost.
7. End with the YAML diff of `policy.stages.thresholds`, each line followed by its evidence
   rows.

## Acceptance

- Golden files under `internal/report/testdata/calibrate/`:
  - a raise proposal;
  - not enough data;
  - two decision models kept apart;
  - two policy hashes kept apart.
- Unit: each paid-off proxy on hand-built runs (test failed then passed; review failed then
  implement; neither).
- The command reads only; a test checks the run directory is byte-identical afterwards.

## Gates

`scripts/check.sh`
