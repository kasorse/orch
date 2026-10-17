# ST05 — `run --story-task`

**Size** M (2 d) · **Depends on** ST01, WT04, WT06 · **Blocks** ST06

## Goal

A child run gets everything a story task needs from one validated input file, and records it so
that its own resume needs no story (`SPEC.md` §11).

## Files

- `internal/app/app.go`: hidden `run --story-task <file>`; it excludes `--stage`, `--jira`,
  `--inplace` and positional text; the file decoded strictly (§11.1: patterns, sizes, `base_ref`
  a commit here, the branch absent)
- `internal/domain/task.go`: `TaskSourceStory` with `story`, `task`, `attempt`, `jira`;
  `Task.Acceptance []string`; both validated on load
- worktree (`specs/worktree/` WT04): the base and branch come from the input; WT03's branch
  validation accepts `story/<slug>/<ID>[.<n>]` when `task.source.kind` is `story`;
  `options.isolation: worktree` whatever the config says; `base_branch` is the input's
- `internal/policy`: `ForceDevelopment(decision, reason)` — development on, testing with it,
  `reason: story task`, pure; called in `app` for a story task after the policy
- `internal/classification`: the scope question not asked for a story task (ST09)
- `internal/action/review.go`: `Task.Acceptance` first in the review criteria, then the plan's
- `internal/app/commit.go`: the `<jira>: ` header prefix from `task.source.jira`; the trailer
  `Orchestrator-Story: <slug>/<ID>`

## Acceptance

- Integration (fake executables): a story task with development below its threshold still
  implements and tests, `decision.json` shows `story task`; the worktree is on the given branch
  from the given commit; the review prompt lists the task's criteria first; the commit header
  starts with the Jira key and carries both trailers; `resume` of a failed child continues with
  the same criteria and branch.
- Refusals: a malformed input, an unknown `base_ref`, an existing branch, `--story-task` with
  `--stage`.

## Gates

`scripts/check.sh`
