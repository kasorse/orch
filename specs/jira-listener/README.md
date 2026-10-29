# Jira listener: tickets labelled `ai-run` become runs, one at a time

Status: **planned** (0 of 6 tasks done)

Design: [`SPEC.md`](SPEC.md). Decisions D1–D21 and the author's defaults A1–A8 (both confirmed by the owner, 2026-10-29):
SPEC.md §14–§15.

| done | task | title | depends on | size |
|---|---|---|---|---|
| [ ] | JL01 | internal/jira: search, comments, /myself, the discussion section | — | M |
| [ ] | JL02 | Config: `listener` and `gitlab`, validation, scrubbing | — | S |
| [ ] | JL03 | `run --listener-task` and `failure_kind` in `--output json` | JL02 | S |
| [ ] | JL04 | Service, project, clone, the git credential helper, fresh base | JL02 | M |
| [ ] | JL05 | `orchestrator jira listen`: poll, claim, outcomes, state, restart, stop | JL01, JL03, JL04 | L |
| [ ] | JL06 | End-to-end scenarios, MVP.md §51, CLAUDE.md, README, TODO | JL05 | M |

## Ground rules

SPEC.md "Ground rules for the tasks". A task is done when `scripts/check.sh` passes, it is ticked
here and in `TODO.md`, the `Status:` line is current, and the task file has an `## As built`.
