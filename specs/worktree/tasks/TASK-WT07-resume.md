# WT07 — resume in the worktree

**Size** M (1.5 d) · **Depends on** WT04 · **Blocks** WT10

## Goal

`orchestrator resume` continues a worktree run in its worktree, after checking that the worktree
is the one the run created (`SPEC.md` §11).

## Files

- `internal/app/resume.go`: the mode from `run.Options.Isolation`, never from the config; with
  `run.Worktree` set and not removed: derive the path
  (`persistence.WorktreePath`) and compare with the record; a missing directory →
  `RunStateError` (`the run's worktree <path> is gone; start a new run`); `ReadGitPointer` equal
  to `gitdir: <admin_dir>` and `admin_dir` directly under `<common>/worktrees/`; `ListWorktrees`
  in the operator's repository lists the path on `refs/heads/orchestrator/<id>`; then open the
  worktree's repository and run the existing §37.8 sequence on it; `checkWorktree` is skipped
  for a worktree run
- a run with no executions and no worktree yet goes through `openWorktree` like a fresh run
  when its `options.isolation` is `worktree`

## Acceptance

- Integration: a worktree run fails at test; the operator edits and commits in their own tree
  (dirty, another branch); resume continues in the worktree and completes; the operator's tree
  is untouched by the resume.
- Refusals, each before any git command in the worktree where the check allows: the directory
  removed; the `.git` pointer rewritten; the path recorded differently from the derived one; the
  worktree on another branch.

## Gates

`scripts/check.sh`
