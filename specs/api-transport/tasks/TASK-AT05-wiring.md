---
id: AT05
depends_on: [AT03, AT04]
---
# AT05 — wiring: registry by transport, fail-fast, `provider_cli`, cost by model

**Size** M

## Goal

SPEC.md §3 (registration), §7 (`ReportsCost(model)`), §9. After this task phase 1 is usable.

## Files

- `internal/app/app.go`: register the API client for a provider whose resolved transport is `api`;
  set its auth after `ResolveClaudeAuth`/codex auth; skip the CLI version check for it and run
  `httpapi.CheckModels` instead, in the fail-fast chain; the hooks line of SPEC §2 item 8.
- `internal/provider`: `CostReporter.ReportsCost(model string)`; `config.CostChecker` and
  `Registry.ReportsCost` follow; the CLI adapters ignore the model.
- `internal/domain`/`store`: `provider_cli` entries with `transport` and `endpoint`, validated on
  load; `resumes[]` the same.
- `resume`: the transport re-resolved and recorded.
- `test/integration`: a fake API server (`httptest`, routed by the `ACTION:` line) and scenarios: a
  run whose translator, clarify, LLM decision and branch naming go over HTTP, and an investigate
  route on an api provider refused by `ValidateAuthorities` until AT07.
- MVP.md §46 and §38.2 (the API transport's lines), CLAUDE.md (architecture: the new packages).

## Acceptance

- A config with a missing model (`/v1/models` lacks it) exits before any run.
- `run.json` has no key and no `base_url` path; `endpoint` is the host only.
- `scripts/check.sh` passes.

## As built (2026-11-01)

- **Phase 1 is usable for codex only.** `actions.research` is a required route with the `web`
  authority, and today only the claude CLI supports `web` (gemini and the codex CLI do not). A
  config with `providers.claude.transport: api` therefore fails `ValidateAuthorities` on research
  until the claude API client supports `web` (AT09). For codex it is the other way round: the codex
  CLI never supported `none`, so `transport: api` is what first lets the translator, clarify and the
  LLM decision provider run on codex, with the steps on the claude CLI. The full-run scenario uses
  codex for that reason; the claude scenario checks the refusal.
- The API clients are constructed with their endpoint and prices (`anthropic.New(baseURL, prices)`,
  `openai.New(...)`) and get the key later (`SetKey`), replacing AT03/AT04's `SetAuth`: the budget
  check in `ValidateDependencies` runs before the key is read, and it needs the prices.
  `registerAPIClients` swaps them into the registry right after `Validate`.
- `CostReporter.ReportsCost(model)` and `CostChecker.ReportsCost(provider, model)`. The budget
  checks walk `modelRoutes` (the routes they counted before - actions, clarify, branch naming; the
  translator and the decision provider still not), reporting a CLI provider once per action as
  before, and an API provider once per action and model with a hint naming `providers.<p>.prices`.
- `checkProviderCLIs` runs `checkAPIModels` for an API client (30 s, the client's retries
  included): an unserved model is a config error, a failed round trip a dependency error, both
  before any run. `orchestrator validate` prints `<p>: transport api, <host> serves every routed
  model` instead of the version line.
- `ProviderCLI` got `transport` and `endpoint`; `persistence` refuses an endpoint that is not a bare
  host, a version with transport api, and an unknown transport.
- Not here: the hooks notice of SPEC §2 item 8 (it belongs with the skills, AT12). No unit test of
  `apiKeySource` for the API transport: the adapter sends the key itself, there is nothing to check
  (SPEC §9).
- Docs: MVP.md §46.2 and §38.2, CLAUDE.md (the packages and the pointer to this spec).
