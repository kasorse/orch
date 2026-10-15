# CA04 — translator and decision provider do not swallow `FailureAuth`

**Size** S (0.5 d) · **Depends on** CA03 · **Blocks** CA06

## Goal

An authentication failure stops the run wherever it happens (`SPEC.md` §9).

## Files

- `internal/translate` (+ test): no fallback to the original text on `FailureAuth`
- `internal/classification` / `internal/decision/llm` (+ test): no manual choice on
  `FailureAuth`
- `internal/app`: the final message (mode, how chosen, what to try)

## Steps

1. `errors.As(err, *apperr.ProviderError)` with `Kind == FailureAuth` → return the error
   instead of the fallback / the `on_failure: ask` path.
2. Workflow steps need nothing: a provider error already fails the run. Check it does not
   count as a retryable implement failure.
3. Exit with the existing provider-error code; the message from §9.

## Acceptance

- Translator: a fake CLI with an auth failure → the run exits, no warning-and-continue.
- `decision.provider: llm`, `on_failure: ask`, interactive: no manual prompt on `FailureAuth`;
  a non-auth failure still prompts as before.

## Gates

`scripts/check.sh`

## As built

- `translate.Translator.Prepare` returns a `FailureAuth` `ProviderError` (with `Action` and `LogPath`
  filled as for any provider error) instead of the original text; every other failure still falls back.
- `llm.Provider` needed no change: it already wraps the `ProviderError` in a `DecisionError` whose
  `Unwrap` reaches it. `classification.Service.manual` returns the error without asking when
  `errors.As` finds a `FailureAuth` `ProviderError`, whatever `on_failure` says.
- `exitcode.FromError` checks `ProviderError` before `DecisionError`, so the wrapped error already
  exits 2; the message is the adapter's (CA03), so `internal/app` needed nothing.
- Workflow steps: a provider error already fails the run; only test/review outcomes retry implement.
- Tests: `TestPrepare_AuthFailureIsAnError`, `TestClassify_AuthFailureNeverAsks`.
- Status: implemented; `scripts/check.sh` is left to the operator.
