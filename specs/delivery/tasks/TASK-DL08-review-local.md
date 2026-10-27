---
id: DL08
repo: orche
depends_on: [DL02]
jira: none
---
# DL08 — `orchestrator review --base/--head`

**Size** M

## Goal

Review `merge-base(base, head)..head` without implement and without a clean tree, in a detached
worktree, as an ordinary run (`SPEC.md` §12.1–§12.3).

## Files

- `internal/app/review.go`: the command, its flags and their combinations; refs resolved with
  `rev-parse --verify --end-of-options`; the merge base; the empty-change error; the task (`--task`,
  `--jira`, else the commit subjects, ≤ 50 / 8 KiB); `domain.ManualDecision([testing, review])`;
  then the shared tail (`executeWorkflow`), which already does setup, guard, report.
- `internal/app/worktree.go`: a detached worktree at `head` for a review run (`git worktree add
  --detach`); `verifyRunWorktree` accepts a detached worktree at `review_target.head` for a run with
  `review_target`; removal at the end whatever the status.
- `internal/repository/reviewdiff.go`: `ReviewDiffOptions.Base` (default `HEAD`).
- `internal/action/review.go`: passes `review_target.merge_base` as the base for a review run.
- `internal/domain/run.go`: `review_target`, validation; `worktree.branch` empty only with it.
- `internal/report`: the review form of `report.md`.
- Exit 6 for a failing verdict from this command only.

## Acceptance

- A repository with `main` and `feature`: the review prompt's diff is exactly the feature commits'
  change (a change on `main` after the fork is not in it); the operator's dirty file is untouched
  and not reviewed.
- A failing test → the run fails before the review, the report says so, exit 4.
- Failing verdict → exit 6; passing → exit 0. Resume of a stopped review run continues in the same
  worktree.
- Flag errors: no `--base`, `--head` without `--base`.

## Gates

`scripts/check.sh`

## Notes

Do not edit anything under `specs/delivery/` (story run writes its README and As built). Leave
MVP.md, CLAUDE.md, TODO.md and README.md to DL12.
