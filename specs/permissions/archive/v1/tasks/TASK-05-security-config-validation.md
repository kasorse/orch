# T05 — `security:` config block and `ValidatePolicies` in the fail-fast chain

**Phase** 1 · **Size** M · **Depends on** T01, T02 · **Blocks** T06, T07, T09, T11

## Goal

Add the four narrow knobs of §9 and — more importantly — put the capability check in the
right position in the fail-fast ordering, so a provider that cannot enforce an action's
policy stops the run **before the classifier object is constructed**. This is MVP.md §7's
invariant extended to authority.

## Files

- `internal/config/config.go` — `SecurityConfig` + `Config.Security`
- `internal/config/loader.go` — defaults (note: `require_clean_worktree` defaults **true**)
- `internal/config/validator.go` — §9 validation rules
- `internal/config/policies.go` — **new**: `ValidatePolicies`
- `internal/app/app.go` — call site and ordering
- tests for each

## What to do

1. `SecurityConfig` per §9: `EnvAllowlist []string`, `ImplementExecAllowlist []string`,
   `RequireCleanWorktree bool`, `MaxBudgetUSD float64`.
2. **Defaulting a bool to `true` needs care.** With yaml.v3 an absent key and an explicit
   `false` both unmarshal to `false`, so `require_clean_worktree: false` cannot be
   distinguished from "not set" by looking at the field. Use `*bool` in the YAML shape (or a
   custom `UnmarshalYAML`, as `ActionConfig`/`ExecutionConfig` already do in this codebase)
   and resolve to a plain `bool` on `Config`. Test both "absent → true" and
   "explicit false → false"; getting this wrong silently disables the control in T09.
3. Validation rules:
   - `env_allowlist` entries match `^[A-Z_][A-Z0-9_]*$` and are **not** on T07's deny list.
     Naming a denied credential variable is an error, not a silent drop — the operator
     believes they configured something.
   - `implement_exec_allowlist` entries are non-empty and contain no shell metacharacters
     (`;`, `|`, `&`, `` ` ``, `$(`, `>`, `<`, newline). They are argv patterns; a pattern
     with shell syntax means the author misunderstood the field, and the error should say so.
   - `max_budget_usd >= 0`.
4. `ValidatePolicies(cfg config.Config, registry *provider.Registry, mode domain.RunMode)
   error` walks every configured action, builds `PolicyFor(action, mode, root)`, applies the
   exec allowlist for `implement`, and calls `provider.Supports`. Returns the first
   `*apperr.PolicyError`.
5. Ordering in `app.go` per §8.9, and it is load-bearing:
   - `config.Load` → `Validate` → `ValidateDependencies` → **`ValidatePolicies`**, all before
     `registry`/`classifier` construction. `ValidatePolicies` needs the registry for
     `Capabilities()`, so registry construction moves above the check — that is fine
     (constructing adapters makes no calls) as long as no *provider call* precedes it.
   - **The mode is not always known at this point.** Without `--mode`, mode is chosen
     interactively after classification. So: this pass validates every mode-independent
     field, and the `Approval` field is re-checked immediately after `resolveMode` returns
     and before the first workflow step. Classification itself runs under the `classify`
     policy, which has no approval requirement, so no AI call precedes a complete check of
     the policy it runs under. Write this reasoning as a comment at both call sites — it is
     the single subtlest ordering claim in the spec.
6. Extend `runValidate` so `orchestrator validate` also runs `ValidatePolicies` (for every
   mode, since no mode is given) and prints the per-action authority summary plus the
   forwarded environment variable **names** (the Q2 substitute for a smoke test; T07 supplies
   the list, T10 the renderer — until then print the policy summary only).

## Acceptance

- Absent `security:` block leaves every existing config valid and behavior unchanged apart
  from the policy itself.
- `require_clean_worktree` absent → `true`; explicit `false` → `false`.
- Each validation rule has a rejecting test with a message naming the offending key.
- A config routing an action at a provider lacking the capability fails
  `orchestrator run` with exit 2 **and a fake provider registry recording zero `Execute`
  calls** — the assertion that matters is "no AI call happened", not just "an error".
- `orchestrator validate` reports the same failure without running anything.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
