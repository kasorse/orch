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

## As built

2026-10-18. Code, unit and integration tests are in. `scripts/check.sh` (root and `bench/`), run
outside the sandbox, is left to the operator, so the task is not closed in the README or TODO.md
Q4 yet.

- `verifyRunWorktree` (`internal/app/worktree.go`) runs the checks in the order §11 gives. The
  only git commands before it accepts are in the operator's repository: `rev-parse
  --git-common-dir` for the derivation, then `git worktree list`. A path recorded differently
  is refused right after the derivation. A missing directory, then a `.git` pointer that is
  unreadable or names another admin directory (`.git (worktree pointer)`), are refused without
  git. So are an `admin_dir` not directly under `<common>/worktrees/`, a path git does not list,
  and a worktree on another branch or a detached HEAD. git spells paths symlink-resolved, so a
  resolved match counts where the literal one fails. A worktree marked `removed` on a run that
  did not complete is refused too: its branch stays, and the refusal says to start a new run.
- `resumeInWorktree` then takes the operator's git fingerprint and calls `switchToWorktree`, as a
  fresh run does. `checkBaselineRecorded`, `checkProtectedState`, `CheckLimits`, `Restore`,
  `StatusSummary` and the repository card then run on the worktree, and the `Worktree:` line
  follows the history. The fingerprint is the reference for the end-of-run conflict check. It
  is taken at the resume, not at creation. A config or hook changed in between still stops a
  run that executed a step, because the worktree's baseline hashes the same shared config and
  hooks.
- A run that executed a step under `options.isolation: worktree` with `implement`, but has no
  `worktree` record, is refused as corrupt rather than continued in the operator's tree.
- A run with no executions and no worktree yet keeps the clean-start check under its recorded
  isolation, unlike the plan, which dropped it. Under `worktree` that check only warns, as a
  fresh run does, so the operator's dirty tree is never refused. The worktree is then created by
  `enterWorktree` after approval. A run whose worktree exists but that has no executions skips
  the operator's tree. `enterWorktree` returns early when the session is already in a worktree,
  so no second `git worktree add` runs.
- Beyond the plan: no step is recorded until it ends, so a process killed in its first step
  leaves no execution but does leave the run-start baseline. Such a run's worktree is compared
  with the baseline (`checkProtectedState`) before it continues. `runSetup` no longer runs once
  a baseline is recorded, so setup never gets the network over a tree a model may have touched,
  even if `worktree.setup` was added to the config since.
- The `StateDone` short-cut (the process died after the last step, before the completion was
  saved) marks the run completed without the commit and prints `The worktree stays at <path>`.
  Committing there would run the commit flow outside `executeWorkflow`.
- Tests: `internal/app/resume_worktree_test.go` uses a recording executor to show that no command
  ran in the worktree before each refusal. Integration tests:
  `TestWorktree_ResumeContinuesInTheWorktree` and
  `TestWorktree_ResumeRefusesAWorktreeItCannotTrust` (a refused resume leaves run.json byte for
  byte and calls no provider).
