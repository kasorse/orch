# The listener as a library: the run in process, a public package, a service on the platform

Status: **completed** (4 of 4 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | EB01 | the run in process: `runTask`/`resumeRun` with a directory, the result as a value, the child mode deleted | orche | — | — | done | `story/embed/EB01` |
| [x] | EB02 | the module path (O6): `go.mod`, every import, `bench/boundary_test.go` | orche | EB01 | — | done | `story/embed/EB02` |
| [x] | EB03 | the public package `orchestrator`: `Listen`, `Embedded` | orche | EB02 | — | done | `story/embed/EB03.2` |
| [x] | EB04 | `examples/embed/`, MVP.md §53, the summaries | orche | EB03 | — | done | `story/embed/EB04` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
