# Spec: Delivery — from a finished run to a merge request, in a terminal or in CI

Status: **completed** (12 of 12 tasks done)

| done | task | title | repository | depends on | jira | status | branch |
|---|---|---|---|---|---|---|---|
| [x] | DL01 | Commit message: Conventional Commit type and the decision trailer | orche | — | — | done | `story/delivery/DL01` |
| [x] | DL02 | `report.md` | orche | — | — | done | `story/delivery/DL02` |
| [x] | DL03 | Worktree by default, the `delivery` config, the checks before the worktree | orche | — | — | done | `story/delivery/DL03` |
| [x] | DL04 | The end of a worktree run: commit, push, merge request | orche | DL01, DL02, DL03 | — | done | `story/delivery/DL04` |
| [x] | DL06 | Jira write-back: comment and transition | orche | DL04 | — | done | `story/delivery/DL06` |
| [x] | DL07 | Headless: no terminal, `--output json`, exit codes | orche | DL04 | — | done | `story/delivery/DL07` |
| [x] | DL08 | `orchestrator review --base/--head` | orche | DL02 | — | done | `story/delivery/DL08` |
| [x] | DL10 | Dockerfile and the CI example | orche | DL07, DL08 | — | done | `story/delivery/DL10.4` |
| [x] | DL13 | The branch name: instructions, template, fallback | orche | — | — | done | `story/delivery/DL13` |
| [x] | DL05 | `story run`: one MR per task, the task's branch by its repository's rules | orche | DL04, DL13 | — | done | `story/delivery/DL05` |
| [x] | DL11 | Integration scenarios 64–77 | orche | DL01, DL02, DL03, DL04, DL05, DL06, DL07, DL08, DL13 | — | done | `story/delivery/DL11` |
| [x] | DL12 | Documentation | orche | DL10, DL11 | — | done | `story/delivery/DL12` |

Run it with `orchestrator story run <this directory>` and follow it with `orchestrator story status <this directory>`.
This file is generated and rewritten whenever a status changes: edits are lost, the task files are the source.
