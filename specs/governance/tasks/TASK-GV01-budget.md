# GV01 — `limits.max_cost_usd`: fail fast, engine stop, resume

**Size** S (1–2 d) · **TODO** G3 · **Depends on** — · **Blocks** GV04, GV07

## Goal

A run stops before its next step once its steps have cost the budget or more. The budget is
refused before any call when a routed provider cannot report cost (`SPEC.md` §5).

## Files

- `internal/config/config.go`: `LimitsConfig.MaxCostUSD *float64` (`max_cost_usd`)
- `internal/config/validator.go`: the value is positive and finite
- `internal/config/dependencies.go`: the check in `ValidateDependencies` against the providers
  of every route (base, `by_complexity`, `review.models`)
- `internal/provider/client.go`: `CostReporter` interface; `internal/provider/claude`:
  `ReportsCost() bool { return true }`
- `internal/domain/workflow.go`: `LimitsView.MaxCostUSD`
- `internal/workflow/engine.go`: the check next to `max_steps`
- `internal/app/app.go`: fill `LimitsView` from config
- `orchestrator.example.yaml`: the key, commented out

## Steps

1. Add the config key, then validation.
2. Add `CostReporter` and the dependency check. Its message names the provider and the action:
   `limits.max_cost_usd cannot be enforced: codex (actions.review) reports no cost`.
3. In the engine, before each step and after the `max_steps` check, sum `CostUSD` over
   `run.Executions` and count partial usages. If `MaxCostUSD > 0 && spent >= MaxCostUSD`:
   - `RunStopped`;
   - `failure_reason`: `budget exceeded: $X of $Y`, with ` (lower bound: N calls without a
     final report)` when N > 0;
   - persist, then `LimitExceededError`.
4. Make no resume change. The sum comes from persisted executions, and the limit comes from
   the current config, as `max_steps` does.

## Acceptance

- Unit (engine): three steps cost 2 each, budget 5 → the third step never starts; a budget of
  6 → all three run; with a partial usage, the message says "lower bound".
- Config: `max_cost_usd: 0` → error; `-1` → error; a codex route with a budget → dependency
  error; a claude-only route → ok.
- Integration lives in GV07 (scenarios 47, 48).

## Gates

`scripts/check.sh`
