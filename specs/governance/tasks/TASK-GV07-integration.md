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
