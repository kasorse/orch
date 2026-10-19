# DS08 — a level's own `escalate`; implement starts from the plan's files

**Size** S · **Depends on** DS06

## Goal

On complex runs the first implement on the strongest model was 65–75% of the cost, and
`escalate: true` cannot move a complex fix anywhere (`SPEC.md` §8.1). Let each `by_complexity`
entry of implement name its own fix route, so complex can start cheaper and escalate on a
failed test or review. And stop the first implement re-surveying a repository the plan
already explored.

## Files

- `internal/config/config.go`: `ModelEffort.Escalate *Escalation`; `escalatedRoute` prefers it
- `internal/config/validator.go`: the escalate's model/effort rules, implement only,
  `allowed_models`
- `internal/domain/run.go`: `ModelSourceEscalateSuffix`; `ValidModelSource` accepts
  `by_complexity.<level>.escalate`
- `internal/action/prompts.go`: `implementPlanFilesRule` on the first attempt after a plan with
  `affected_files`
- docs: MVP.md §31.1, §44.8 and the implement prompt paragraph, README, example config,
  CLAUDE.md, `SPEC.md` §8.1

## Acceptance

- Unit: complex with its own escalate → its route; an empty escalate field keeps the entry's;
  a level without one climbs under `escalate: true`; `--stage` → base.
- `escalate` inside a review entry, an empty one and a bad effort are config errors; a provider
  or a nested escalate is an unknown key.
- Scenario 63: a fix at simple runs on the entry's escalate model, recorded as
  `by_complexity.simple.escalate`.
- The goldens of the first implement carry the rule; the retry goldens do not.

## Gates

`scripts/check.sh`

## As built (2026-10-19)

Built as planned, except as listed. `scripts/check.sh` (root and `bench/`) passed on
2026-10-19, run outside the bwrap sandbox, after rebuilding `bin/orchestrator`.

- **Precedence, not exclusion.** An entry's `escalate` and `escalate: true` may both be set:
  the entry's wins for its level, the others climb. The entry's needs no `escalate: true`, so
  the bool keeps its meaning and no existing config changes.
- **Its own source.** `by_complexity.<level>.escalate` rather than `escalated.<level>`: the
  latter already means "the route of the level above", and a complex fix on complex's escalate
  would have recorded the same source as a medium fix on complex's entry.
- **A separate `Escalation` type** with no `escalate` key, so the strict parser refuses a
  nested one instead of the validator.
- **The plan-files rule only on the first attempt.** A retry starts from the failure and the
  run's diff stat; pointing it back at the plan's files would compete with that.
- **Not measured yet.** Whether sonnet with a plan holds the pass rate of opus on complex
  tasks is for a bench config (next to `bench/configs/F.yaml`), not this task.
