# T09 — Optional: run `test.command` under bwrap

**Phase** 3 (optional) · **Size** M · **Depends on** T04 · **Blocks** —

## Goal

Close F5 — agent-written code executed by `test` with full authority — for operators who
opt in (`SPEC.md` §8, D10). Default behavior is unchanged.

## Prerequisite on this machine

Ubuntu ≥ 24.04 blocks bwrap's user namespaces (observed: `bwrap: setting up uid map:
Permission denied`). Needs an AppArmor profile for `/usr/bin/bwrap` with `userns,` — see
Claude Code's sandboxing docs, "Ubuntu 24.04 and later". Do this before starting.

## Files

- `internal/config` — `test.sandbox`, `test.ro_binds`, `test.rw_binds` (+ validation)
- `internal/config/dependencies.go` — bwrap presence + probe when `sandbox: bwrap`
- `internal/testexec/runner.go` (+ test) — argv construction
- MVP.md §30 amendment, §18; README "What is not protected" update

## Steps

1. Config: `sandbox` ∈ {`none`, `bwrap`} (default `none`); binds are absolute or `~/`
   paths, expanded at load; each must exist.
2. `ValidateDependencies`: `bwrap` on PATH, and the probe
   `bwrap --ro-bind / / --unshare-net --unshare-user true` exits 0; on failure the message
   names the AppArmor remedy. Still before any AI call.
3. `testexec`: `bwrap --ro-bind / / --tmpfs $HOME --bind <repo> <repo>` then ro/rw binds,
   `--dev /dev --proc /proc --unshare-net --unshare-pid --die-with-parent --chdir <repo>
   sh -c <command>`, `Env: ScrubbedEnv()`. Keep the argv order from `SPEC.md` §8 and pin it
   in a test — `--tmpfs $HOME` after the repo bind would hide a repo under `$HOME`.
4. `TestResult` records whether it ran sandboxed; the `authority:` line shows
   `test runs (sandboxed): go test ./...`.

## As built

- `internal/testexec/sandbox.go`: `Sandbox`, the argv, `Probe`/`ProbeArgs`. Additions over
  the plan: `--tmpfs /tmp` and `TMPDIR=/tmp` (with `/` read-only `go test` could not build);
  the git directories re-bound read-only over the repository; `--unshare-ipc/-uts` and
  `--new-session`.
- The probe runs from `app` right after `ValidateDependencies` (bwrap on PATH and binds
  existing are checked there), in `run` and `validate`.
- New: the implement guard also runs around the test step, sandboxed or not; and
  `RepoGuard.Changed` compares the `.git` fingerprint before any `git status`, because git
  status runs a planted `core.fsmonitor` (verified). A `.git` violation message now warns not
  to run git until `.git/config` and the hooks are inspected.
- `TestResult.Sandboxed`; the authority line shows `test runs (sandboxed): ...`.
- Tests: full-argv unit test, env, probe; config validation and dependency checks; scenario
  16 through a fake bwrap (argv, authority line, run.json; unusable sandbox → exit 2 with no
  AI call and no Jev request); live `TestTestSandbox` with an unsandboxed positive control.
- Verified live after installing the AppArmor profile: `TestTestSandbox` passes, and this
  repository's whole `go test` suite passes inside the sandbox with the module cache
  read-only and the build cache writable.

## Acceptance

- Unit: argv full-slice test; `sandbox: none` produces today's `sh -c` spec exactly.
- Live (`-tags security`): a test command that writes `$HOME/marker` and `curl`s a URL fails
  to do either under `bwrap`, while `go test ./...` of this repository passes with
  `ro_binds: [~/go/pkg/mod]`, `rw_binds: [~/.cache/go-build]`.
- MVP.md §30 states the exception.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
