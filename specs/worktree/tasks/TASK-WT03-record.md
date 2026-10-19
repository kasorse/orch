# WT03 — `run.json: worktree`, path derivation, strict validation

**Size** S (1 d) · **Depends on** — · **Blocks** WT04, WT08

## Goal

A run records its mode and its worktree, and everything about them that a resume uses is
derived and checked, never trusted (`SPEC.md` §6, §8.1, §8.2).

## Files

- `internal/domain/run.go`: `RunOptions.Isolation` (`inplace` | `worktree`; a run recorded
  before this task loads as `inplace`), set at run start from the config and `--inplace`;
  `Run.Worktree *RunWorktree{Path, Branch, BaseRef, BaseBranch,
  AdminDir string; SetupDone bool; Conflicts []string; Removed bool}` with JSON names as in §8.2
- `internal/persistence/worktree.go`: `WorktreePath(stateDir, commonDir, runID string) string`
  (§8.1: 16 hex digits of sha256 of the resolved common dir); `StateDir()` from
  `$XDG_STATE_HOME` or `~/.local/state`
- `internal/persistence/load.go`: on load, `worktree` is checked: `branch` ==
  `orchestrator/<run-id>`; `base_ref` a full SHA; `base_branch` empty or a valid branch name;
  `conflicts` only on `completed`; `removed` not on `running`; `worktree` only with `options.isolation: worktree`. The path and admin-dir checks
  need the repository and live in WT07, where the resume has it.

## Acceptance

- Unit: round trip; each invalid field is a `RunStateError` naming it; a run without `worktree`
  loads exactly as before.
- `WorktreePath` is stable for the same common dir reached through a symlink.

## Gates

`scripts/check.sh`

## As built

2026-10-17. Code and unit tests are in; `scripts/check.sh` is left to the operator, so the task
is not closed in the README or TODO.md Q4 yet.

Closed 2026-10-18: `scripts/check.sh` (root and `bench/`) passed outside the sandbox.

- `RunOptions.Isolation` is a string (`domain.IsolationInplace` / `IsolationWorktree`);
  `IsolationMode()` gives `inplace` for a run recorded without it. `runTask` sets it from
  `execution.isolation` and `--inplace`. The per-workflow rule (no `implement` runs in place, O3)
  is decided after approval and belongs to WT04, which overwrites it there.
- `StateDir()` returns `(string, error)` (no home directory is an error); a relative
  `$XDG_STATE_HOME` is ignored.
- `base_branch` is checked with a copy of the repository package's cheap branch-name check
  (`persistence` does not import `repository`); it is not run through `git check-ref-format`.
