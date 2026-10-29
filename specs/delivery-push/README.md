# Delivery without a push: `delivery.push: false`

Status: **completed** (3 of 3 tasks done, 2026-10-29, built by hand, not by `story run`: the
remote of this repository is GitHub, so a story would have stopped at DP01's push)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | DP01 | delivery.push: false in run: the chain ends at the commit | orche | — | — | done | `spec/delivery-push` |
| [x] | DP02 | story run with services that do not push | orche | DP01 | — | done | `spec/delivery-push` |
| [x] | DP03 | The documentation of delivery.push | orche | DP02 | — | done | `spec/delivery-push` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
