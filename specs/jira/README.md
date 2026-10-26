# Take the task from a Jira ticket

Status: **planned** (0 of 7 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [ ] | JR01 | Ticket, TaskSource, load validation, ticket.json | orche | — | — | pending | — |
| [ ] | JR02 | The jira config section, validation, DependencyOptions | orche | JR01 | — | pending | — |
| [ ] | JR03 | Child processes lose the Jira and Jev credential variables | orche | JR02 | — | pending | — |
| [ ] | JR04 | internal/jira client: request, retries, validation, JiraError, redaction | orche | JR03 | — | pending | — |
| [ ] | JR05 | ParseRef, Render, golden files | orche | JR04 | — | pending | — |
| [ ] | JR06 | run --jira: CLI, taskFromJira, console output, exit codes, resume line | orche | JR05 | — | pending | — |
| [ ] | JR07 | Integration scenarios 19a–19h and MVP.md §39 | orche | JR06 | — | pending | — |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
