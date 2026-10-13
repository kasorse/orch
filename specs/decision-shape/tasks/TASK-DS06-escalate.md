# DS06 — `actions.implement.escalate`

**Size** S (1–2 d) · **Depends on** DS02 (O1: yes) · **Blocks** DS07

## Goal

With `escalate: true`, an implement right after a failed test or a failing review routes as the
next level up (`SPEC.md` §8).

## Files

- `internal/config/config.go`: `ActionConfig.Escalate`, accepted on `implement` only;
  `Route(c, manual, escalated bool)` — the next level's `by_complexity` entry, source
  `escalated.<level>`; complex and manual stay as today
- `internal/domain/run.go`: `ModelSourceEscalated`; `ValidModelSource` accepts it
- `internal/action/context.go`: `escalated` is true when the execution before this implement is
  a failed test or a review that failed (or `medium` under `options.review_fix_medium`)
- `internal/app/models.go`: `decision.json: models` lists the escalated implement route
- MVP.md §31.1: one sentence: escalation is static routing by attempt (O1)

## Acceptance

- Unit: simple + failed test → the medium route; complex → unchanged; `--stage` → base; a
  resume after a failed test routes the same as the original process would have.
- `escalate` on another action is a config error.
- `max_cost_usd` and `allowed_models` need no change (they already cover `by_complexity`):
  check it, don't assume it.

## Gates

`scripts/check.sh`
