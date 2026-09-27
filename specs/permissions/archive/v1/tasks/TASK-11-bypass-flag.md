# T11 — `--dangerously-bypass-permissions` with a hard isolation precondition

**Phase** 3 · **Size** S · **Depends on** T03, T05, T10 · **Blocks** T12

## Goal

One escape hatch, deliberately inconvenient, always recorded. Per the Q1 resolution it
**refuses** outside a detected sandbox — no typed-confirmation fallback.

## Files

- `internal/app/app.go` — `parseArgs`, the isolation check, threading `Bypass` into policy
- `internal/domain/policy.go` — a `Bypass` override applied after `PolicyFor`
- `internal/logging/progress.go` — banner (shared with T10)
- tests

## What to do

1. Parse `--dangerously-bypass-permissions` in `parseArgs`. The name is the documentation:
   do not add a short form, and do not accept an `ORCHESTRATOR_BYPASS` env var — an
   environment variable is exactly the invisible, inherited switch this must not be.
2. Isolation check, before anything else that matters: `ORCHESTRATOR_SANDBOX=1` in the
   environment, **or** `/.dockerenv` exists. Otherwise `*apperr.ConfigError` (exit 2) naming
   both ways to satisfy it. Note the check reads the *orchestrator's* environment, not the
   allowlisted child env from T07 — they are unrelated, and conflating them would make the
   check bypassable by config.
3. Refuse in combination with `--mode manual` (§8.4 rule 2): asking a human per tool call
   while disabling all checks is incoherent, and the combination almost certainly means the
   operator misunderstood one of the two flags. Say which.
4. Apply as an override after `PolicyFor`, not as a parameter to it. `PolicyFor` stays a pure
   function of `(action, mode, root)` — keeping the table free of the escape hatch is what
   makes the table auditable. A small `domain.BypassPolicy()` (or
   `Policy.AsBypass()`) that produces the `Bypass`-only policy, with `Validate` enforcing
   §8.1's "`Bypass` with any other restriction is invalid", is the clean shape.
5. `Bypass` implies the claude adapter drops `--restricted` (T03 step 3). Verified on 2.1.278:
   `--restricted` refuses `bypassPermissions` outright, so the two cannot coexist.
6. Record `"bypass": true` in `policy.json` and in every step's audit record (T12), so a run
   done this way can never be mistaken for a constrained one after the fact.

## Acceptance

- Without `ORCHESTRATOR_SANDBOX=1` and without `/.dockerenv` → exit 2, message naming both
  options, **zero AI calls**.
- With `ORCHESTRATOR_SANDBOX=1` → runs, banner on stderr, `bypass: true` recorded.
- With `--mode manual` → exit 2 regardless of sandbox detection.
- The bypass policy passes `Validate` and every adapter's `Supports` (bypass requires no
  enforcement, so no provider can refuse it for lack of capability).
- `grep -rn "bypassPermissions" internal/` still matches only T03's branch, its comment, and
  tests.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
