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

## As built (2026-10-10)

Scenarios 33–46 and the fake-CLI changes are written as planned, except as listed.

- **Gates:** written without being able to run anything, so the four gates (`scripts/check.sh`)
  are the operator's to run, and QL13 stays unticked in `README.md` until they pass. The
  scenarios were checked against the code by reading only. Nothing was run against a real
  `bwrap` or `claude`. A red scenario may expose a bug in QL04, QL05 or QL08–QL10, which were
  written the same way: read the failing assertion against the as-built note of the task it
  tests before changing either side. Scenarios 1–32 were not touched; their only exposure is
  the shared fake CLI and `writeConfig`, whose output for a config that sets none of the new
  options is the text it was before.
- **Fake CLI (`fakeexec.ProviderScript`):**
  - the call counter is now kept for every action (`<action>_calls` in `FAKE_STATE_DIR`, still
    after the `FAKE_FAIL_ACTION` check, so a failed call is not counted), and `implement_<N>.json`
    is preferred over `implement.json` like `review_<N>.json`;
  - with `FAKE_STATE_DIR` set, every call's prompt is also saved to `<action>_<N>.txt`; the
    `<action>.txt` of the last call is unchanged;
  - `FAKE_IMPLEMENT_APPEND_FILE` (not in the task) appends a line to a file on each implement
    call. The no-progress scenarios need an implement that changes the tree without changing
    the verdict (scenarios 33, 34, 42, 43), and the existing fix-on-call copy cannot: it writes
    the same bytes every time;
  - `FAKE_PLANT_HOOK_BEFORE_CALL` / `FAKE_HOOK_MARKER` plant `.git/hooks/pre-commit` before an
    implement call; the hook touches the marker;
  - **MCP client mode** (`FAKE_VERIFY_CALLS`, `FAKE_VERIFY_DIR`) does not hold a conversation.
    POSIX sh cannot read a response and write the next request without a coprocess, so it
    writes the whole conversation (`initialize`, `notifications/initialized`, the listed
    `tools/call`s) to a file and runs the server with that file as stdin. The server answers
    requests in order and exits on EOF, so nothing can hang on a half-read pipe, and
    `timeout 120` bounds it anyway. The server's command and args are read from the
    `--mcp-config` JSON with `sed` (the config is the compact JSON the orchestrator marshals, so
    this is enough for the `orch` server). The calls are made after the call's file edits, like
    a model that edits and then checks.
- **The server is the test binary.** `Dispatcher.executable()` names `os.Executable()`, which
  in `app.Run` under `go test` is the integration test binary, so a new `TestMain`
  (`main_test.go`) answers `mcp-verify` before flags are parsed. The sandbox verify requires is
  the fake bwrap of scenario 16 (`installFakeBwrap`).
- **`configOptions`:** `testSandbox`, `checks`, `verify`, `verifyMaxCalls`, `byComplexity`
  (action → complexity → model), `fixMedium` and `reviewModels`, each rendered only when set;
  `stop_without_progress` was already there and keeps its default of off. `actions:` is now
  rendered by `actionsYAML`, which prints the same text for a config that sets none of them.
- **Shared helpers** are in `quality_helpers_test.go`: `newQualityRun` / `newQualityPreRunRun`
  (a `stageRun` that also records argv), the per-call prompt, a view of `run.json` with the
  quality fields (`loadQL`), and `reviewJSON`.
- **Scenarios** (one file each; a few have more than one test):
  33 stage context · 34 vet fails then is fixed · 35 checks in the pre-run test · 36 verify ·
  37 verify guard · 38 acceptance criteria · 39 `fix_medium` (on and off) · 40 extra reviewer ·
  41 no progress, tree (and the resume into implement) · 42 no progress, tests · 43 no
  progress, review (and a changed issue as progress) · 44 old/new failure split · 45
  `by_complexity` (simple, `--stage`, a level without an entry) · 46 resume after a review that
  passed with a medium issue.
- **35** adds to scenario 26 instead of repeating it: a green check does not hide a red suite,
  a red check with `--allow-red-tests` ends with the check green, and `test.baseline: off` skips
  the checks. Scenario 26 still covers the refusal for a red check.
- **46** gets "interrupted after the review" from `limits.max_steps: 3` (the run stops right
  after the review, before the step back to implement) rather than from editing `run.json`,
  then resumes with a config without `fix_medium` and a higher step limit.
- **37** plants the hook before implement call 2, not call 1, as the task says: call 1 runs the
  tests red so that a second implement happens. The hook is asserted to exist (the scenario
  proves nothing otherwise) and its marker not to.
- **Not done:** summary.md rows for verify and criteria are not asserted (`Reviewers` is, in
  scenario 40); the benchmark configurations are QL15.
