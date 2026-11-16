# Listener story: an Epic becomes child tickets, the children run in parallel

Status: **completed** (7 of 7 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | LS01 | `internal/jira` and `internal/gitlab`: links, the link write, the children search, the group's projects | orch | — | — | done | `story/listener-story/LS01` |
| [x] | LS02 | Config: `max_parallel`, `blockers`, `ref_prefix`, `listener.story` | orch | LS01 | — | done | `story/listener-story/LS02` |
| [x] | LS03 | Blockers: a ticket blocked by an unfinished one waits | orch | LS02 | — | done | `story/listener-story/LS03` |
| [x] | LS04 | Slots: up to `max_parallel` runs at once, at most one per service | orch | LS03 | — | done | `story/listener-story/LS04` |
| [x] | LS05 | Planning an Epic: catalog, selection, references, survey, specify | orch | LS04 | — | done | `story/listener-story/LS05` |
| [x] | LS06 | The children: creation, links, the Epic's comment and outcomes | orch | LS05 | — | done | `story/listener-story/LS06` |
| [x] | LS07 | The end-to-end scenario and the documentation | orch | LS06 | — | done | `story/listener-story/LS07` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
