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

## As built (2026-10-13)

Built as planned, except as listed. The code and its tests were written without being able to
run anything: `scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run
for this task. It is the operator's to run, and GV05 is not ticked in `README.md` or TODO.md
G5 until it passes. The four golden files under `internal/report/testdata/calibrate/` were
written by hand, not by `-update`, so a mismatch in spacing is possible: if a golden test
fails, read the diff, and rewrite them with `go test ./internal/report -update` once the
output is what the spec asks for.

- **Signature.** `report.Calibrate(entries, unreadable, minRuns)` returns a `Calibration` and
  `RenderCalibration(w, runsDir, c)` prints it. The task's `Calibrate(entries, runs, minRuns)
  Report` took `runs` for the unreadable directories, as `SummarizeRuns` does.
- **The hash.** `domain.PolicyHash` returns an empty string instead of panicking if the
  policy cannot be encoded (a NaN threshold, which config validation never lets through). It
  is set in `classification.Service.Classify` on the `Result`, so it covers the provider path
  and the user's choice after a provider failure, and both trace builds in `app`. The hash
  covers the Go encoding of `DecisionPolicy` (it has no JSON tags): `TestPolicyHash_PinnedEncoding`
  pins that encoding, because a renamed or added field changes every hash. It pins the
  encoding and checks that the hash is its sha256, not a literal digest.
- **When a stage "ran".** The trace has the stage enabled with a probability and a threshold,
  and the run has an execution of its action (`test` or `review`). A stage that was on but
  never reached because an earlier step failed is not counted. A stage forced on below its
  threshold is in the bucket table and outside the raise window.
- **Paid-off proxies.** `testingPaidOff`: any failed `test_result` after an `implement` (a
  later pass or the failure left exposed). `reviewPaidOff`: a review whose `Decide()` is
  `fail`, or with a medium issue when `options.review_fix_medium` is set, followed by an
  `implement`. A failing review with nothing after it does not count.
- **Raise rule.** The first window `[current, t)` with at least `--min-runs` runs decides: a
  payoff in it means no proposal, since wider windows only add runs. The last window includes
  `P = 1`. Runs decided under different thresholds (possible only in the group of traces
  written before `policy_hash`) print `mixed thresholds` and get no proposal. `lowerProposal`
  is the empty hook for GV06.
- **Comparison and model tables** print the counts and means without a `not enough data`
  mark; only the probability buckets use `--min-runs`. The model table has one row per action
  and route (`implement claude/sonnet`) and counts a trace without `models` under `—`.
- **`decisions report`.** The decider key is `<source> <provider>/<model> <hash12|—>`, and a
  disagreement row gets the short hash after the time. Scenario 22 matches the hash with a
  regular expression, since it depends on the config the fixture writes.

**Gates passed 2026-10-13:** `scripts/check.sh` (root and `bench/`, outside the sandbox) passed over the
whole phase, GV01–GV08, before the merge into `master`.
