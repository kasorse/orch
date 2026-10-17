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
