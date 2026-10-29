# Jira listener: tickets labelled `ai-run` become runs, one at a time

Status: **completed** (6 of 6 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | JL01 | internal/jira: search, comments, /myself, the discussion section | orche | — | — | done | `story/jira-listener/JL01` |
| [x] | JL02 | Config: the listener and gitlab sections, validation, scrubbing | orche | JL01 | — | done | `story/jira-listener/JL02.2` |
| [x] | JL03 | run --listener-task and failure_kind in --output json | orche | JL02 | — | done | `story/jira-listener/JL03` |
| [x] | JL04 | Service, project, clone, the git credential helper, a fresh base | orche | JL03 | — | done | `story/jira-listener/JL04` |
| [x] | JL05 | orchestrator jira listen: poll, claim, outcomes, state, restart, stop | orche | JL04 | — | done | `story/jira-listener/JL05` |
| [x] | JL06 | The documentation of the listener and the changed invariants | orche | JL05 | — | done | `story/jira-listener/JL06` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
