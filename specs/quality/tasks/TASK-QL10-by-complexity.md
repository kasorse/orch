# QL10 — `by_complexity`: model and effort per decided complexity

**Size** M (2 d) · **TODO** Q3 · **Depends on** — · **Blocks** QL13, QL15

## Goal

An action can name a different model and effort for each decided complexity. Go picks from a
static config table; the provider never changes. The route is recorded, and a resume warns
when the config changed it (`SPEC.md` §9).

## Files

- `internal/config/config.go`: `ActionConfig.ByComplexity map[domain.Complexity]ModelEffort`
  (the raw struct in `UnmarshalYAML`); a method `Route(c domain.Complexity, manual bool)
  (ProviderModel, source string)`
- `internal/config/validator.go`: keys must be valid complexities; an entry needs model or
  effort; effort per `EffortLevels` and provider support; a `provider` key is rejected by the
  strict parser
- `internal/action/*`: each executor resolves `pm` through `Route` instead of reading
  `d.Config.Actions.X` directly
- `internal/domain/run.go`: `ProviderRef.ModelSource`
- `internal/domain/trace.go` (the decision.json trace) and its writer in `internal/persistence`:
  `models` per workflow action
- `internal/app/resume.go`: compare the routes and warn
- `internal/persistence/validate.go`; `orchestrator.example.yaml`

## Steps

1. Config type and validation. `ValidateDependencies` and `ValidateAuthorities` are unchanged,
   because the provider per action is unchanged. Add a test that proves it: a config whose
   `by_complexity` names a model only.
2. `Route`: a manual decision → base; a complexity present in the table → the base merged
   with the entry; otherwise → base. The source is `base` or `by_complexity.<level>`.
3. One helper `d.route(run, action)` used by every executor, which sets
   `ProviderRef.ModelSource`.
4. `decision.json: models`: `{action: {provider, model, effort, source}}` for each action in
   the built workflow, written with the decision. For a manual decision, every source is
   `base`.
5. Resume: compute the route for each remaining action from the persisted decision and the
   current config. Print a warning for every action whose model or effort differs from
   `decision.json: models`. This covers any model change, not only `by_complexity`. Old runs
   without `models` get no warning.

## Acceptance

- Unit: `Route` table (simple / medium / complex / manual / missing level / effort only).
- Config: `{simple: {provider: codex}}` → unknown key; `{trivial: …}` → invalid complexity;
  an empty entry → error.
- Integration (QL13): a simple decision runs implement on the `by_complexity.simple` model
  (fake CLI records `--model`); `--stage` runs the base.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-10)

Done as planned, except:

- **Gates:** the change was written without being able to run anything, so the four gates
  (`scripts/check.sh`) are the operator's to run. They passed on 2026-10-11 (root and `bench/`, outside
  the sandbox).
- **Parsing:** `ActionConfig.ByComplexity` is `map[domain.Complexity]ModelEffort`. The raw struct
  of `UnmarshalYAML` reads it as `map[string]ModelEffort` and keeps every key as written
  (including `trivial`), so `Validate` (`byComplexityProblems`) can name an invalid complexity.
  `ModelEffort` has only `model` and `effort`, so the strict parser rejects a `provider` key.
- **Route:** `ActionConfig.Route(c, manual)` as specified (an empty complexity is the base
  too). `ActionsConfig.For` and `ActionsConfig.Route(action, *Decision)` pick the action and
  read the decision (a nil decision or source `manual` is the base). `Dispatcher.route` and the
  `app` trace and resume code share them. `call` takes the source as a new argument after `pm`.
- **Record:** `domain.ProviderRef.ModelSource` (`base` or `by_complexity.<level>`,
  `domain.ValidModelSource`) is validated on load. `DecisionTrace.Models`
  (`map[string]ModelRoute{provider, model, effort, source}`) is filled in `internal/app`
  (`modelRoutes`), not in `Result.Trace` or `ManualTrace`, because `domain` and
  `classification` cannot import `config`. It is set where `run` saves the decision and again in
  the review screen's `onEdit`, from the edited decision. A manual decision gets `base` for
  every action. Only actions in the built workflow that make a call are listed (no `test`).
- **Resume:** `modelChangeWarnings` compares model and effort for the remaining actions, one
  line each (`implement model changed since the run started: opus → sonnet`, and `… effort
  changed …`, an unset effort shown as `(default)`), printed with the other `Warning:` lines
  on stderr. A `decision.json` that is missing, unreadable or without `models` gives none.
- **Tests:** `config.TestActionConfig_Route`, `TestLoad_ByComplexity*`,
  `TestByComplexity_ProviderSetUnchanged`; `action.TestExecute_RoutesByDecidedComplexity`;
  `app.TestModelRoutes`, `TestModelChangeWarnings`; `persistence.TestLoadDecision_Models`.
  The integration scenarios stay in QL13.
