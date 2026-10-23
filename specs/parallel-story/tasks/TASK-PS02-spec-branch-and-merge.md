---
id: PS02
repo: orche
depends_on: [PS01]
jira: none
---
# PS02 — The spec branch, the merge of several parents, the conflict state

**Size** M

## Goal

A task that needs several tasks of its repository is no longer a spec error: `story run` merges
their commits on the story's spec branch `story/<slug>/spec` and dispatches the task from there.
A conflict stops the story for a manual merge, and the next `story run` continues once the
operator has merged (SPEC.md §1, §3). Still one task at a time: the parallel loop is PS03.

## Files

- `internal/repository`: `MergeTreeWrite`, `CommitTree`, `UpdateRef` and `IsAncestor` as in
  SPEC.md Contracts. Argv only; a ref that is empty or starts with `-` is refused without running
  git, as `MergeConflicts` does; reuse its git version check (merge-tree `--write-tree` needs git
  2.38). Tests with real git in a temporary repository, like the existing worktree tests.
- `internal/persistence/story.go`: `bases.<repo>.spec_branch`; `tasks.<ID>.status: conflict` with
  `conflict {parents, files}`. Strict validation on load (SPEC.md Contracts); a record without the
  new fields loads as before.
- `internal/app/story_run.go`:
  - First start, after the approval, before the Jira children: create `story/<slug>/spec` at
    `bases.<repo>.sha` in every repository the story touches (`UpdateRef` with an empty old
    value) and record it. A continued story whose record lacks it: create it before any dispatch
    and print a note. A spec branch the record names but git lacks stops the story before any
    dispatch.
  - `knownBranches` and the unknown-branch check know the spec branch.
  - The pre-run checks keep each repository's git fingerprint. Before the first merge-tree in a
    repository, the current fingerprint must equal it, or the story stops.
  - `dispatch`: `story.Parents` in place of `story.Base`. None: the story base commit. One: the
    parent's recorded commit. Several: §1's merge, parents in ID order (nothing, fast-forward, or
    merge-tree + commit-tree + `update-ref` with the old value), then `base_ref` is the spec tip.
    The merge commit's message is `Merge <parent's branch> for <ID>` with the trailer
    `Orchestrator-Story: <slug>/<ID>`.
  - A conflict: the task becomes `conflict` and is not dispatched, `conflict {parents, files}` is
    recorded (at most 50 files), the story is `blocked`, and `StoryConflict` prints the files and
    the manual merge (a worktree of the spec branch, `git merge` of the parents' branches, commit,
    remove the worktree, run the story again).
  - `advance`: a `conflict` task is looked at again. The spec tip must descend from
    `bases.<repo>.sha`. If every parent's commit is now its ancestor, the task is dispatched from
    the tip; otherwise the merge is tried again.
  - The plan before `[Y/n]`, the As built and `finish`'s end table: the base and the target of a
    task with several parents are the spec branch, written as `story/<slug>/spec (PS02 + PS03)`
    in the plan and the As built. A task without parents targets the spec branch too.
  - Remove the "make one depend on the other" refusal (in `story.Base` and wherever `load` or
    `Validate` reaches it). Remove `Base` if nothing calls it any more.
- `internal/logging/story.go`: `StoryConflict`, plain and pretty, like the other story lines.

## Acceptance

- Unit (repository): a clean merge gives a tree and no conflicts, and a conflict gives its files;
  `CommitTree` with two parents; `UpdateRef` refuses when the old value is not the ref's value;
  `IsAncestor` in both directions; option-like refs are refused without running git.
- Story (`internal/app` tests with fake executables): A and B independent, C depends on both, all
  in one repository. C's task input carries a `base_ref` that is a merge commit on
  `story/<slug>/spec` whose parents include A's and B's commits. The spec branch exists from the
  first start, at the base commit.
- The same with A and B changing one line differently: C is `conflict`, the story is `blocked`,
  the files are printed, and nothing is dispatched for C. After the test merges them on the spec
  branch by hand, the next `story run` dispatches C from that tip.
- A chain A → B: B is stacked on A as before, and no merge commit is made.
- A record from before this task (no `spec_branch`) continues: the spec branch is created with a
  note.
- The existing story tests pass. Change one only where the target of a task without parents
  changed to the spec branch, and say so in the commit.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/parallel-story/` (story run writes its README and As built).
Leave MVP.md, CLAUDE.md, TODO.md and README.md to PS04.
