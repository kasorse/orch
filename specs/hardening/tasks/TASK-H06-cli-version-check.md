# H06 — Provider CLI version check

**Size** M (1–1.5 d) · **TODO** S3 · **Depends on** H01 (fake CLI script) · **Blocks** H13

## Goal

The orchestrator refuses, before any AI call, a provider CLI version its permission flags were
not verified on, unless the operator explicitly overrides; the version is recorded
(`SPEC.md` §7).

## Files

- `internal/provider/client.go`: `VersionVerifier` interface
- `internal/provider/claude/version.go` (new) + test: range, parsing, `CLIVersion`
- `internal/config/config.go`, `validator.go`: `security.allow_unverified_cli`
- `internal/config/authorities.go`: export the distinct routed providers (reuse `authorityRoutes`)
- `internal/app/app.go` (`loadAndValidateConfig`, `runValidate`), `resume.go`
- `internal/domain/run.go`: `ProviderCLI`, `Run.ProviderCLI`, `Resume.ProviderCLI`
- `internal/persistence` validation
- `internal/logging`: header warning, `validate` output
- `internal/testutil/fakeexec/harness.go`: `--version` → `${FAKE_CLI_VERSION:-2.1.289 (Claude Code)}`

## Steps

1. `claude/version.go`: `verifiedMin = [3]int{2,1,283}`, `verifiedMaxExclusive = [3]int{2,2,0}`
   with a comment pointing at `specs/permissions/DECISIONS.md` and the rule for raising them.
   `parseVersion(firstLine) ([3]int, bool)` with `^(\d+)\.(\d+)\.(\d+)(\s|$)`.
   `(*Client).CLIVersion(ctx)`: run `claude --version` via the executor with
   `ScrubbedEnv()`, 10 s timeout; non-zero exit or exec error → `err`; return the trimmed first
   line (≤ 64 bytes) and `verified`.
2. `config`: `security.allow_unverified_cli bool` in the schema; `ProvidersInUse(cfg) []string`
   from the routes (distinct, sorted, non-empty).
3. `app.loadAndValidateConfig`, right after `ValidateAuthorities`: for each provider in use
   whose registered client implements `VersionVerifier`, call `CLIVersion`. Collect results
   into a `map[string]domain.ProviderCLI` returned alongside the config (or stored on the
   session). Not verified and no override → `apperr.NewDependencyError` with the text in
   `SPEC.md` §7.2. Exec failure → dependency error regardless of the override.
4. Record: fresh run → `run.ProviderCLI`; resume → `Resume.ProviderCLI` of the new entry.
   Header line when any entry is unverified (override in use): red warning with the version
   and the range.
5. `validate` prints `claude 2.1.289 (verified range >=2.1.283 <2.2.0)` or the refusal.
6. Fake CLI answers `--version` before any other processing (`[ "$1" = "--version" ]`).
   Integration helper leaves `FAKE_CLI_VERSION` unset (in range).
7. `persistence`: validate `provider_cli` entries (known provider, version printable ≤ 64
   bytes) on load.

## Acceptance

- Parse/range table: `2.1.282` ✗, `2.1.283` ✓, `2.1.999` ✓, `2.2.0` ✗, `3.0.0` ✗,
  `2.1.290-beta` ✗ (unparseable), `""` ✗, `garbage` ✗, `2.1.289 (Claude Code)` ✓.
- Integration: `FAKE_CLI_VERSION=2.2.0` → exit 2 before any provider call (invocation log
  empty), message names the range and the override; same with
  `security.allow_unverified_cli: true` → run proceeds, `run.json` has
  `provider_cli.claude.verified: false`, header warns.
- A config using claude only for `translation` still triggers the check.
- Resume records the version it saw in its `resumes[]` entry.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-05 in a parallel session (commit a44462d, together with H01); checked against
this task's acceptance in H10. `internal/provider/claude/version.go` holds the range
(`verifiedMin` 2.1.283, `verifiedMaxExclusive` 2.2.0) and `CLIVersion`; `provider` declares
the optional `VersionVerifier`; `config.ProvidersInUse` lists every routed provider. The
check runs in the fail-fast chain after `ValidateAuthorities`; `security.allow_unverified_cli`
turns the refusal into a warning; `run.json: provider_cli` and `resumes[].provider_cli`
record what was seen, validated on load. Scenario 24 covers the refusal before any call, the
override, the recorded version, and a resume that records its own.
