# S05 — `decision.json` and `run.json` for a manual run

**Size** S (0.5 d) · **Depends on** S01, S04 · **Blocks** S06

## Goal

A manual run records what it was: `source: manual`, the request, the stages, the workflow,
nothing about a provider (`SPEC.md` §5, §12).

## Files

- `internal/domain/trace.go`, `internal/persistence/*`, `internal/logging/decision.go`,
  `pretty.go` + goldens

## Steps

1. Build the `DecisionTrace` for a manual selection: `Source: manual`, `Request`, `Stages`,
   `Workflow`, empty provider and model, zeroed latencies, `ContextLimited: false`, translation
   `{detected: "", translated: false}`. No `project_context.json` is written.
2. Loading a persisted `manual` run (run.json and decision.json) works; a stored run from an
   older version loads as before.
3. Where the console or the trace shows complexity, show `—` for a manual decision (D1).
4. `resume` needs no change: `workflow.Verify` rebuilds from `decision.stages`. Add the test
   that proves it.

## Acceptance

- Golden `decision_manual.golden` (JSON) and the pretty-printed form.
- Round-trip: save → load → `Verify` → equal workflow.
- Existing decision goldens unchanged.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03. `domain.ManualTrace` builds the trace (S04 had inlined a minimal one).
`LoadRun` needed no change. The plain and pretty decision renderers show `—` for complexity and
`manual (--stage)` for the provider; a normal `run --stage` only prints the stages line, so
these matter where a manual trace is rendered. Goldens: `decision_manual.golden` (JSON) and
`decision_manual_text.golden`. The save → load → `Verify` test is in `persistence`.
