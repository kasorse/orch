# WT04 — `run` in a worktree

**Size** L (3 d) · **Depends on** WT01, WT02, WT03 · **Blocks** WT05, WT06, WT07, WT09

## Goal

A fresh `run` whose approved workflow contains `implement`, under `isolation: worktree`, creates
the worktree after approval and runs every step there; the operator's tree is untouched
(`SPEC.md` §7, §8.3, §12). Setup (WT05) and the end of the run (WT06) come later: until then a
worktree run ends with its changes uncommitted in the worktree, and prints the path.

## Files

- `internal/app/worktree.go`: `openWorktree(ctx, s)` — base and base branch from the operator's
  repository; derived path and `orchestrator/<run-id>`; fingerprint before and after
  `AddWorktree`; `run.Worktree` saved at once; returns the worktree's `*repository.Repository`
- `internal/app/app.go` (`executeWorkflow`, for a run with no executions): after approval and
  before `recordRestorePoint`, call `openWorktree` and switch `s.repo`, `s.dispatcher.Repo`,
  `s.runsRel` (now `""`) and the protected config pattern (the operator-relative path, §7);
  `recordRestorePoint` takes `base_ref` with no snapshot; `testSandbox` runs on the worktree
- `internal/guard/guard.go`: `RepoGuard.GitPointer` (the expected `.git` content, empty in
  place); `Changed` compares it with `repository.ReadGitPointer` **before** `GitFingerprint`; a
  mismatch is `.git (worktree pointer)`; `State` carries it so `CheckBaseline` checks it too
- `internal/testexec/sandbox.go`: `Sandbox.ReadOnlyFiles` — the worktree's `.git` file, bound
  `--ro-bind` after the repository bind (mount order is load-bearing; comment it)
- `internal/logging`: the `Worktree:` line after approval (§13)

## Acceptance

- Unit: the guard reports `.git (worktree pointer)` for a rewritten pointer without running git
  (an executor that fails on any call proves it); bwrap argv binds the `.git` file after the
  repository.
- Integration (fake executables): worktree run with implement + test; the fake implement writes
  a file; the operator's tree, index and HEAD are byte-for-byte unchanged (a dirty file there
  included); the worktree has the change; `run.json: worktree` is set; the provider's argv shows
  the worktree as its working directory and no runs-directory deny rule.
- A read-only workflow under `worktree` runs in place (O3).
- An unborn HEAD is a config error naming `isolation: inplace`; an existing branch
  `orchestrator/<id>` is refused before any step.

## Gates

`scripts/check.sh`
