---
id: CL05
repo: orche
depends_on: [CL04]
jira: none
---
# CL05 — Clarify types, prompt, answer parser and config

**Size** M

## Goal

Everything the clarify loop (CL06) needs that is not the loop itself: the domain types and their
rendering, the prompt, the strict answer parser, the `clarify` config section and its place in
the fail-fast chain (Approach §3, Contracts: Clarify answer, `run.json: task.clarification`,
Render, Configuration).

## Files

- `internal/domain`: `Clarification`, `ClarifyRound`, `ClarifyPoint` with JSON tags as in
  Contracts; `Render()` producing exactly the Contracts text; `Validate()` with the rules of
  Contracts.
- `internal/action`: `BuildClarifyPrompt(ClarifyPromptInput{Task, Instructions, Clarification,
  Mode})` (first line `ACTION: clarify`, the task through `taskBlock`, rules, excerpts and the
  clarification so far through `fence`); `ParseClarify(output, mode)` (strict; at most 8 points,
  300 bytes per field, cut on a rune boundary); goldens `clarify_interactive.golden`,
  `clarify_auto.golden`.
- `internal/config`: the `clarify` section (`enabled` default true, `provider`, `model`, `effort`,
  `timeout`, `max_rounds` 0..5 default 2; `docs` and `max_bytes` from CL04), now required: a route,
  or `clarify: {enabled: false}` written out. With `enabled: true` it is checked in
  `ValidateDependencies` (the executable), `ValidateAuthorities` (`Supports(AuthorityNone)`), the
  CLI version check, the org layer's `allowed_models`, and `provider.CostReporter` under
  `limits.max_cost_usd`.
- `orchestrator.example.yaml`, `bench/configs/*.yaml` (with `clarify: {enabled: false}`) and the
  test configs gain the section, so every config a test loads stays valid.

## Acceptance

- `ParseClarify` refuses an unknown key, a `basis` outside docs/assumption/none, a `source` on a non-docs point, and prose without JSON.
- `ParseClarify` in interactive mode refuses a point with `basis: assumption`.
- `ParseClarify` keeps at most 8 points and cuts a 400-byte field to 300 bytes on a rune boundary.
- `Clarification.Render()` output for the Contracts example matches the Contracts text exactly (golden or literal).
- `Clarification.Validate()` refuses `len(checks) != 1 + len(rounds)`, a probability above 1 and an unknown `by`.
- A config without a `clarify` section fails validation; `clarify: {enabled: false}` alone is valid.
- With `enabled: true`, a missing clarify executable fails `orchestrator run` before any provider call.
- The clarify prompt golden holds the task in a task block and the excerpts in fenced blocks.
- `scripts/check.sh` passes, including `bench/`.

## Notes

Do not edit anything under `specs/clarify/`.
