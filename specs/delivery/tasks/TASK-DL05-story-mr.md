# DL05 — `story run`: one MR per task, the task's branch by its repository's rules

**Size** M (2 d) · **Depends on** DL04, DL13 · **Blocks** DL11

## Goal

Every story task's child delivers (it already runs under `--mode auto`), its branch is named by its
repository's rules with `story/<slug>/<ID>` as the fallback, and its MR is stacked on its
dependency's branch (`SPEC.md` §9, §9.1).

## Files

- `internal/app/story_run.go`: the per-service `validate` includes the §8 checks; exit 6 from a
  child is "completed without an MR": the task is `completed`, the story stops before any task that
  depends on it (`blocked: <ID> has no merge request`), independent tasks continue.
- `internal/story` / `story.json`: `tasks.<ID>.mr {iid, url}` read from the child's `run.json`,
  validated; `story status` and the end table print it next to the target branch.
- The child's target is its run's `base_branch`, which the story already sets; assert that it equals
  the target `finish` prints.
- Branch naming (§9.1):
  - `internal/story/input.go`: `TaskInput.Title` (one line, 1–200 runes, required); `Branch` stays
    the fallback, validated by the story pattern.
  - `internal/app` (`runTask` with `--story-task`): DL13's naming runs in the child with the
    child's config, `{key}`/Jira key = `TaskInput.Jira`, `{slug}`/task line = `TaskInput.Title`,
    fallback = `TaskInput.Branch` (its "must not exist" check only when it is used);
    `task.source.branch` keeps the fallback, `worktree.branch` the name used.
  - `internal/persistence`: `story.json: tasks.<ID>.branch` accepts §4.3's names or
    `StoryTaskBranch`; it is set from the child's `worktree.branch` when the run is read (after the
    child, or when a dead `story run`'s run is settled), absent before; `run.json` validation of a
    story run's `worktree.branch` accepts both.
  - `story_run.go`: the dependent's target, the "commit beyond its base" check of a blocked task,
    `knownBranches`, the README, As built and end table read the recorded name, never
    `StoryTaskBranch`; the plan prints the fallback with `or by <repo>'s rules`.
- A story child's push has no `-u` (`SPEC.md` §7.3, D22).
- The spec branch (`SPEC.md` §9, D22), once `specs/parallel-story` is built (whichever lands
  second does this part): tasks with no parent or several target `story/<slug>/spec`; the parent
  pushes it after the approval and after every merge on it, before the dispatch that needs it
  (fingerprint first, no `-u`, no push options, never forced, `ls-remote` empty at the first
  push); `story.json: bases.<repo>.spec_push` with the intent before the push; a failed or
  rejected push stops the story before the next dispatch.

## Acceptance

- Two tasks in one repository, B depends on A, a bare remote with the push-options hook: two MRs,
  B's target `story/<slug>/A`.
- The same with `worktree.branch.template: "feature/{key}-{slug}"` in the service and Jira children:
  branches `feature/PAY-101-<a-title>` and `feature/PAY-102-<b-title>`, B's MR targets A's branch,
  `story.json` records both names.
- `--retry` of a templated task: the new attempt's branch gets the `-<6>` suffix; without rules it
  is `story/<slug>/<ID>.2`.
- A with a failing review (no commit → exit 6): B blocked, an independent C runs.
- A service without its remote: stops before the first dispatch.
- A story child's push argv has no `-u`, and the shared git config is unchanged after it.
- With `specs/parallel-story`: the spec branch reaches the remote before the first dispatch and
  again after a merge on it; a task with two parents gets an MR targeting it; a spec branch that
  moved on the remote stops the story, and nothing is forced.

## Gates

`scripts/check.sh`; live check step 4 (operator).
