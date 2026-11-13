# Listener story: an Epic becomes child tickets, the children run in parallel

Status: **planned** (0 of 7 tasks done; spec 2026-11-13, owner's answers L1–L7, defaults A1–A16 confirmed 2026-11-13)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [ ] | LS01 | `internal/jira` and `internal/gitlab`: links, the link write, the children search, the group's projects | orch | — | — | pending | — |
| [ ] | LS02 | Config: `max_parallel`, `blockers`, `ref_prefix`, `listener.story` | orch | LS01 | — | pending | — |
| [ ] | LS03 | Blockers: a ticket blocked by an unfinished one waits | orch | LS02 | — | pending | — |
| [ ] | LS04 | Slots: up to `max_parallel` runs at once, at most one per service | orch | LS03 | — | pending | — |
| [ ] | LS05 | Planning an Epic: catalog, selection, references, survey, specify | orch | LS04 | — | pending | — |
| [ ] | LS06 | The children: creation, links, the Epic's comment and outcomes | orch | LS05 | — | pending | — |
| [ ] | LS07 | The end-to-end scenario and the documentation | orch | LS06 | — | pending | — |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
