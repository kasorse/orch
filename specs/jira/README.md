# Take the task from a Jira ticket

Status: **completed** (7 of 7 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | JR01 | Ticket, TaskSource, load validation, ticket.json | orche | — | — | done | `story/jira/JR01` |
| [x] | JR02 | The jira config section, validation, DependencyOptions | orche | JR01 | — | done | `story/jira/JR02` |
| [x] | JR03 | Child processes lose the Jira and Jev credential variables | orche | JR02 | — | done | `story/jira/JR03` |
| [x] | JR04 | internal/jira client: request, retries, validation, JiraError, redaction | orche | JR03 | — | done | `story/jira/JR04` |
| [x] | JR05 | ParseRef, Render, golden files | orche | JR04 | — | done | `story/jira/JR05` |
| [x] | JR06 | run --jira: CLI, taskFromJira, console output, exit codes, resume line | orche | JR05 | — | done | `story/jira/JR06` |
| [x] | JR07 | Integration scenarios 19a–19h and MVP.md §39 | orche | JR06 | — | done | `story/jira/JR07` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
