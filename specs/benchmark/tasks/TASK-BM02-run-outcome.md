# BM02 — `outcome` in `run.json`

**Size** S (0.5–1 d) · **TODO** B3 · **Depends on** BM01 · **Blocks** BM03, BM09

## Goal

A finished run says in one block how it ended and what it cost (`SPEC.md` §11.2), so the
report and the benchmark read one place instead of re-deriving it from executions.

## Files

- `internal/domain/outcome.go` (new) + test: `type Outcome`, `func ComputeOutcome(run Run,
  trace *DecisionTrace) Outcome`
- `internal/domain/run.go`: `Run.Outcome *Outcome` (`json:"outcome,omitempty"`)
- `internal/app/app.go`: write it wherever the run reaches a terminal status
  (`completed`, `failed`, `stopped`) — in the shared tail `executeWorkflow`, so `run` and
  `resume` both do it
- `internal/persistence`: validation

## Steps

1. `Outcome`: `status`, `failure_reason`, `fix_iterations`, `tests_passed_final *bool`
   (the last test execution's exit code; nil without a test step or before one ran),
   `review_verdict` (the last review's, empty without one), `duration_ms` (first
   execution start → last execution finish; 0 without executions), `usage` (sum over
   executions plus the decision calls, with `calls`), `decision_overhead` (`cost_usd`, `ms`
   from the trace's latencies, `calls`; zero for `--stage` runs).
2. `ComputeOutcome` is pure and total: any `Run` gives an answer, never an error, so the
   report (BM03) can compute it for old runs and for runs that died without a terminal
   status.
3. Write it after the final status is set and before the last `SaveRun`; a resume
   recomputes it over the whole history (the earlier attempts' usage included — money
   spent before a resume is still spent).
4. Persistence: optional; when present the status equals `run.status`, counters ≥ 0.

## Acceptance

- Unit table for `ComputeOutcome`: completed with green final test; failed in test after
  two fix iterations; review fail without implement (completed with the verdict); stopped
  in plan mode; no test step; resumed run (usage of both attempts summed); manual decision
  (no overhead); a run with no executions.
- Integration: a completed and a failed scenario show `outcome` with the expected fields;
  a resumed scenario's outcome covers both attempts.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
