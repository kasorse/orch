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
