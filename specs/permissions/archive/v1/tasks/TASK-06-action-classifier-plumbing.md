# T06 — Carry the policy through `action` and `classifier`, record it per step

**Phase** 1 · **Size** M · **Depends on** T01, T02, T03, T05 · **Blocks** T12

## Goal

Every provider call gets a policy derived from the fixed table — not a default, not a
zero value. `internal/action` is the composition point, so this is where policy is built;
`internal/workflow` must not learn what a policy is.

## Files

- `internal/action/context.go` — build the policy in `Dispatcher.call`, pass in `Request`
- `internal/action/*.go` — the per-action executors, where the action name is known
- `internal/classifier/classifier.go` — the no-tools classify policy
- `internal/domain/run.go` — `StepExecution.Policy`, `Run.PreRunRef`
- `internal/workflow/*.go` — `StepOutcome` carries the policy back for recording
- tests

## What to do

1. `Dispatcher` gains the run mode and repo root (it has `Repo.Dir` already; mode must be
   passed in from `app.go` — add a field, set at construction, since one `Dispatcher` is
   already scoped to exactly one run).
2. In `Dispatcher.call`, build `domain.PolicyFor(actionName, d.mode, d.Repo.Dir)`, apply
   `Config.Security.ImplementExecAllowlist` when `actionName == implement`, set
   `MaxBudgetUSD` from config, `Validate()` it, and pass it in `provider.Request`. A
   `Validate` failure is a wiring bug, not user error — return `*apperr.WorkflowError`.
3. `classifier.Classify` passes the classify policy. The classifier builds its own
   `provider.Request` today, so it needs the policy too; give `classifier.New` the policy (or
   a `domain.ClassifyPolicy()` call) rather than letting it construct a zero-value `Policy`,
   which would mean "no capabilities" by luck rather than by intent. State that in a comment
   — a zero value that happens to be correct is a trap for the next refactor.
4. `StepExecution.Policy domain.Policy` with a `json:"policy"` tag. Append-only history is
   preserved: each retry records the policy it actually ran under, which matters because a
   retry after a review failure is a *different* execution and could in principle carry a
   different policy.
5. Thread the policy back through `workflow.StepOutcome` so the engine can put it on the
   `StepExecution` it appends. **`internal/workflow` must not import `provider` or `config`**
   — `StepOutcome` already carries `*domain.ProviderRef`, so adding `domain.Policy` beside it
   keeps the existing dependency rule intact. Verify with `go list -deps`.
6. `Run.PreRunRef string` is added here (cheap, keeps `domain` changes in one task) but
   populated by T09.

## Acceptance

- Every `provider.Request` constructed anywhere in the codebase carries a non-zero policy for
  actions that need capabilities — assert by having the fake provider client reject a
  zero-value policy for read-capable actions, so a missed call site fails a test instead of
  silently running with nothing.
- `run.json` for a completed run contains a `policy` object on every execution entry.
- `internal/workflow`'s import set is unchanged apart from `domain`
  (`go list -deps ./internal/workflow | grep orchestrator/internal` shows only `domain`).
- Existing integration scenarios 1–13 pass unmodified except those asserting argv containing
  `bypassPermissions`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
