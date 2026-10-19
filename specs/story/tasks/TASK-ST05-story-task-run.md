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

## As built

2026-10-19. `scripts/check.sh` is the run's test step; it was not run while writing the change.

- **`task.source` also records `branch`, `base_ref` and `base_branch`** (SPEC §11.1 lists only
  story, task, attempt and jira). A resume of a run that stopped before its worktree exists has no
  other place to read the base and the branch from; `SPEC.md` §11.1 says so. `Key` and `URL` of
  `TaskSource` are now `omitempty`, so a story source carries no empty Jira fields; the Jira
  `run.json` and its goldens are unchanged.
- **Loading** (`persistence`): `validateTaskSource` switches on `kind`. A story source needs a slug,
  an ID, `attempt >= 1`, a Jira key or none, a branch that is `story/<story>/<task>[.<n>]` for
  that very story and task, a full-SHA `base_ref` and a valid `base_branch`, and no Jira field;
  a Jira source may have no story field. `task.acceptance` is accepted on a story task only, with
  the input's bounds (`story.CheckAcceptance`). `validateWorktree` and `verifyRunWorktree` compare
  the worktree with `domain.RunWorktreeBranch(run)`, and for a story also its `base_ref` and
  `base_branch` with the source's, so a forged `run.json` cannot claim another branch or base.
- **`story.ParseTaskInput`** (`internal/story/input.go`) is the strict decoder: no other key, one
  object, every field as §11.1 says. `base_ref` must be a full lowercase SHA (a name could be read
  by git as an option or move), `base_branch` a plain branch name, `text` non-blank and at most
  32 KiB, `acceptance` at most 10 one-line items of at most 300 characters (an empty list is
  allowed: a task file may have none). `app` reads the file (at most 256 KiB) first of all, before
  the configuration, and checks against the repository (`base_ref` is a commit, the branch is
  absent) before `store.NewRun`: every refusal leaves no run directory.
- **Worktree**: `openWorktree` takes base, base branch and branch from `task.source` for a story
  and checks the commit again; the operator's HEAD, branch and dirty tree play no part. The dirty
  tree warning names the base commit (`checkWorktreeFor`, also used by `resume`). A `base_branch`
  that does not exist in the repository makes the conflict check "skipped", not a failure
  (`TestStoryTask_AMissingBaseBranchSkipsTheConflictCheck`): `checkConflicts` asks
  `BranchExists` first, and `MergeConflicts` no longer takes git's exit 1 without a tree (an
  unknown ref) for a clean merge.
- **`policy.ForceDevelopment(decision, reason)`** is pure and runs right after `policy.Explore`
  (so `decision.json`, the announced table and `run.json` agree), and again in `onEdit`, because
  `WithEdit` rebuilds the forced stages from `policy.force_stages` and could drop development; the
  re-enable prints a line. It does nothing when development is already on, so a task whose
  development cleared its threshold keeps the policy's own reason. A decision the operator made by
  hand (the choice offered when the decision provider failed) gets the same forcing. A review that
  `SelectStages` dropped only for lack of development comes back with its own reason when its
  probability cleared the threshold or the template forces it.
- **Review criteria**: `reviewCriteria(task.acceptance, plan)` is the one list the prompt numbers,
  `parseReviewStrict` counts and the verdicts store; the task's items come first, listed even when
  no plan stage ran. A run without acceptance renders the prompt it always did.
- **Commit**: the `<jira>: ` prefix comes from `source.jira` for a story and `source.key` for a
  Jira task; `Orchestrator-Story: <story>/<task>` follows `Orchestrator-Run`.
- **Display**: the resume header and `summary.md` show `Task <story>/<task> (attempt n, <jira>) on
  <branch>` for a story source (`resume_story_source.golden` is new; the Jira golden is unchanged).
- **Not done here**: the scope question (ST09) and the parent that dispatches children (ST06).
  The classification questions and `questions_hash` are untouched.
- Tests: `internal/story/input_test.go`, `internal/policy/force_test.go`,
  `internal/persistence/task_source_test.go`, `internal/action/review_test.go`,
  `internal/app/{args,commit,worktree}_test.go`, `internal/logging/resume_test.go` and
  `test/integration/story_task_test.go` with every Acceptance item.
