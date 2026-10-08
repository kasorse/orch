# BM01 — Persist usage per step and per decision call

**Size** S (1 d) · **TODO** B3 · **Depends on** — · **Blocks** BM02

## Goal

What every provider call cost is kept in the run directory, not only shown in the TUI
(`SPEC.md` §2, §11.1). Today the claude adapter parses `total_cost_usd`, tokens and turns into
`provider.Result.Usage`, and `provider.ReportUsage` hands it to the observer only.

## Files

- `internal/domain/run.go`: `StepExecution.Usage *Usage` (`json:"usage,omitempty"`)
- `internal/domain/usage.go`: JSON tags on `Usage` (snake_case; `limits` stays out of
  persistence — it is the account's state, not the call's cost)
- `internal/domain/trace.go`: `DecisionTrace.Usage` — `DecisionUsage{Translation,
  ContextDepth, Classification *Usage}` (`json:"usage,omitempty"`)
- `internal/action`: the dispatcher records `result.Usage` on the execution it builds
  (where `provider.ReportUsage` is called, `context.go`)
- `internal/translate/translator.go`, `internal/decision/llm/provider.go`: return the call's
  usage alongside the answer (they already have it at the `ReportUsage` call)
- `internal/decision/jev`: map the API's `usage` (tokens, no cost) into `domain.Usage`; the
  mock's invented tokens are **not** recorded (`SPEC.md` §11.1)
- `internal/classification`: put the three usages into the trace
- `internal/persistence`: validation of the new fields on resume

## Steps

1. Tags and the new fields. `Usage` keeps every counter it has (`input_tokens`,
   `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `cost_usd`, `turns`).
2. Thread usage out of the translator and the LLM decision provider without changing what
   they return to their callers otherwise; `decision.Provider` gains nothing provider-specific
   (usage is a `domain` type).
3. The dispatcher stores it on the `StepExecution` it appends (append-only history is
   untouched: the usage belongs to that execution).
4. A call that fails after the CLI reported usage still records it (money was spent).
5. Persistence: `usage` optional; when present every counter ≥ 0 and `cost_usd` finite.
   Run directories written before this task load unchanged.

## Acceptance

- Unit: stream parsing → `Usage` on the execution; failed call with usage → recorded; jev
  usage mapped, mock usage absent; persistence round-trip and rejection of a negative
  counter; an old `run.json` without `usage` loads.
- Integration: a scenario with the fake CLI emitting a `result` event with
  `total_cost_usd` shows `executions[].usage.cost_usd` and `decision.json`
  `usage.classification` (LLM decision provider) and `usage.translation` (Russian request).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-06. Differences from the plan, and why:

- **Step usage goes through `workflow.StepOutcome.Usage`**, set once in
  `Dispatcher.Execute` from what `Dispatcher.call` saw (`stepUsage`, reset per step), and the
  engine copies it onto the `StepExecution`. Every action makes at most one provider call,
  so this covers all of them — failure returns included — without touching each action's
  return paths. `internal/workflow` still imports only `domain`.
- **Decision usage travels with the answer**: `ContextDepthDecision.Usage` and
  `TaskClassification.Usage` (`json:"-"`, not part of the answer's wire form), and
  `apperr.DecisionError.Usage` for a call that failed or whose answer did not parse or
  map. `apperr` now imports `domain` (no cycle: `domain` imports nothing internal).
  `classification.Service` collects the three into `Result.Usage`; `Trace` writes
  `decision.json` `usage` only when some round reported any.
- **Translation** records usage also when its sanity check rejects the answer (the call
  was paid for).
- **JSON form**: `input_tokens` and `output_tokens` always, `cache_read_tokens`,
  `cache_creation_tokens`, `cost_usd`, `turns` when non-zero; `Limits` never (`json:"-"`).
- **`LoadDecision` now validates** the usage it reads (it decoded strictly before but
  checked no values); `run.json` executions' usage is checked in `validateExecution`.
- Tests: `domain` (Validate, JSON without limits), `jev` (fixture tokens, unmapped answer,
  mock records none), `llm` (answer, unparsed answer, failed call), `classification`
  (three rounds, failed round before the manual choice, none), `persistence` (round trip,
  negative counter / cost, limits refused, decision usage), integration scenario 29
  (`test/integration/scenario29_usage_test.go`: stream-json costs in `run.json` per step
  and in `decision.json` per round, a failed implement's usage, no rate limits recorded,
  the strict loader reads it back).
