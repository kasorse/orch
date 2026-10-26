---
id: JR03
repo: orche
depends_on: [JR02]
jira: none
---
# JR03 — Child processes lose the Jira and Jev credential variables

**Size** S

## Goal

No provider CLI and no test command inherits a variable the config names as a credential:
`jira.token_env`, `jira.email_env` and `decision.jev.api_key_env` (`SPEC.md` F1, §12, D5). This
also closes the existing gap for `JEV_API_KEY`.

## Files

- `internal/execution/env.go`: the credential-name set, `ScrubCredentials`.
- `internal/config/config.go`: `(Config).CredentialEnvNames() []string`.
- `internal/app/app.go`: one call in `loadAndValidateConfig`.
- `internal/execution/env_test.go`, `internal/execution/export_test.go`,
  `internal/config/*_test.go`, the `internal/testexec` sandbox env test.

## Steps

1. `Config.CredentialEnvNames()` returns the trimmed, non-empty `decision.jev.api_key_env`
   (whatever the decision provider, since the variable may be set either way) and, when
   `cfg.Jira != nil`, `jira.token_env` and `jira.email_env`; sorted, deduplicated, `nil` when
   nothing is named.
2. `execution/env.go`: `credentialVars`, a set behind a `sync.RWMutex`, and
   `ScrubCredentials(names ...string)`, which adds exact names (never prefixes) and ignores empty
   ones. `scrub` drops a variable whose name is in `parentSessionVars` or in `credentialVars`,
   holding the read lock for the whole pass. The comment says why the state is process-wide: the
   adapters and the test runner are built before the config is loaded (`newRegistry`) and each
   calls `ScrubbedEnv()` at call time; the set only grows. The `ScrubbedEnv` comment says the
   credential variables are removed.
3. `loadAndValidateConfig` calls `execution.ScrubCredentials(cfg.CredentialEnvNames()...)` right
   after `config.Validate` succeeds, before `ValidateDependencies` and the bwrap probe. That
   covers `run`, `resume` (which runs the test command) and `validate`.
4. Git keeps the full environment. The Jira and Jev clients read their variables in `app`
   through `os.Getenv`, which this does not affect.
5. Tests: `scrub` is table-driven over an explicit environ slice; the `ScrubCredentials` test
   resets the set with `resetCredentialVarsForTest` (in `export_test.go`) through `t.Cleanup`.
   The sandbox env test registers a name only it uses (`ORCH_SANDBOX_TEST_TOKEN`), since it
   cannot see `execution`'s test helper.

## Documentation

- MVP.md §38.2, where `ScrubbedEnv` is described: it also removes the variables the config names
  as credentials (`jira.token_env`, `jira.email_env`, `decision.jev.api_key_env`).
- CLAUDE.md: the guard invariant (`execution.ScrubbedEnv()` also drops `Config.CredentialEnvNames`,
  registered right after `Validate`) and the Jev key sentence of "Secrets never leave the
  machine" (scrubbed from every child process).
- README.md, security section: the credential variables are scrubbed from provider CLIs and the
  test command.

## Acceptance

- `scrub` with `JIRA_API_TOKEN`, `JIRA_EMAIL` and `JEV_API_KEY` registered drops exactly those
  three (a bare `JIRA_EMAIL` without `=` too) plus the parent-session variables, and keeps
  `JIRA_API_TOKEN_OLD`, `XJEV_API_KEY`, a lower-case `jira_api_token`, `PATH` and `HOME`.
- A name registered after `ScrubbedEnv`'s first use is still removed on the next call.
- `CredentialEnvNames`: Jev only; an LLM decision provider with a jev block still returns
  `JEV_API_KEY`; a blank name is dropped; no jira section gives no Jira names; bearer gives no
  email name; duplicates appear once.
- The `internal/testexec` sandbox environment lacks a registered name.
- `scripts/check.sh` passes; the integration check of the test command's environment is
  scenario 19g in JR07.

## Notes

Do not edit anything under `specs/jira/`.

## As built (2026-10-26)

Built by hand on 2026-09-30, before this spec was rewritten as a story, as task J04 of the old
`specs/jira` plan: commit f4b58f5 on `master` ("Keep the Jira and Jev credential variables from every child process (J04)"). Marked done by hand on 2026-10-26:
no story run built it.
