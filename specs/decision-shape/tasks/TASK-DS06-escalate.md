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

## As built (2026-10-13)

Built as planned, except as listed. `scripts/check.sh` (root and `bench/`) passed on
2026-10-13, run outside the bwrap sandbox, after rebuilding `bin/orchestrator`.

- **When a fix escalates.** `action.fixesACheck`: the last execution that is not an implement
  is a test or a review. Stage order puts both after implement, so only a fix transition leads
  back from them; skipping implements keeps the answer when a resume repeats a fix that failed.
  It reads the history, not the outcome of the step before: the transition already decided
  that a fix follows, and a passing review never leads back to implement.
- **Where the route is chosen.** `config.ActionsConfig.EscalatedRoute(action, decision)` next to
  `Route`, instead of a third parameter on `ActionConfig.Route`: the many callers of `Route` stay
  as they were, and `Dispatcher.route` is the one place that asks for the escalated route.
- **Not escalated:** complex (nothing above it), a level above with no `by_complexity` entry
  (the fix then keeps the decided level's route rather than falling to the base), `--stage`,
  and every action but implement.
- **`decision.json: models` does not list the escalated route.** Its keys are actions, checked
  on load and grouped by in `decisions calibrate`; a second implement entry would change that
  format for a route most runs never take. `run.json` records it per step as
  `model_source: escalated.<level>`, which `ValidModelSource` accepts for medium and complex.
- **Validation.** `escalate` on another action is an unknown key; `escalate: true` without
  `by_complexity` is a config error. `allowed_models` and `limits.max_cost_usd` needed no change:
  the first checks every `by_complexity` model, the second every action's provider, which
  escalation never changes.
- **Docs now:** MVP.md §31.1 (the paragraph that said nothing picks a model from failures) and
  the example config; the rest stays in DS07.
- **No integration scenario here.** DS07 adds scenario 61 (fake executables, `FAKE_ARGS_DIR`).
