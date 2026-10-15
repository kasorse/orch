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

## As built

- 2026-10-15. MVP.md: §46 "Claude credentials" (46.1–46.7), the `providers` block in the §5
  example, a "Claude credential resolution" paragraph in §7, a `Claude auth` row in §43.2.
  CLAUDE.md: the spec-list entry, the invariant, `claude_auth` and `call.api_key_source` under
  Run artifacts. README: a "Running claude on an API key" section and `--claude-auth` in the
  usage block. `bench/README.md`: the baseline note, next to the container section.
- `api_key_source` is documented as `call.api_key_source`, where CA05 put it, not
  `provider.api_key_source` as the spec says.
- TODO.md: P11 is **not** ticked and P12 stays open. P11 waits on `scripts/check.sh` (root and
  `bench/`), which none of CA01–CA08 has run, and on CA07's live check; the entry says so. P6
  item 2 now points to P11 and MVP.md §46 for `ANTHROPIC_API_KEY`.
- The README's `Status:` line and table say "written", not "done", for the same reason.
  Documentation only: no code or test changed, so there is no new gate to run.
