# CA07 — live check under `-tags security`

**Size** S (0.5 d) · **Depends on** CA03 · **Blocks** CA08 · **Needs** the owner's answer to
`SPEC.md` §15 Q5 and a Console key in the environment

## Goal

Confirm on the real CLI what this spec assumes from the docs, and record what a bad key looks
like in stream-json.

## Files

- `test/security/claude_auth_test.go`: skipped unless `ORCH_LIVE_ANTHROPIC_KEY` is set (and
  for the login case, unless the CLI is logged in)
- `specs/permissions/DECISIONS.md`: an entry with the CLI version and the observed events

## Checks (one cheap call each, smallest model, `AuthorityNone`)

1. `api` with a valid key → `apiKeySource: ANTHROPIC_API_KEY`, `total_cost_usd > 0`.
2. `login` → `apiKeySource` is not `ANTHROPIC_API_KEY`.
3. `api` with a syntactically valid but wrong key → record the exact events (an `api_retry`
   `authentication_failed`? a result error only?) and make `parseStream`'s classifier match.
4. `api` with `ANTHROPIC_AUTH_TOKEN` set in the parent → still `ANTHROPIC_API_KEY` (§7 works).

## Gates

`go test -tags security ./test/security/... -run ClaudeAuth`, plus `scripts/check.sh`.
