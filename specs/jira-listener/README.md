# Jira listener: tickets labelled `ai-run` become runs, one at a time

Status: **planned** (0 of 6 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [ ] | JL01 | internal/jira: search, comments, /myself, the discussion section | orche | — | — | pending | — |
| [ ] | JL02 | Config: the listener and gitlab sections, validation, scrubbing | orche | JL01 | — | pending | — |
| [ ] | JL03 | run --listener-task and failure_kind in --output json | orche | JL02 | — | pending | — |
| [ ] | JL04 | Service, project, clone, the git credential helper, a fresh base | orche | JL03 | — | pending | — |
| [ ] | JL05 | orchestrator jira listen: poll, claim, outcomes, state, restart, stop | orche | JL04 | — | pending | — |
| [ ] | JL06 | The documentation of the listener and the changed invariants | orche | JL05 | — | pending | — |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
