# CA06 — integration scenarios

**Size** M (1 d) · **Depends on** CA04, CA05 · **Blocks** CA08

## Goal

End-to-end proof with the fake CLI (`internal/testutil/fakeexec`, `FAKE_ENV_DIR` records each
call's environment) of every mode and the corner cases of `SPEC.md` §13 that do not need a
real CLI.

## Scenarios (`test/integration`)

1. No section, key unset → `login`; no `ANTHROPIC_API_KEY` in any call's environment.
2. No section, `ANTHROPIC_API_KEY` set → `api`; every claude call (translate, decide, each
   step) has it; `ANTHROPIC_AUTH_TOKEN` and `CLAUDE_CODE_USE_BEDROCK` set in the parent are
   absent (§13 #1, #2).
3. `api_key_env: ORCH_CLAUDE_KEY`: the child sees `ANTHROPIC_API_KEY` with its value and no
   `ORCH_CLAUDE_KEY`; the test command's environment has neither.
4. `--claude-auth login` with the key set → absent everywhere; notice printed.
5. `--claude-auth api`, key unset → config error, zero fake calls.
6. Fake init event `apiKeySource: none` under `api` → the run fails at the first call with
   `FailureAuth`, translator did not fall back (§13 #7).
7. Fake `api_retry` `billing_error` in a step → run fails, message names the mode.
8. A run stopped by (7), then `resume --claude-auth login` → continues; `resumes[0].claude_auth`
   differs from the run's.
9. `actions.implement.verify: true` under `api`: the verify server's `run_tests` process
   environment has no `ANTHROPIC_API_KEY` (§12).
10. `codex`/`gemini` adapters' environment (unit level if no route is possible): no key.

## As built

- `test/integration/claude_auth_test.go`, one test per scenario (1–9). The fake CLI
  (`fakeexec.ProviderScript`) gained a stream-json mode: `FAKE_API_KEY_SOURCE` (`auto` = the key
  if the call's environment has it, else `none`) and `FAKE_API_RETRY_ERROR` /
  `FAKE_API_RETRY_ACTION` print a `system/init` (and `api_retry`) event and the answer as the
  `result` event.
- Scenario 10 is a unit test (`TestClient_GetsNoClaudeAPIKey` in `internal/provider/codex` and
  `internal/provider/gemini`): no route to codex or gemini is possible, since neither adapter
  supports any authority and config validation refuses every route to them. Scenario 2 also
  greps the whole run directory for the key (the check CA05 left here).
- The 'auth changed' notice of scenario 8 goes to stderr, so it is checked on both streams.
- Scenarios 6 and 7 assert the `FailureAuth` kind and that the message names the mode; 8 asserts
  `resumes[0].claude_auth` is `login` (by flag) while the run's stays `api`.
- Left to the operator: `scripts/check.sh` (root and `bench/`) and the run outside the sandbox;
  I could not run the new tests, so the task is not marked done and CA08 stays blocked.

## Gates

`scripts/check.sh`, and the script outside the sandbox once before closing.
