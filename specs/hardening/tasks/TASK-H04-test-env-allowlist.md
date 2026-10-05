# H04 — Test environment allowlist and `test.env_pass`

**Size** M (1–1.5 d) · **TODO** S2 · **Depends on** — · **Blocks** H05, H07

## Goal

Every process that runs the test command (and the bwrap probe) gets an allowlisted
environment; the developer's credentials never reach model-written code (`SPEC.md` §6.1, §6.2).

## Files

- `internal/execution/env.go` (+ test): `TestEnv`, `SetTestEnvPass`
- `internal/testexec/runner.go`, `sandbox.go` (+ tests)
- `internal/config/config.go`, `validator.go` (+ tests): `TestConfig.EnvPass`
- `internal/app/app.go`: register `env_pass` right after `Validate`, next to `ScrubCredentials`
- `test/security/sandbox_test.go`

## Steps

1. `execution`:
   - `testEnvBase` and `testEnvToolchain`: exact-name sets from `SPEC.md` §6.1, commented with
     why proxies are absent.
   - `SetTestEnvPass(names ...string)`: process-wide, add-only, like `ScrubCredentials`
     (same reasoning: adapters and the runner are built before the config is loaded).
   - `TestEnv() []string`: keep `os.Environ()` entries whose name is in base ∪ toolchain ∪
     pass, and not in `parentSessionVars` or `credentialVars`. Keep the original order.
2. `testexec.Runner.Run` uses `execution.TestEnv()`; `Sandbox.env()` filters `TestEnv()` and
   still forces `TMPDIR=/tmp`; `Probe` uses `TestEnv()`.
3. Config: `test.env_pass []string`. Validation per `SPEC.md` §6.2: name regex; secret-like
   substrings (list in one place, with the rule in the error message); a declared credential
   name (`CredentialEnvNames`) or a parent-session variable is an error. Duplicates are
   collapsed silently.
4. `app.loadAndValidateConfig`: `execution.SetTestEnvPass(cfg.Test.EnvPass...)` right after
   `ScrubCredentials`.
5. Provider CLIs are untouched: assert in a test that `claude.Execute` still passes
   `ScrubbedEnv()` (an unrelated variable like `FOO_BAR` reaches the provider, not the test).

## Acceptance

- Unit: with `ANTHROPIC_API_KEY`, `AWS_SECRET_ACCESS_KEY`, `GITHUB_TOKEN`, `SSH_AUTH_SOCK`,
  `GOPROXY`, `GOFLAGS`, `PATH` set, `TestEnv()` keeps exactly `GOFLAGS` and `PATH`; with
  `env_pass: [GOPROXY]` also `GOPROXY`; a credential name in `env_pass` is still removed.
- Config: `GITLAB_TOKEN`, `MY_AUTH_HEADER`, `db-url`, `JIRA_API_TOKEN` (declared credential)
  each rejected with a message naming the rule; `DATABASE_URL` accepted.
- Integration (fake test command `env > $FAKE_ENV_DIR/test.env`): the test env holds no
  `ANTHROPIC_API_KEY` and the provider env still does (if set in the harness).
- Live (`-tags security`, sandbox test): `printenv ANTHROPIC_API_KEY` inside the sandbox prints
  nothing.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-05. As planned, with these differences:

- **Names:** `execution.AllowTestEnv` (not `SetTestEnvPass`: the set is add-only, like
  `ScrubCredentials`) and `execution.IsParentSessionVar`, which config validation uses so the
  parent-session list stays in one place. `config` now imports `execution` (no cycle:
  `execution` imports nothing internal).
- **Credential-like names are matched by substring _and_ by segment.** `TOKEN`, `SECRET`,
  `PASSWORD`, `PASSWD`, `CREDENTIAL`, `APIKEY`, `PRIVATEKEY` anywhere in the upper-cased
  name; `AUTH`, `KEY`, `KEYS`, `PASS`, `COOKIE` only as a whole `_`-separated segment. The
  spec's plain "contains AUTH" would have refused `GIT_AUTHOR_NAME`; segments still catch
  `SSH_AUTH_SOCK`, `MY_AUTH_HEADER`, `STRIPE_KEY`, `AWS_ACCESS_KEY_ID`, `DB_PASS`. `PWD` is
  allowed (it is the working directory).
- **The bwrap probe uses `TestEnv()` too**, as specified. Integration scenario 16's fake bwrap
  read `FAKE_BWRAP_LOG`/`FAKE_BWRAP_FAIL` from the environment, which no longer reaches it;
  the fake now gets the log path and the failure flag baked into the script
  (`installFakeBwrap(t, fail)`), rather than loosening the allowlist for a test.
- Scenario 19g's test command took its output path from `$ENV_OUT`; the path is now in the
  command itself. New scenario 25: the test command sees `PATH`, `HOME`, `TZ` and an
  `env_pass` name but not `ANTHROPIC_API_KEY` or an unrelated variable, while the provider CLI
  still sees both; a credential-like `env_pass` name is refused before any call.
- The live sandbox check (`test/security/sandbox_test.go`) now also asserts that the test
  command cannot read `ANTHROPIC_API_KEY` and still has `PATH`, with and without bwrap; it
  ran green on this host (real bwrap, no model calls).
- `orchestrator.example.yaml` documents the allowlist and `env_pass` under `test:`.
