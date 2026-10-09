# QL13 — Integration scenarios

**Size** M (2–3 d) · **TODO** — · **Depends on** QL01–QL12 · **Blocks** QL14

## Goal

End-to-end scenarios with the fake CLI cover every behavior of this phase (`SPEC.md` §17).

## Files

- `internal/testutil/fakeexec/harness.go`: per-call implement responses
  (`implement_<N>.json`, counted in `FAKE_STATE_DIR` like review); an MCP client mode for
  verify; prompts saved per call (`<action>_<N>.txt`) next to the last-call file
- `test/integration/scenario33_*` … (one file per scenario)
- `test/integration/helpers_test.go`: config options for `checks`, `verify`,
  `by_complexity`, `review.fix_medium`, `review.models`, `stop_without_progress`

## Steps

1. Extend the fake CLI:
   - `implement_<N>.json` and per-call prompt files.
   - **MCP mode:** when the argv has `--mcp-config` with an `orch` server and
     `FAKE_VERIFY_CALLS` is set (e.g. `run_tests;run_check:vet;run_tests:TestX`), the fake
     starts the server command from the config, speaks `initialize` and
     `notifications/initialized`, makes the listed `tools/call`s, and saves the responses to
     `FAKE_VERIFY_DIR`.
   - `FAKE_PLANT_HOOK_BEFORE_CALL=<k>` writes `.git/hooks/pre-commit` before call k.
2. Scenarios, one per file:
   - 33 stage context: the plan's files, risks and criteria appear in the implement and
     review prompts; the retry prompt has the diff stat.
   - 34 checks: vet fails → implement fixes → green; `failed_check` recorded; per-check logs.
   - 35 red check before the run: refused; with `--allow-red-tests` it starts and the first
     implement sees the check.
   - 36 verify: calls recorded in `verify_calls`, logs moved, dir removed; run green.
   - 37 verify guard: a hook planted before call 2 → call 2 refused → step fails; the hook is
     never executed (the fake hook writes a marker that must not exist).
   - 38 criteria: an unmet criterion → implement → met → done.
   - 39 fix_medium: review passes with medium → one more implement → done, fix_iterations 1.
   - 40 extra reviewer: the second reviewer's high issue forces a retry.
   - 41 no progress, tree: implement makes no change twice → stopped with the reason.
   - 42 no progress, tests: the same failure twice → stopped.
   - 43 no progress, review: the same issue twice → stopped.
   - 44 baseline names: `--allow-red-tests` with `go test -json`-shaped fake output; the prompt
     splits old and new; the step still fails on exit code.
   - 45 by_complexity: a simple decision → the `--model` from the table; `--stage` → base;
     `decision.json: models`.
   - 46 resume: a run interrupted after review(pass + medium) resumes into implement with the
     option recorded, even when the config changed.

   Use the next free numbers if others landed in the meantime.

## Acceptance

All scenarios are green, and scenarios 1–32 are unchanged and green.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty · `scripts/check.sh`
outside the sandbox
