---
id: DP02
repo: orche
depends_on: [DP01]
jira: none
---
# DP02 — story run with services that do not push

**Size** S

## Goal

SPEC.md §4: a story whose services set `delivery.push: false` runs every task to its commit,
stacks tasks on local branches, and prints what to push at the end.

## Files

- `internal/app/story_run.go`: `checkDeliverable` reads `PushEnabled()` and keeps it per service;
  without a push the remote check is skipped (the secret-scan check stays).
- The spec branch: `pushSpecBranches` and `pushSpecBranch` skip a service that does not push;
  `spec_push` stays absent.
- `withoutMR`: a done parent whose run recorded `options.push: false` does not hold its dependents.
- The end of the story: the merge-request column `—` for such tasks and one `Not pushed
  (delivery.push: false): git push <remote> <branches>` line per service (SPEC §4).
- Tests next to the existing story run tests, and an integration scenario: a two-task chain in a
  repository without a remote, both tasks done, the story completed.

## Acceptance

- A story of two chained tasks in a service with `push: false` and no remote completes; the second task is stacked on the first's commit.
- No `git push` runs during that story, and `story.json` has no `spec_push` and no `tasks.<ID>.mr`.
- The story's end prints the push line with the branches in the story's order.
- A service with `push: true` in the same story still pushes and opens merge requests as today.
- With every service's key absent, every existing story test passes unchanged.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/delivery-push/`, nor MVP.md, CLAUDE.md, TODO.md, README.md,
`orchestrator.example.yaml` (DP03).
