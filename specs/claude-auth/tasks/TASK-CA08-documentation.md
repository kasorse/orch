# CA08 — MVP.md §46, CLAUDE.md, README, TODO.md

**Size** S (0.5 d) · **Depends on** all

## Steps

1. MVP.md: a new §46 "Claude credentials" (normative: modes, resolution table, environment
   table, the `apiKeySource` check, `FailureAuth`, persistence, resume); the config example in
   §5 gets the `providers` block; §7 names the resolution step in the fail-fast chain; §43 the
   summary line.
2. CLAUDE.md: one line in the spec list; the invariant "the claude credential is set by the
   adapter for the resolved mode and checked against `apiKeySource`"; `claude_auth` and
   `api_key_source` under Run artifacts.
3. README: how to run on an API key; `--claude-auth`.
4. `bench/README.md`: the baseline `claude` gets the operator's environment as is (§13 #20).
5. TODO.md: tick P11, keep P12 open; P6 item 2 refers to this spec for `ANTHROPIC_API_KEY`.
6. This README's table and `Status:` line; `## As built` in every task.
