---
id: PS01
repo: orche
depends_on: []
jira: none
---
# PS01 — internal/story: parents, the ready set, the conflict status

**Size** S

## Goal

The pure functions the later tasks build on (SPEC.md §1, §2, §3 and Contracts, `internal/story`):
a task's parents in its repository, the set of tasks ready to dispatch, and the `conflict`
status. Only additions: no caller changes yet, and `story run` behaves exactly as before.

## Files

- `internal/story/graph.go`: `Parents(tasks []Task, id string) ([]string, error)`, which returns
  what `Base` computes today (the task's ancestors in its own repository, transitively and across
  repositories, minus those another of them depends on), sorted, for any number of them; an error
  only for an unknown ID. Write `Base` on top of `Parents` so that the two cannot drift; `Base`
  keeps its behaviour and its error message for several parents, because `story run` still relies
  on them until PS02.
- `internal/story/graph.go`: `Ready(tasks []Task, states map[string]TaskStatus) []string`: in
  `Order(tasks)`, the tasks whose every `depends_on` is `TaskDone` and whose own state is neither
  `TaskDone` nor `TaskRunning`. A task with no entry is pending.
- `internal/story/readme.go`: `TaskConflict TaskStatus = "conflict"`, documented next to the
  others (the task's parents do not merge cleanly on the spec branch; it is not dispatched).
  `RenderREADME` shows it like any other status.
- Tests next to the existing ones (`graph_test.go`, `readme_test.go`).

## Acceptance

- `Parents` returns none for a task without same-repository ancestors, one for a chain, and both
  for a task that needs two tasks of its repository neither of which depends on the other; an
  ancestor in another repository is never a parent.
- `Base` gives the same results and errors as before for every case in the existing tests.
- `Ready` returns, in the story's order: the tasks without dependencies first; a task only once
  all its dependencies are done; never a done or running task; failed, stopped, blocked and
  conflict tasks whose dependencies are done.
- `RenderREADME` renders a `conflict` task's status cell as `conflict`.
- `internal/story` still imports only `domain` and the standard library (`imports_test.go`).
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/parallel-story/` (story run writes its README and As built).
