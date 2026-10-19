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

## As built

Code and tests written 2026-10-18 (`internal/app/clean.go`, `clean_test.go`); `scripts/check.sh`
not yet run, so the task is not ticked in `README.md` or TODO.md.

Closed 2026-10-18: `scripts/check.sh` (root and `bench/`) passed outside the sandbox.

- `--older-than` takes `<N>d` or any Go duration; the age is measured from `run.created_at`.
- Only the flat runs directory is walked (`Store.RunIDs`), not nested bench trees.
- The run is reloaded after the lock is taken. A worktree directory already gone is recorded as
  removed without calling git; the single `PruneWorktrees` drops its administrative data.
- Unreadable runs are listed as skipped and do not stop the command.
