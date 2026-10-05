# H05 — Sandbox on by default

**Size** S (0.5–1 d) · **TODO** S2 · **Depends on** H04 · **Blocks** H07

## Goal

`test.sandbox` absent means `bwrap`; `none` must be explicit and is announced on every run
(`SPEC.md` §6.3).

## Files

- `internal/config/config.go`, `validator.go`, `dependencies.go` (+ tests): `Sandboxed()`,
  binds validation, bwrap dependency check
- `internal/testexec/sandbox.go`: probe remedy text
- `internal/logging` (authority line) (+ golden)
- `orchestrator.example.yaml`
- `test/integration/helpers_test.go` and any scenario config: explicit `sandbox: none`
- `internal/testutil/fakeexec` if a scenario relies on the old default

## Steps

1. `TestConfig.Sandboxed()` → `t.Sandbox == "" || t.Sandbox == SandboxBwrap`. Add
   `SandboxNone = "none"`; validation accepts `""`, `bwrap`, `none` only. `ro_binds`/`rw_binds`
   with `none` stays an error.
2. Dependency check: bwrap required whenever `Sandboxed()`; the error adds
   `or set test.sandbox: none to run tests unsandboxed`. Same addition in `Probe`'s remedy.
3. Authority line: when not sandboxed,
   `test: <command> — NOT sandboxed: network, $HOME and your files are reachable` (styled as a
   warning in pretty mode). Golden files updated (`go test ./internal/logging -update`, then
   review the diff by hand).
4. Integration helper config writes `sandbox: none` explicitly (the CI host may lack bwrap);
   the sandbox scenario keeps `bwrap` and keeps its skip-if-unavailable logic.
5. `orchestrator.example.yaml`: `sandbox: bwrap` uncommented; binds stay commented with the
   Go example; a line about `none` and macOS.

## Acceptance

- Config without `sandbox` + `FakeDependencyChecker` without bwrap → dependency error naming
  both remedies; with `sandbox: none` → valid.
- Authority line golden for `none` shows the warning; for bwrap unchanged.
- All existing integration scenarios pass with the explicit `none`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
