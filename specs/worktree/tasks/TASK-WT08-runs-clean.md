# WT08 — `runs clean`

**Size** S (1 d) · **Depends on** WT03 · **Blocks** WT10

## Goal

`orchestrator runs clean [--older-than 7d] [--dry-run]` removes old worktrees safely
(`SPEC.md` §14).

## Files

- `internal/app/app.go`: the `runs clean` subcommand next to `runs report`, its usage line
- `internal/app/clean.go`: for every run in the runs directory with a worktree not removed, older
  than the limit (`created_at`), whose lock `Store.Lock` can take: `RemoveWorktree` (no
  `--force`); refused → listed as kept with git's reason; done → `worktree.removed: true` saved;
  then `PruneWorktrees` once. `--dry-run` lists and changes nothing.

## Acceptance

- Unit/integration: an old completed run's worktree is removed and recorded; a locked run is
  skipped; a worktree with an uncommitted file is kept and listed; no branch or ref is deleted;
  `--dry-run` changes nothing on disk.

## Gates

`scripts/check.sh`
