# J01 — `jira` config section, validation, `DependencyOptions`

**Size** S–M (0.75 d) · **Depends on** — · **Blocks** J02, J04, J06

## Goal

The configuration can describe a Jira instance. A malformed section fails `Validate`
wherever it appears. The credential variables are checked in the fail-fast chain only when
the run needs them, and always by `orchestrator validate` (`SPEC.md` §7).

## Files

- `internal/config/jira.go` (new): `JiraConfig`, `JiraField`, `UnmarshalYAML`,
  `validateJira`, `jiraCredentialProblems`, defaults
- `internal/config/config.go`: `Config.Jira *JiraConfig \`yaml:"jira"\``
- `internal/config/validator.go`: call `validateJira` when `cfg.Jira != nil`
- `internal/config/dependencies.go`: `DependencyOptions`, new `ValidateDependencies`
  signature
- `internal/app/app.go`: pass the options through `loadAndValidateConfig` (only the
  plumbing; `run` passes `Jira: false` until J06)
- `internal/config/jira_test.go`, `dependencies_test.go`, `validator_test.go`

## Steps

1. `JiraConfig{BaseURL, Auth, EmailEnv, TokenEnv string; Timeout time.Duration; MaxRetries,
   MaxBytes int; Fields []JiraField}`, with `JiraField{ID, Label string}` and the constants
   `JiraAuthBasic = "basic"` and `JiraAuthBearer = "bearer"`. Use a pointer on `Config`, so
   "section absent" is `nil`, not a zero value that fails validation.
2. `UnmarshalYAML`: parse `timeout` through `parseDurationField`. Record literal
   `token`/`api_token`/`password`/`pat`/`email` keys as booleans without keeping their
   values (mirror `JevConfig.apiKeyLiteralSet`). Apply defaults for absent keys: timeout
   10s, `max_retries` 1, `max_bytes` 32768. Distinguish absent from explicit `0` for
   `max_retries` (a `*int` in the raw struct).
3. `validateJira(j) []string`, with messages prefixed `jira.`:
   - `base_url`: parses, absolute, no query or fragment, `https`; `http` only when the
     hostname is `localhost`, `127.0.0.1` or `::1`. A trailing `/` is allowed and trimmed.
   - `auth` ∈ {basic, bearer}. `token_env` is required. `email_env` is required for
     basic and is an error for bearer. Env names match `^[A-Za-z_][A-Za-z0-9_]*$`.
   - Each literal secret key → `jira.<key> must not be set in the configuration file;
     name an environment variable via jira.token_env / jira.email_env instead`.
   - `timeout > 0`, `max_retries` ∈ [0, 3], `max_bytes` ∈ [1024, 262144].
   - `fields[i].id` matches `^customfield_[0-9]+$`. `label` is non-empty, has no `\n` and
     is not reserved (case-insensitive `summary`, `description`, `parent`, `type`,
     `source`, `operator notes`). Ids and labels are unique.
4. `DependencyOptions{Jira bool}`. `ValidateDependencies(cfg, checker, opts)` appends
   `jiraCredentialProblems` when `opts.Jira`:
   - `cfg.Jira == nil` → `--jira needs a jira section in the configuration (see
     orchestrator.example.yaml)`
   - each named variable unset or empty → `jira.token_env names JIRA_API_TOKEN, which is
     not set`. Never print the value.

   Read the environment through an injected `getenv func(string) string` field or
   parameter, as the Jev key check does, so tests don't mutate the process environment.
5. `loadAndValidateConfig` gains `opts config.DependencyOptions`. `runValidate` passes
   `Jira: cfg.Jira != nil`, which needs the loaded config: compute it inside
   `loadAndValidateConfig` via a `func(config.Config) config.DependencyOptions`, or split
   the call. `run` and `resume` pass `Jira: false` for now.

## Acceptance

- Config without `jira`: `Validate` and `ValidateDependencies` produce the same results as
  before (existing tests unchanged).
- A table test for every rule in step 3, including an aggregated multi-problem message.
- `http://127.0.0.1:8080` is valid. `http://jira.corp` and `ftp://…` are rejected.
- `max_retries: 0` stays 0, and an absent one becomes 1.
- A literal `token: abc` is rejected, and `abc` does not appear in the error.
- `Jira: true` with the section missing, with the token unset, or with the email unset
  (basic) → one `DependencyError` listing each problem. `Jira: false` with unset
  variables → nil.
- `orchestrator validate` with a `jira` section and no variables → exit 2.

## As built

Done 2026-09-30. As planned, with these differences:

- **Literal secrets** are stored as `literalSecrets []string` (the key names, in the fixed
  order `token, api_token, password, pat, email`), not as five booleans. A key counts when
  it is present, even with an empty or null value: `UnmarshalYAML` does a second pass that
  reads only the keys. `Jev` counts only a non-empty `api_key`, but an empty `token:` is
  still a mistake.
- **`base_url` with userinfo** (`https://me:secret@host`) is rejected as well. The step
  list did not mention it, but it would be a literal credential in the file.
- **Defaults**: `max_bytes` is a `*int` in the raw struct, like `max_retries`, so an
  explicit `0` reaches validation and is rejected instead of silently becoming 32768.
  An absent or empty `timeout` becomes 10s.
- **Trailing `/`** is trimmed in `UnmarshalYAML`. `validateJira` accepts either form, so
  a `JiraConfig` built in Go is validated the same way.
- **Labels**: uniqueness and the reserved check are case-insensitive and ignore
  surrounding whitespace. `\r` counts as a line break, the same as `\n`.
- **`getenv`** is an unexported field of `DependencyOptions` (nil → `os.Getenv`). The
  Jev key check still uses `os.LookupEnv` directly. Contrary to step 4, the Jev check
  never had an injection point, and it was left unchanged.
- **Blank values**: a variable holding only whitespace counts as unset, the same as the
  Jev key.
- **App plumbing**: `loadAndValidateConfig` takes a
  `func(config.Config) config.DependencyOptions`. `run` and `resume` pass
  `noJiraDependencies`. `validate` passes `Jira: cfg.Jira != nil`.
- **Tests**: `internal/config/jira_test.go` covers unmarshalling, defaults, literal
  secrets, a table over every §3 rule, and aggregation with the rest of `Validate`.
  `dependencies_test.go` has a table over `DependencyOptions`.
  `test/integration/jira_config_test.go` checks that `orchestrator validate` with the
  section and no variables exits 2, naming both variables, and passes once they are set.
  Scenario 19h (a `run` without `--jira` is unaffected) is left for J07.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
