# J04 — Child processes lose the Jira and Jev credential variables

**Size** S (0.5 d) · **Depends on** J01 · **Blocks** J06

## Goal

No provider CLI and no test command inherits a variable that config names as a credential:
`jira.token_env`, `jira.email_env` and `decision.jev.api_key_env` (`SPEC.md` F1, §12,
default D5). This also closes the existing gap for `JEV_API_KEY`, and the task can ship on
its own before the rest of the feature.

## Files

- `internal/execution/env.go`: the credential-name set, `ScrubCredentials`
- `internal/config/config.go` (or `jira.go`): `(Config).CredentialEnvNames() []string`
- `internal/app/app.go`: one call in `loadAndValidateConfig`
- `internal/execution/env_test.go`, `internal/config/*_test.go`

## Steps

1. `Config.CredentialEnvNames()` returns the non-empty `decision.jev.api_key_env` (whatever
   the decision provider, since the variable may be set either way), plus, when
   `cfg.Jira != nil`, `jira.token_env` and `jira.email_env`. The result is sorted and
   deduplicated.
2. In `execution/env.go`, add `credentialVars`, a set guarded by a `sync.RWMutex`, and
   `ScrubCredentials(names ...string)`, which adds exact names (never prefixes, per the
   file's rule) to it. `scrub` drops a variable when its name is in `parentSessionVars` or
   in `credentialVars`. Document why this is process-wide state: the provider adapters and
   the test runner are constructed before the config is loaded (`newRegistry`), and every
   one of them calls `ScrubbedEnv()` at call time. The set is only ever added to.
3. In `loadAndValidateConfig`, call `execution.ScrubCredentials(cfg.CredentialEnvNames()...)`
   right after `config.Validate` succeeds, before `ValidateDependencies` and the bwrap
   probe. This covers `run`, `resume` and `validate` the same way, and `resume` needs it
   because it runs the test command.
4. Git keeps `Env: nil` (full inheritance), unchanged. The Jira and Jev clients read their
   variables through `os.Getenv` in `app`, which is unaffected.
5. Tests: `scrub` is table-driven over an explicit environ slice. A `ScrubCredentials` test
   resets the set with a test-only helper (`resetCredentialVarsForTest` in an
   `export_test.go`) through `t.Cleanup`.

## Acceptance

- `scrub` with `JIRA_API_TOKEN`, `JIRA_EMAIL` and `JEV_API_KEY` registered drops exactly
  those three plus the parent-session variables, and keeps `JIRA_API_TOKEN_OLD`,
  `PATH` and `HOME`.
- `CredentialEnvNames`: an LLM decision provider with a jev block still returns
  `JEV_API_KEY`. No jira section → no Jira names. Bearer auth → no email name.
- `internal/testexec` sandbox `env()` (which builds on `ScrubbedEnv`) also lacks the
  names: extend its existing env test.
- The integration check is scenario 19g (J07).

## As built

Done 2026-09-30. As planned, with these differences:

- **`CredentialEnvNames`** is in `internal/config/config.go`. It trims
  `decision.jev.api_key_env` (as `jevKeyProblems` does), drops empty names, and returns
  `nil` when nothing is named.
- **`ScrubCredentials`** ignores empty names. `scrub` holds the read lock for the whole
  pass. The `ScrubbedEnv` comment now says the credential variables are removed too;
  before, it said auth variables are inherited.
- **Where it runs**: the call sits in `loadAndValidateConfig` right after `Validate`, so
  it applies before `ValidateDependencies` and the bwrap probe. That covers `run`,
  `resume` and `validate` alike.
- **Sandbox test**: `TestSandboxEnv` registers a name only it uses
  (`ORCH_SANDBOX_TEST_TOKEN`) instead of resetting the set. The reset helper lives in
  `execution`'s `export_test.go`, which `testexec` tests cannot see, and the set only
  grows.
- **Tests**:
  - `TestScrub_CredentialVars` drops exactly the three names plus the parent-session
    variables, including a bare `JIRA_EMAIL` with no `=`. It keeps `JIRA_API_TOKEN_OLD`,
    `XJEV_API_KEY`, a lower-case `jira_api_token`, `PATH` and `HOME`.
  - `TestScrubbedEnv_CredentialVarsRegisteredLater` checks that the set is read at call
    time.
  - `TestConfig_CredentialEnvNames` covers Jev only, an LLM provider (the Jev name is
    kept), a blank name, basic, bearer and duplicates.
- **Integration check**: scenario 19g (the test command's environment) is left for J07,
  as planned. Integration scenarios 1–18 pass unchanged; no fake provider relied on
  `JEV_API_KEY`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
