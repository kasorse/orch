---
id: JR02
repo: orche
depends_on: [JR01]
jira: none
---
# JR02 — The jira config section, validation, DependencyOptions

**Size** M

## Goal

The configuration can describe a Jira instance. A malformed section fails `Validate` wherever it
appears. The credential variables are checked in the fail-fast chain only when the run needs
them, and always by `orchestrator validate` when the section is present (`SPEC.md` §7).

## Files

- `internal/config/jira.go` (new): `JiraConfig`, `JiraField`, `UnmarshalYAML`, `validateJira`,
  `jiraCredentialProblems`, defaults.
- `internal/config/config.go`: `Config.Jira *JiraConfig` (`yaml:"jira"`); a pointer, so an absent
  section is `nil`, not a zero value that fails validation.
- `internal/config/validator.go`: call `validateJira` when `cfg.Jira != nil`.
- `internal/config/dependencies.go`: `DependencyOptions`, the new `ValidateDependencies`
  signature.
- `internal/app/app.go`: the plumbing through `loadAndValidateConfig`.
- `internal/config/jira_test.go`, `dependencies_test.go`, `validator_test.go`;
  `test/integration/helpers_test.go` (`configOptions.jira`), `test/integration/jira_config_test.go`.

## Steps

1. `JiraConfig{BaseURL, Auth, EmailEnv, TokenEnv string; Timeout time.Duration; MaxRetries,
   MaxBytes int; Fields []JiraField}`, `JiraField{ID, Label string}`, constants
   `JiraAuthBasic = "basic"`, `JiraAuthBearer = "bearer"`.
2. `UnmarshalYAML`: `timeout` through `parseDurationField`; an absent or empty one is 10s.
   `max_retries` and `max_bytes` are `*int` in the raw struct, so an explicit `0` reaches
   validation (`max_retries: 0` stays 0, `max_bytes: 0` is refused) and an absent one gets its
   default (1, 32768). A trailing `/` of `base_url` is trimmed here; `validateJira` accepts both
   forms, so a `JiraConfig` built in Go validates the same way. Literal secret keys (`token`,
   `api_token`, `password`, `pat`, `email`) are recorded as `literalSecrets []string` - the key
   names in that fixed order, never their values - by a second pass that reads only the keys, so
   a key counts even with an empty or null value.
3. `validateJira(j) []string`, messages prefixed `jira.`:
   - `base_url`: parses, absolute, no query, fragment or userinfo (`https://me:secret@host` is a
     literal credential), `https`; `http` only for the hostname `localhost`, `127.0.0.1` or `::1`.
   - `auth` in {basic, bearer}; `token_env` required; `email_env` required for basic and an
     error for bearer; env names match `^[A-Za-z_][A-Za-z0-9_]*$`.
   - Each literal secret key: `jira.<key> must not be set in the configuration file; name an
     environment variable via jira.token_env / jira.email_env instead`.
   - `timeout > 0`, `max_retries` in [0, 3], `max_bytes` in [1024, 262144].
   - `fields[i].id` matches `^customfield_[0-9]+$`; `label` non-empty, without `\n` or `\r`, not
     reserved (`summary`, `description`, `parent`, `type`, `source`, `operator notes`); ids and
     labels unique. The reserved check and label uniqueness ignore case and surrounding
     whitespace.
4. `DependencyOptions{Jira bool}` with an unexported `getenv` field (nil means `os.Getenv`), so
   tests do not touch the process environment; the Jev key check is left as it is.
   `ValidateDependencies(cfg, checker, opts)` appends `jiraCredentialProblems` when `opts.Jira`:
   `cfg.Jira == nil` gives `--jira needs a jira section in the configuration (see
   orchestrator.example.yaml)`; each named variable unset, empty or only whitespace gives
   `jira.token_env names JIRA_API_TOKEN, which is not set`. A value is never printed.
5. `loadAndValidateConfig` takes a `func(config.Config) config.DependencyOptions`. `validate`
   passes `Jira: cfg.Jira != nil`; `run` and `resume` pass `noJiraDependencies` (JR06 changes
   `run`).
6. `configOptions.jira *jiraOptions{baseURL, fields}` in `test/integration/helpers_test.go`
   renders a basic-auth `jira` section after everything else, only when set, so every existing
   scenario keeps an identical config. `jira_config_test.go` uses it.

## Documentation

- MVP.md §5 (a commented `jira` block in the example config), §6 (the section's validation), §7
  (the variables are checked only with `--jira`, and by `validate` when the section is present).
- `orchestrator.example.yaml`: the `jira` block of `SPEC.md` §7, commented out, one comment per
  key; `TestLoad_ExampleConfigIsValid` still passes.
- CLAUDE.md, the fail-fast invariant: the Jira section and its variables are checked only with
  `DependencyOptions{Jira: true}`.

## Acceptance

- A config without `jira`: `Validate` and `ValidateDependencies` give the same results as before
  (existing tests unchanged).
- A table test covers every rule of step 3, including an aggregated multi-problem message.
- `http://127.0.0.1:8080` is valid; `http://jira.corp` and `ftp://...` are refused.
- `max_retries: 0` stays 0 and an absent one becomes 1; `max_bytes: 0` is refused.
- A literal `token: abc` is refused and `abc` is not in the error; an empty `token:` is refused
  too.
- `Jira: true` with the section missing, the token unset, or the email unset (basic) gives one
  `DependencyError` listing each problem; `Jira: false` with unset variables gives nil.
- `orchestrator validate` with a `jira` section and no variables exits 2 naming both variables,
  and passes once they are set (`test/integration/jira_config_test.go`).
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira/`.
