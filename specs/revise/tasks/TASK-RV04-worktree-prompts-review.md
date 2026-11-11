---
id: RV04
depends_on: [RV03]
---
# RV04 — The worktree on the existing branch, the prompts, the review of the whole MR

**Size** M

## Goal

SPEC §6.1, §6.2, §7 (the prompts): the fetch into `refs/orchestrator/revise/<id>`, the local
branch fast-forwarded or refused, the worktree on it, development forced, the prompts with the
original task, the review over `merge-base(target, head)...head` with the requirement as
criteria. `mode: new` uses the existing `openWorktree` with the fetched target as base.

## Files

- `internal/repository`: `FetchRef(remote, src, dst)`, `UpdateRefFrom(ref, new, old)`,
  `AddWorktreeExisting(path, branch)`, `BranchCheckedOut(branch)`; `ReviewDiff` takes an explicit
  base.
- `internal/app/worktree.go`: a `TaskSourceRevise` branch in `openWorktree` next to the story
  task's; `worktree.existing_branch`; `branch_naming.source: existing`; `verifyRunWorktree`
  accepts the recorded branch equal to `task.source.mr.source_branch`.
- `internal/classification` / `internal/policy` input: development forced with reason `revise`
  (as the story task's force, no new policy code).
- `internal/action`: the original-task fence and the MR line in the stage prompts; golden
  prompts (`-update`).
- `run.json: review_base`.
- Tests: the four local-branch cases of SPEC §6.1.3; a fetched tip different from `mr.sha`
  refused; the fingerprint compared around the fetch and the add; the review diff covers the MR's
  earlier commits.
- Docs: MVP.md §54.6 (the branch), §54.7 (the stages); MVP.md §47.2 (an existing branch for
  `revise`).

## Acceptance

- The operator's working tree, index, `HEAD` and `refs/remotes/` are unchanged after a revise run's start.
- A diverged local branch, or one checked out elsewhere, is refused before any model call.
- The implement prompt contains the requirement in the task block and the original task fenced after it.
- The review diff of an update run includes a file changed only by the MR's first commit.
- `scripts/check.sh` passes.

## As built (2026-11-11)

- `internal/repository/revise.go`: `ReviseRef`, `FetchBranch` (forced, `--no-tags
  --no-write-fetch-head`, into `refs/orchestrator/revise/<id>[-target]` only), `AddWorktreeOnBranch`,
  `CheckedOutAt`; the rest (`BranchTip`, `IsAncestor`, `UpdateRef`, `MergeBase`) already existed.
- `internal/app/worktree.go`: `reviseBase` (the fetches, the sha check, `review_base`, the
  fingerprint around them) and `localReviseBranch` (the four cases of SPEC §6.1.3), wired into
  `openWorktree`; `domain.BranchSourceExisting`, `RunWorktree.ExistingBranch`, `Run.ReviewBase`, and
  their load checks (`validateReviseWorktree`).
- No prompt changes and no golden files: RV03 put the original task in the task text, and the
  criteria in `task.acceptance` (SPEC §7's fence is not needed). `executeReview` takes
  `run.review_base` as the diff's base.
- No `logs/revise-fetch.*` (SPEC §10): a failed fetch's error carries git's stderr, and a fetch that
  succeeds says nothing worth keeping.
- `development` forced on is RV03's (`decideWithProvider`), not here.
- Also fixed here: three tests RV03 broke and its gate did not show (the output was cut): the
  messages of `--allow-red-tests`, `--claude-auth`/`--codex-auth` and `task.acceptance` now name
  `revise`.
- MVP.md §54.6, §54.7 and a line in §47.2.
