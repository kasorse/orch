# CA01 — `providers.claude` section, key variable as a credential

**Size** S (0.5 d) · **Depends on** — · **Blocks** CA02

## Goal

The config accepts `providers.claude.{auth, api_key_env}` and treats the named variable as one
of the orchestrator's credentials (`SPEC.md` §5).

## Files

- `internal/config/providers.go` (+ test): `ProvidersConfig`, `ClaudeProviderConfig`
- `internal/config/config.go`: the field on `Config`, `CredentialEnvNames()` adds `api_key_env`
- `internal/config/validator.go`: validation problems
- the strict schema of a version-2 file

## Steps

1. `providers.claude.auth`: `""` (= `auto`), `auto`, `api`, `login`; anything else is a problem.
2. `api_key_env`: default `ANTHROPIC_API_KEY`; must match `envNameRE`; must not be a
   parent-session variable.
3. `providers.claude.api_key` is declared and rejected with the Jev wording.
4. Any key under `providers` other than `claude` is an unknown key (strict parsing).
5. `CredentialEnvNames()` includes the effective `api_key_env`, so `ScrubbedEnv`, `TestEnv` and
   `envPassProblems` cover it with no further change.

## Acceptance

- Table tests for every value and problem; the default when the section is absent.
- `CredentialEnvNames()` with and without the section (the default variable is in it).
- `test.env_pass: [ANTHROPIC_API_KEY]` is still refused.

## Gates

`scripts/check.sh`
