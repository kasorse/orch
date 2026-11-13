---
id: LS02
repo: orch
depends_on: [LS01]
jira: none
---
# LS02 — Config: `max_parallel`, `blockers`, `ref_prefix`, `listener.story`

**Size** S

## Goal

SPEC §2: the new keys, their defaults, their bounds and the cross-checks, in the strict schema.
Nothing reads them yet.

## Files

- `internal/config/listener.go`: `ListenerConfig.MaxParallel` (1..8, default 1),
  `ListenerLabels.RefPrefix` (default `ref:`, must differ from the service and project prefixes),
  `ListenerBlockers{LinkType, DoneStatuses}`, `ListenerStory{MaxServices, MaxRefs, MaxTasks,
  MaxCatalog, MaxCostUSD}` with the bounds of SPEC §2.
- Validation: `listener.story` requires `jira.create`, `gitlab.api_token_env` and
  `listener.blockers`; `blockers.done_statuses` non-empty, `link_type` non-empty.
- `story.jira_issue_types` is readable without the rest of the `story` section being required
  (the listener reads only it); `isEpic` keeps its default `[Epic]`.
- Tests: each key's default, bound and refusal; the three cross-checks; a config without the new
  keys validates as before.

## Acceptance

- `listener.story` without `listener.blockers` is a config error naming both keys.
- A listener config of today validates unchanged, with `MaxParallel` 1.
- `scripts/check.sh` passes.
