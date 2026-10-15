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

## As built

- `claude.Client.SetAuth(mode, key)`, `env()`, the `apiKeySource` check and the redaction are in
  `internal/provider/claude/auth.go`; `Execute` and `CLIVersion` call `env()`/`buildEnv`. A
  `Client` whose auth was never set returns a `ConfigError` and runs nothing. State is behind an
  `RWMutex`, so parallel review calls are safe.
- `app.applyClaudeAuth` runs inside `loadAndValidateConfig` right after `ResolveClaudeAuth` and
  **before** `checkProviderCLIs`: the version probe is the first `claude` process and gets the
  mode's environment (SPEC §7), so one call covers `run`, `resume` and `validate`. It reads the key
  once (`os.LookupEnv`, trimmed) only in `api` mode, type-asserts the registry's claude client to
  `*claude.Client`, and adds `ignored in api mode: …` to the notices the reporter already prints.
- `apperr.FailureAuth` maps to exit code 2 (like `DecisionAuth`). `ProviderError` got a `Detail`
  line, rendered as `detail: …`, because `Error()` does not print `Err`; the auth message
  (`claude auth (api): billing_error. Check the Console account, or run with --claude-auth login`)
  lives there. It names the mode but not how it was chosen: the adapter does not know that.
- The auth check runs on every call that ended normally or by timeout, whatever the exit code; a
  cancelled or unspawnable call is not checked. `domain.CallReport.APIKeySource` carries what the
  init event said (`api_key_source` in `run.json`); persisting it for decisions is CA05.
- The key is redacted (`[REDACTED]`, as the Jev key is) from the process's stdout and stderr
  before anything is parsed, from the wrapped error text (the chain still unwraps, so a timeout
  stays a timeout), and from the live stream and the activity lines.
- `parseStream` reads `apiKeySource` and `api_retry`'s `error` as raw JSON and takes strings only,
  so an undocumented shape costs the field, not the event. Only the four categories of SPEC §9 are
  auth; a non-retryable 401 is still CA07's.
- Scenario 25 sets `ANTHROPIC_API_KEY` with no `providers.claude.auth`, so the run resolves to
  `api` (auto) and now expects the claude CLI to get the key back as `ANTHROPIC_API_KEY`, while the
  test command still does not (the CA01 expectation that no child gets it is undone here).
- Not done here: the translator and the LLM decision provider still treat a `FailureAuth` like any
  provider error (CA04).
- Status: implemented; `scripts/check.sh` is left to the operator.
