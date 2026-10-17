# WT05 — setup: networked sandbox, never after a step

**Size** M (1.5 d) · **Depends on** WT04 · **Blocks** WT10

## Goal

`worktree.setup` runs once in a new worktree, with the network, before the baseline and the
pre-run test, and never on a tree a model has touched (`SPEC.md` §9).

## Files

- `internal/testexec/sandbox.go`: `Sandbox.Network bool` — leaves out `--unshare-net` and keeps
  everything else (mounts, `$HOME` tmpfs, the `.git` file bind); `ProbeArgs` unchanged
- `internal/app/worktree.go`: `runSetup(ctx, s)` — only when `len(run.Executions) == 0` and
  `!run.Worktree.SetupDone`; `sh -c` under the sandbox with `Network: true` (or unsandboxed with
  `test.sandbox: none`), environment `execution.TestEnv()` + `test.env_pass`; a guard comparison
  (pointer, fingerprint, protected paths) after it; logs `logs/setup.{stdout,stderr}.log`;
  `SetupDone` saved on success
- `internal/app/app.go`: the order in `executeWorkflow`: open → setup → restore point → baseline
  → pre-run test
- `internal/persistence`: the `setup` log names

## Acceptance

- Unit: bwrap argv with `Network` has no `--unshare-net` and is otherwise identical.
- Integration: setup writes a file the pre-run test needs → the run passes; setup exits 3 →
  the run fails before any provider call with `worktree setup failed (exit 3)`; setup that
  writes `.git/config` of the shared common dir (unsandboxed, to reach it) fails the run as a
  guard violation; a resume of a run with one execution never runs setup (a marker file proves
  it); a resume of a run with none and `setup_done: false` runs it.

## Gates

`scripts/check.sh`

## As built

2026-10-17. Code, unit and integration tests are in. `scripts/check.sh` (root and `bench/`) run
outside the sandbox is left to the operator, so the task is not closed in the README or TODO.md
Q4 yet.

- `Sandbox.Network` is `json:"-"`: it is set only in Go, on the copy `runSetup` builds, and never
  serialised, so a verify.json naming `network` is still rejected as an unknown field and the
  verify server can never get the network.
- `runSetup` (`internal/app/worktree.go`) runs right after `enterWorktree`, which sets
  `s.worktree` the `.git` file bind needs, and before the restore point, the baseline (so the
  baseline includes what setup installed) and the pre-run test. It uses a `testexec.Runner` of
  its own, never `s.runner`. A `SetupStarted` reporter line precedes it.
- Setup runs inside the engine's share of `execution.timeout` (§9): `executeWorkflow` now starts
  `opts.engineContext()` before the worktree block rather than after it, and passes it to
  `runSetup`. A setup that outlives it is killed and fails the run with
  `worktree setup failed (timed out: execution.timeout ran out)`; any other runner error (bwrap
  could not start) with `worktree setup failed: <err>`. The runner keeps no output of a run it
  killed, so `logs/setup.stderr.log` then says why there is none.
- The guard snapshot is taken before setup and compared after it, before the exit code or the
  runner error is looked at, and under `context.WithoutCancel` so the deadline that killed setup
  does not skip it: a violation is reported as one (`action.SetupViolation`, the same text as a
  step's) even if setup also failed or timed out. `setup_done` stays false on any failure.
- Tests: `TestExecuteWorkflow_SetupTimeoutFailsTheRun`, `TestExecuteWorkflow_SetupKilledIsStillGuarded`
  and `TestRunSetup_SandboxedWithTheNetwork` (a recording executor: the bwrap argv has no
  `--unshare-net`, binds the `.git` file and both git directories read-only, runs in the worktree,
  carries `test.env_pass`; `s.runner` keeps no sandbox), besides the acceptance cases above. A real
  bwrap run of setup is not tested (the host may have no bwrap), like the test steps.
- `persistence` needed no change: call-log names are not checked against a list, so the logs are
  `logs/setup.stdout.log` and `logs/setup.stderr.log` through `AppendCallLog`.
- The resume acceptance cases are tested on `runSetup` directly
  (`TestRunSetup_OnlyForARunWithoutExecutionsAndSetupNotDone`): until WT07, `resume` refuses a
  run that has a worktree, so no resume reaches it end to end.
