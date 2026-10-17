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

## As built

2026-10-18. Code, unit and integration tests are in. `scripts/check.sh` (root and `bench/`) run
outside the sandbox is left to the operator, so the task is not closed in the README or TODO.md
Q4 yet.

- `offerCommit(ctx, s, canAsk) string` returns the commit. In place it is unchanged: the caller's
  gate decides whether it runs, and `canAsk` is not read. In a worktree, `auto` is always true.
  The branch in every message and in the question is the run's branch, and the worktree must
  still be on it (a model that switched branches gets no commit). A §42.6 refusal turns into
  the question when `canAsk` is true (`--mode` is not `auto` and stdin is a terminal);
  otherwise the reason is printed. The order before `CommitAll` is unchanged:
  `pre_run_ref == head_ref`, then `CheckBaseline`, whose baseline holds the `.git` pointer and
  compares it before the fingerprint, then `git status`.
- The session keeps the operator's repository (`session.operator`, set by `switchToWorktree`)
  and its git fingerprint right after `git worktree add` (`session.operatorFingerprint`, set by
  `openWorktree`). `s.repo` is the worktree by then. `finishWorktree` and `checkConflicts`
  (`internal/app/worktree.go`) use only the operator's repository. A fingerprint that changed,
  or cannot be read, skips the conflict check with a warning. A merge-tree error is a warning
  too, and git < 2.38 prints a note. The removal is tried after every commit, whether or not
  conflicts were found or the check was skipped. Any removal error keeps the worktree, and its
  reason is printed next to the path.
- A failed or stopped worktree run prints `The worktree stays at <path>` too, before its error.
  A completed run with nothing to commit, or one whose commit was refused, keeps its worktree
  (§10.4). `runs clean` (WT08) removes those.
- Output: `Committed <12-char sha> to orchestrator/<id>: <header>` is the in-place line with the
  run's branch. `Based on <base>@<7-char sha>. To take it:` / `  git merge orchestrator/<id>`
  follows it (bare SHA on a detached HEAD). The conflict warning lists at most 20 files, then
  `... and N more`, with the total in its first line. `WorktreeLeft` is gone.
- `summary.md` is rendered by `writeOutcome` before the commit, so a worktree run renders it
  again after `finishWorktree` (`saveSummary`). In place it is rendered once, as before. The
  `Worktree` row is `orchestrator/<id> from <base>@<sha>; commit <sha>; conflicts: a, b; kept at
  <path>`, each part only when it applies.
- The failing-review acceptance case: with `implement` in the workflow, a failing review sends
  the run back to implement, so a completed run never ends on one. The integration scenario
  ends at `max_fix_iterations` (stopped), and the worktree is kept and printed. The completed
  run with a warning is unit-tested (`TestFinishWorktree_WarningWithoutAnOperatorKeepsTheWorktree`,
  and `TestOfferCommit_WorktreeWarningAsks*` for the question). In the conflict scenario, the
  test command moves the operator's branch while the run is in its test step.
- `internal/interaction` needed no change: `AskCommit` already names `offer.Branch`.
- MVP.md §42/§47 still describe only the in-place commit; they move with WT10.
