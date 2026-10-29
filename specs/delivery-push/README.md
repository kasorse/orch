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

**Moved (2026-10-29, owner's D2):** the key is `story.push` in the story home's config, for every
service of the story; the parent writes `no_push: true` into each child's task input, the child
copies it into `options.push`, and `story run`'s `validate` of a service gets the hidden
`--story-no-push`. `delivery.push` is no longer a key (a strict-schema error), and a plain `run`
always pushes. Built by hand on `spec/jira-listener`.
