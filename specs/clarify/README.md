# Clarify: a clear task before the decision, one stage that reads the code

Status: **planned** (0 of 8 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [ ] | CL01 | Analysis leaves the stages: two asked stages, fallback, config errors | orche | — | — | pending | — |
| [ ] | CL02 | planning becomes investigation, with root_cause | orche | CL01 | — | pending | — |
| [ ] | CL03 | The clarity question: pre-check, re-check, threshold, trace | orche | CL02 | — | pending | — |
| [ ] | CL04 | projectctx.Docs: instruction excerpts for clarify | orche | CL03 | — | pending | — |
| [ ] | CL05 | Clarify types, prompt, answer parser and config | orche | CL04 | — | pending | — |
| [ ] | CL06 | The clarify loop: service, operator, auto, needs input, persistence | orche | CL05 | — | pending | — |
| [ ] | CL07 | Stages get the clarification; summary.md | orche | CL06 | — | pending | — |
| [ ] | CL08 | Scenarios and documentation | orche | CL07 | — | pending | — |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
