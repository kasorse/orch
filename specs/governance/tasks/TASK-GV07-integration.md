# GV07 — integration scenarios 47–56

**Size** M (2–3 d) · **TODO** — · **Depends on** GV01–GV06 · **Blocks** GV08

## Goal

Scenarios 47–56 of `SPEC.md` §12 run end to end with fake executables, a fake Jev and the
fake git fixture. They need no network and no real CLI.

## Files

- `test/integration/scenario47_budget_test.go` … `scenario56_explore_test.go`
- `internal/testutil/fakeexec`: the fake claude can report a configurable `total_cost_usd`,
  if it cannot already
- an `/etc` path override for the org-config scenarios, through the same parameter
  `FindOrgConfig` takes, never through the real `/etc`

## Steps

Write one file per scenario. Each asserts:
- the exit code;
- `run.json` (`status`, `failure_reason`, `path_rules`, `outcome`, `org_config`);
- `decision.json` (`policy_hash`, `explored`);
- the stderr lines the spec quotes.

## Acceptance

All ten scenarios are green, and none of them touches the network or the real `/etc`.

## Gates

`scripts/check.sh`, also outside the sandbox

## As built (2026-10-13)

Built as planned, except as listed. The tests were written without being able to run anything:
`scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run for this task.
It is the operator's to run, and GV07 is not ticked in `README.md` until it passes. GV08 waits
on that.

- **Files.** `scenario47_budget_test.go` … `scenario56_explore_test.go`, one per scenario
  (54 has two tests: the model refusal and the `protected_paths` union).
- **Cost.** No `FAKE_COST_USD` knob: `streamResult` (scenario 29) already makes the fake claude
  report any `total_cost_usd` through its canned response files, so `fakeexec` has no cost
  change. `configOptions` gained `maxCostUSD` (`limits.max_cost_usd`) and `explore`
  (`policy.explore`).
- **`/etc` override.** `app.SetOrgConfigEtcPath(path)` returns a restore function. `TestMain`
  points the location at a path that never exists for the whole package, so a host
  `/etc/orchestrator/org.yaml` cannot change any integration test; `useEtcOrgConfig` (scenario
  55) puts a file there for one test. It is the same `etcPath` parameter `FindOrgConfig` takes.
- **Fake implement.** `FAKE_IMPLEMENT_TOUCH_FILE` writes a file on every implement call, and
  from call `FAKE_IMPLEMENT_REVERT_ON_CALL` on removes it (scenario 52: the second attempt
  reverts the first). Both are opt-in; no other scenario sets them.
- **Scenario 48** is a `DependencyError` (exit 2), which is where the budget check lives, not a
  `ConfigError`; both exit 2.
- **Scenario 56 and the run id.** A run id is random (`newUUIDv4`) and no seam fixes it. The
  test runs fresh fixtures (at most 30, rate 0.5) until one run was explored and one was not,
  and checks each run's `decision.json` and executions against the draw computed from that
  run's own id, so it never pins an id.
- **Not run.** Nothing here was executed, so the exact messages asserted (the warning, the
  skip line of scenario 51, the guard's path list) are taken from the code, not from a run.

**Gates passed 2026-10-13:** `scripts/check.sh` (root and `bench/`, outside the sandbox) passed over the
whole phase, GV01–GV08, before the merge into `master`.
