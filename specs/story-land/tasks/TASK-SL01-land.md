---
id: SL01
repo: orche
jira: none
---
# SL01 — Land a story that does not push on its base branch

**Size** M

## Goal

SPEC.md §1–§6: at the end of a completed story, a service with `delivery.push: false` gets the
tasks' result on its base branch, and the story's README and As built are committed there.

## Files

- `internal/repository`: `MergeFastForward`, `CommitPaths`.
- `internal/app/story_merge.go`: the merge loop shared by the spec branch and the land.
- `internal/app/story_land.go`: the land; `finish` calls it.
- `internal/logging/story.go`: the console lines.
- Tests: integration scenarios for the land (fast-forward of a checked-out branch, a branch not
  checked out, a refused land and its retry, the story files committed).
- MVP.md §48 and §50.8, CLAUDE.md, README.md, TODO.md.

## Acceptance

- A story of two chained tasks in a service with `push: false` ends with the service's base branch at the second task's commit, and the checkout's tree has it.
- A base branch that is not checked out moves with update-ref; one checked out in another worktree is not touched.
- A local change git refuses to overwrite leaves the branch where it was, the story ends with exit 6, and the next story run lands it.
- When the spec directory is in the service, its README and task files are committed on the base branch.
- A service that pushes is unchanged.
- `scripts/check.sh` passes.

## As built (2026-10-29)

Built by hand on `spec/story-land`. `story_land.go`: `landAll` replaces `reportNotPushed` at the
end of `finish`; `land` per service, `leaves`, `recordStory`; the merge loop of `mergeParents` is
`mergeOnto` now, shared by both (it moves no ref; `mergeParents` moves the spec branch once with
its old value). Differences from the plan: a later `story run` of a completed story skips the
service checks (`checkServices` checks only services with a task to run), so it knew neither
`delivery.push` nor a fingerprint, and the old `Not pushed` line was not printed there either;
`readDelivery` reads both for a service the checks skipped. Tests:
`TestStoryRun_AServiceThatDoesNotPushStacksOnLocalBranches` (now asserts the land),
`TestStoryRun_ALandThatWasRefusedIsTriedAgain` (git refuses, another worktree, update-ref, already
landed), `TestStoryRun_TheLandCommitsTheStoryFilesInTheService` (fixture option `homeService`).
