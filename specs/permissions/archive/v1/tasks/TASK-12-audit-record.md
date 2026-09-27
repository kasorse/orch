# T12 — Audit record: `policy.json` and per-step policy persistence

**Phase** 1 (completes) · **Size** S–M · **Depends on** T06; fields from T07, T09, T11 ·
**Blocks** T13, T14

The record itself lands in phase 1 as soon as there is a policy to record (needs only T06).
The `env_forwarded`, `pre_run_ref` + provenance, and `bypass` fields are filled in as T07, T09
and T11 land — each of those tasks adds its own field and its own assertion here. Do not wait
for all four: an unrecorded phase-1 run is exactly the audit gap this task exists to close.

## Goal

Make "what was this run allowed to do?" answerable after the fact from the run directory
alone (F9, G5). A policy that is enforced but not recorded cannot be audited, and an
unauditable control is indistinguishable from an absent one to anyone reviewing a past run.

## Files

- `internal/persistence/filesystem.go` — `SavePolicy`, `ApprovalsLogPath`
- `internal/domain/run.go` — the `policy.json` document type
- `internal/app/app.go` — call `SavePolicy` once the mode is resolved
- tests

## What to do

1. New `policy.json` per run, beside `classification.json`/`workflow.json`, written through
   the same write-tmp-then-rename path `SaveRun` already uses. Contents per §8.13:
   - `mode`
   - the resolved per-action policy table for this run's actions
   - `env_forwarded`: variable **names** only
   - `ignored_advisory`: per action, from T02's `Supports` return (e.g. `["max_budget_usd"]`)
   - `bypass`
   - `pre_run_ref`
   - `config_path` (absolute) and its provenance verdict from T09 — the audit question "could
     `test.command` have been attacker-supplied?" is answerable only with this
   - `test_command` as configured, since the orchestrator shell-executes it and that is part
     of the run's effective authority
   - the claude CLI version where obtainable, because flags change between releases and a
     matrix entry (§12.1) is only meaningful against a version
2. Write it once the mode is known — after `resolveMode` for interactive runs, before the
   first step in every case. Never write a placeholder that a later step overwrites: a
   half-true audit record is worse than a missing one.
3. `StepExecution.Policy` (added in T06) is already persisted by `SaveRun`. Confirm it
   survives the append-only history — each retry entry carries its own policy.
4. `ApprovalsLogPath(runID)` returns `logs/approvals.log` for T13. Add it here so persistence
   has one place that owns run-directory layout.
5. Values are never recorded — only names (T07 step 5, MVP.md §23).

## Acceptance

- Every run directory contains `policy.json`, including runs that fail during the first step
  and runs cancelled at the gate.
- `policy.json` round-trips through `encoding/json` into its Go type (guards against a field
  that is silently dropped by a missing tag).
- `run.json` has a `policy` object on every execution entry, including retries.
- No environment variable value and no secret appears in `policy.json` — test with a planted
  sentinel.
- A bypass run records `bypass: true`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
