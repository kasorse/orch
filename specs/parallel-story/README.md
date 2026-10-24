# Parallel story: ready tasks at once, several parents merged on the spec branch

Status: **completed** (4 of 4 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | PS01 | internal/story: parents, the ready set, the conflict status | orche | — | — | done | `story/parallel-story/PS01` |
| [x] | PS02 | The spec branch, the merge of several parents, the conflict state | orche | PS01 | — | done | `story/parallel-story/PS02` |
| [x] | PS03 | Parallel dispatch: slots, one owner of the state, logs, drain, budget, cancel | orche | PS02 | — | done | `story/parallel-story/PS03` |
| [x] | PS04 | End-to-end scenarios and the documentation | orche | PS03 | — | done | `story/parallel-story/PS04` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
