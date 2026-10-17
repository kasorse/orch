# WT06 — end of run: commit on the run branch, conflicts, removal

**Size** M (2 d) · **Depends on** WT04 · **Blocks** WT10

## Goal

A completed worktree run commits on its own branch (O2), reports conflicts with the operator's
branch, and removes the worktree (`SPEC.md` §10).

## Files

- `internal/app/commit.go`: in a worktree run the §42.6 path is taken as if `auto_commit: true`
  (also under `--mode manual`: the operator's branch is not involved); the branch name in every
  message is `orchestrator/<run-id>`; with a warning and an operator at the terminal, the §42.2
  question with default no; the pointer check, then `CheckBaseline`, before `CommitAll` in the
  worktree
- `internal/app/worktree.go`: `finishWorktree` — `MergeConflicts(base_branch, commit)` in the
  operator's repository after a fingerprint check (skipped without `base_branch`, noted on
  git < 2.38); `RemoveWorktree` after a commit; `worktree.conflicts` / `worktree.removed` saved
- `internal/logging`: §10.2 output; the conflict warning lists at most 20 files and the count
- `internal/report/summary.go`: the worktree line (branch, base, commit, conflicts, kept path)

## Acceptance

- Integration: completed run → commit on `orchestrator/<id>` with the `Orchestrator-Run` trailer,
  the operator's branch and HEAD unchanged, the worktree removed, the branch kept; a failing
  review → no commit under `--mode auto`, the worktree kept and printed; the operator's branch
  moved with a conflicting change → the warning names the file and `run.json` records it; a
  commit refused by a hook → the worktree kept, the run still `completed`.
- `inplace` runs commit exactly as before (the existing scenarios pass unchanged).

## Gates

`scripts/check.sh`
