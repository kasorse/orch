---
id: AT01
depends_on: []
---
# AT01 — `transport`, `base_url`, `prices`, `web_tools`: schema and validation

**Size** S

## Goal

SPEC.md §2: the four keys under `providers.claude` and `providers.codex`, items 1–8 of the
validation, the org layer's pins. Nothing calls HTTP yet.

## Files

- `internal/config/providers.go`: the keys in both structs (strict schema, `UnmarshalYAML`),
  `Transport()` (`cli` default), `WebTools()` (default from `base_url`), `Prices()`.
- `internal/config/validate*.go`: items 1–7, each error naming the key and the route.
- `internal/domain`: the CLI alias list (`sonnet`, `opus`, `haiku`, `fable`, `opusplan`,
  `default`, `…[1m]`) next to the other claude constants.
- `orchestrator.example.yaml`: the commented block of SPEC §2.

## Acceptance

- Each item of SPEC §2 has a test: a valid config, and an invalid one refused with its message.
- `transport: api` with `--claude-auth login` is refused before any call.
- With every key absent, every existing test passes unchanged.
- `scripts/check.sh` passes.

## As built (2026-11-01)

- One `config.TransportConfig` (`transport`, `base_url`, `web_tools`, `prices`) embedded in both
  `ClaudeProviderConfig` and `CodexProviderConfig`, with `UsesAPI()` and `WebToolsOn()`;
  `transport.go` holds it and its checks. `ModelPrice` uses pointers, so a missing `input`/`output`
  is told from 0.
- Item 2 lives in `ResolveClaudeAuth`/`ResolveCodexAuth`, not in `Validate`: only the resolution
  knows the mode (flag, config, key present). Both now wrap the previous body.
- Item 4 needed the list of every route with its provider: `allowedModelProblems`' walk became
  `modelRoutes(cfg)`, which both checks use (the keys in messages are unchanged).
- Item 7 moved to AT05: the built-in price table is in `httpapi` (AT02), and the budget is already
  checked through `config.CostChecker`, which AT05 makes model-aware. Item 8 is runtime (AT05/AT12).
- Dropped: the org layer's pins. The org file has no providers section; SPEC §2 corrected.
- Added: `base_url`, `web_tools` and `prices` are refused with `transport: cli`, not ignored.
- Docs: MVP.md §46.2 (the keys), §46.7 and §46.8 (pointer here), `orchestrator.example.yaml`.
