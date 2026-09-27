# T01 — `domain.Authority`, provider contract, fail-fast check

**Phase** 1 · **Size** S–M · **Depends on** — · **Blocks** T02, T03

## Goal

The provider-neutral vocabulary (`SPEC.md` §5), the adapter contract (§6.1), and the
startup check that refuses an unenforceable route before any AI call (§6.5).

## Files

- `internal/domain/authority.go` (+ test) — **new**
- `internal/provider/client.go` — `Request.Authority`, `Request.DenyRead`, `Client.Supports`
- `internal/provider/registry.go` — `Supports(providerID string, a domain.Authority) bool`
- `internal/apperr/errors.go` — `AuthorityError`
- `internal/exitcode/exitcode.go` — `AuthorityError` → 2
- `internal/config/authorities.go` (+ test) — **new**: `ValidateAuthorities`
- `internal/app/app.go` — registry construction moves up; call site; `runValidate`
- every provider test double (`internal/testutil`, `internal/action/dispatcher_test.go`,
  `internal/translate`, `internal/decision/llm` tests) — add `Supports`

## Steps

1. `Authority` with the four constants, `Valid() bool`, and
   `AuthorityFor(ActionType) Authority` backed by a package-level map (§5.1). Unknown
   action → `AuthorityNone`. `ActionTest` has no row (no AI call) and also yields `none`.
2. `provider.Request` gains `Authority domain.Authority` and `DenyRead []string`.
   `provider.Client` gains `Supports(domain.Authority) bool`.
3. `apperr.AuthorityError{Provider, Route, Authority, Reason}` — `Route` is the action or
   pseudo-action (`translation`, `decision`). Message names the remedy:
   ```
   provider codex cannot enforce the "read" authority required by action review

   codex's permission translation is unverified, so the orchestrator refuses rather
   than run it with unknown authority.

   remedy: route review to claude (actions.review.provider: claude)
   ```
   Exit code 2 in `exitcode.FromError`, next to `ConfigError`/`DependencyError`.
4. `config.ValidateAuthorities(cfg Config, checker AuthorityChecker) error`, where
   `AuthorityChecker` is `interface{ Supports(providerID string, a domain.Authority) bool }`
   — same pattern as `DependencyChecker`. Walks the six actions (`AuthorityFor`),
   `translation` and, if not Jev, `decision` (both `AuthorityNone`). Collects all problems,
   like `ValidateDependencies`.
5. codex/gemini `Supports` false for everything (their refusal in `Execute` stays in T02);
   claude `Supports` true for every valid authority with a `TODO(T02)` — T02 makes it true in
   fact. Re-point `orchestrator.example.yaml` `review` to claude, or the example stops
   validating.
6. `app.go`: build the registry right after `loadAndValidateConfig` (it makes no calls), then
   `ValidateAuthorities(cfg, registry)`. `runValidate` builds a registry too and runs it.
   Comment: authority is mode-independent (`SPEC.md` §5.2), so this one check is complete.

## Acceptance

- `AuthorityFor` table test pins every action; an unknown action yields `none`.
- A config routing `review` to a provider whose `Supports` is false → `run` and `validate`
  exit 2; a recording fake registry shows **zero `Execute` calls**.
- `errors.As` finds `*AuthorityError` through `%w` wrapping; exitcode table test covers it.
- `go list -deps ./internal/workflow ./internal/policy | grep orchestrator/internal` shows
  only `domain`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
