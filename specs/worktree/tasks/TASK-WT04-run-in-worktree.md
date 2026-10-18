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

## As built

2026-10-17. Code, unit and integration tests are in; `scripts/check.sh` (root and `bench/`) is
left to the operator, so the task is not closed in the README or TODO.md Q4 yet.

Closed 2026-10-18: `scripts/check.sh` (root and `bench/`) passed outside the sandbox.

- `enterWorktree` (`internal/app/worktree.go`) runs in `executeWorkflow` for a run with no
  executions, after approval and before the restore point. It keys off `run.json:
  options.isolation` and the approved workflow, not the config. A workflow without `implement`
  has its `options.isolation` rewritten to `inplace` (O3), so the record says where it ran.
  `resume` now applies the clean-start rule with the run's recorded isolation too (§11), so it
  agrees with `executeWorkflow`.
- `openWorktree` refuses an existing branch or path before `git worktree add`, and maps git's
  own refusal of a branch created in between to the same message. `run.json: worktree` is saved
  right after git creates it, before the pointer check and the fingerprint comparison, which can
  still fail the run. git wrote the `.git` file just then, so when it names the admin directory
  through other symlinks than `rev-parse` does, its spelling is recorded. A file that names
  another directory (or a relative path, `worktree.useRelativePaths`) fails the run before any
  step.
- `switchToWorktree` sets `s.repo`, `s.dispatcher.Repo`, `RunsDir`/`runsRel` (`""`) and
  `s.worktree`. The protected config pattern is left as `newSession` computed it, relative to the
  operator's repository, which is the path its copy has in the worktree.
- The guard's expected pointer is `RepoGuard.GitPointer` (the recorded `admin_dir`). `State`
  carries it as `pointer` (omitted in place, so older baselines load unchanged), and `Changed`
  falls back to it, so `CheckBaseline` and the verify server check the pointer from the
  before-state alone. A guard and a snapshot that name different pointers count as a violation.
- `Sandbox.ReadOnlyFiles` (`ro_files`) is the worktree's `.git` file, symlink-resolved.
  `verify.Spec` checks it is absolute like the other sandbox paths.
- Until WT06, a worktree run makes no §42 commit offer. It ends by printing `The changes are
  uncommitted in the worktree <path>`. Until WT07, `resume` refuses a run that has a worktree
  (`RunNotResumable`, naming its path and branch) rather than continuing it in the operator's
  tree. WT07 replaced that refusal with the resume in the worktree.
- The fake provider gained `FAKE_CWD_DIR`, which records each call's working directory. The
  integration config helper gained `isolation` (default `inplace`, as before).
- Not done here: `bench/configs/*.yaml` do not pin `execution.isolation: inplace` yet (WT10). With
  this task they run `implement` in a worktree, so the bench's final check of the workspace tree
  would not see the change until they are pinned.
