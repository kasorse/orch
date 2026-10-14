# CA03 — adapter: environment per mode, `apiKeySource` check, `FailureAuth`

**Size** M (1–1.5 d) · **Depends on** CA02 · **Blocks** CA04, CA05, CA07

## Goal

The claude adapter runs every call (and the version probe) with the environment of the mode,
reads `apiKeySource` from the init event, fails on a mismatch, and classifies authentication
failures (`SPEC.md` §7–§9, §16 D4–D5).

## Files

- `internal/provider/claude/client.go` (+ test): `SetAuth(mode, key)`, `env()`
- `internal/provider/claude/stream.go` (+ test): `apiKeySource` from `system/init`; auth error
  categories from `system/api_retry`
- `internal/provider/claude/version.go`: use `env()`
- `internal/apperr`: `FailureAuth` kind (and its exit code mapping, as a config-like operator
  error)
- `internal/domain`: `CallReport`/execution field `APIKeySource`
- `internal/app`: call `SetAuth` after the fail-fast chain, before the first provider call

## Steps

1. `env()`: `execution.ScrubbedEnv()`, then the table of §7. A `Client` whose auth was never
   set refuses to run (backstop, like an unset authority).
2. Collect the names removed in `api` that were present, for one console notice (no values).
3. `parseStream` records `apiKeySource` and whether any `api_retry` carried an auth category.
4. After the call: §8 table; a mismatch or an auth category gives a `ProviderError` with
   `Kind: FailureAuth`, the mode in the message, and the key redacted from `Stdout`/`Stderr`.
5. Redaction: the same helper the Jev key goes through, applied in the adapter before the
   error or result leaves it.

## Acceptance

- Unit tests over a fixed environment: each row of §7 for both modes; `api_key_env` with a
  custom name reaches the child as `ANTHROPIC_API_KEY` and not under its own name.
- `parseStream` fixtures: init with each `apiKeySource` value; an `api_retry` with
  `billing_error`; no init event.
- A mismatch fails even when the exit code is 0 and the answer looks fine.
- The key does not appear in a `ProviderError`'s `Error()`, `Stdout` or `Stderr` when the fake
  CLI echoes it.

## Gates

`scripts/check.sh`
