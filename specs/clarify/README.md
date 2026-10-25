# Clarify: a clear task before the decision, one stage that reads the code

Status: **completed** (8 of 8 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | CL01 | Analysis leaves the stages: two asked stages, fallback, config errors | orche | — | — | done | `story/clarify/CL01` |
| [x] | CL02 | planning becomes investigation, with root_cause | orche | CL01 | — | done | `story/clarify/CL02` |
| [x] | CL03 | The clarity question: pre-check, re-check, threshold, trace | orche | CL02 | — | done | `story/clarify/CL03` |
| [x] | CL04 | projectctx.Docs: instruction excerpts for clarify | orche | CL03 | — | done | `story/clarify/CL04` |
| [x] | CL05 | Clarify types, prompt, answer parser and config | orche | CL04 | — | done | `story/clarify/CL05` |
| [x] | CL06 | The clarify loop: service, operator, auto, needs input, persistence | orche | CL05 | — | done | `story/clarify/CL06` |
| [x] | CL07 | Stages get the clarification; summary.md | orche | CL06 | — | done | `story/clarify/CL07` |
| [x] | CL08 | Scenarios and documentation | orche | CL07 | — | done | `story/clarify/CL08` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
