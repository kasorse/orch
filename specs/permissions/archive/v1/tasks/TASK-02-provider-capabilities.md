# T02 — Provider capability declaration, `Supports`, and `PolicyError`

**Phase** 1 · **Size** S–M · **Depends on** T01 · **Blocks** T03, T04, T05, T06

## Goal

Make "this provider cannot enforce what this policy requires" a typed, fail-closed error
that is impossible to reach an AI call through. This is goal G2; the rest of the spec rests
on it, because every per-CLI translation below is only trustworthy if the alternative to
enforcement is refusal rather than silence.

## Files

- `internal/provider/client.go` — `Request.Policy`, `Capabilities`, `Client.Capabilities()`,
  `Supports`
- `internal/apperr/errors.go` — `PolicyError`
- `internal/exitcode/exitcode.go` — `PolicyError` → `ConfigOrDependency` (2)
- `internal/provider/registry.go` (or wherever `Registry` lives) — unchanged interface, but
  confirm `Resolve` still returns the concrete `Client` so `Capabilities()` is reachable
- tests alongside each

## What to do

1. Add `Policy domain.Policy` to `provider.Request`. `internal/provider` importing
   `internal/domain` is consistent with the dependency rules in `CLAUDE.md` (only
   `internal/workflow` is restricted to `domain` alone).
2. Add `Capabilities` per §8.9 and extend `Client` with `Capabilities() Capabilities`. Every
   existing fake/stub in tests needs the method — expect to touch
   `internal/testutil` and the provider test doubles.
3. `Supports(caps Capabilities, p domain.Policy) error` returns `*apperr.PolicyError` naming
   the **first** unsupported mandatory field. Advisory fields never cause an error; instead
   `Supports` reports them separately so callers can record them:
   `Supports` returns `(ignoredAdvisory []string, err error)`. T12 persists that list, so
   decide the signature here rather than bolting it on later.
4. `PolicyError` renders provider, action, the unsupported field, and a **remedy** — the
   error text is the entire user experience of a fail-closed system, so it must say what to
   do, not only what is wrong. Target shape:

   ```
   provider cannot enforce the required permission policy

   provider: gemini
   action:   answer
   field:    DenyExec (the answer action must not be able to run commands)

   gemini's argv translation for this restriction is unverified, so the orchestrator
   refuses rather than run with unknown authority.

   remedies:
     - route answer at a provider with a verified translation:
         actions.answer.provider: claude
     - or verify the translation and declare the capability (specs/permissions/SPEC.md §12)
   ```

5. Map `PolicyError` to exit code 2 in `exitcode.FromError`, ordered with the other
   `ConfigError`/`DependencyError` cases. It is a misconfiguration, not a provider runtime
   failure — a `ProviderExecution` (3) here would mislead anyone reading exit codes in CI.

## Acceptance

- `Supports` rejects each mandatory field independently (one subtest per field), and the
  message names the field.
- A policy needing only advisory fields the adapter lacks **succeeds** and reports
  `ignoredAdvisory == ["max_budget_usd"]`.
- `exitcode.FromError(&apperr.PolicyError{})` is 2, asserted in
  `internal/exitcode`'s existing table test.
- `errors.As` unwraps `PolicyError` through at least one layer of `%w` wrapping, since
  `app.go` wraps errors on several paths.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
