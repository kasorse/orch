# BM05 — Mirrors, workspace without the future, sandbox

**Size** M (1.5 d) · **TODO** B2 · **Depends on** BM04 · **Blocks** BM07, BM08, BM10

## Goal

A fresh workspace for every check and every cell that holds `base_commit` and nothing after
it (`SPEC.md` §7.2), and one way to run trusted-but-model-touched commands under bwrap
(§7.4).

## Files

- `bench/internal/workspace` + test: `Mirror(repo) (path, error)`, `Create(task, at
  commit) (*Workspace, error)`, `(*Workspace).Diff()` (excluding `.orchestrator`),
  `Status()`, `Remove()`
- `bench/internal/sandbox` + test: `Run(ctx, Spec{Dir, Command, Binds, Network bool,
  Timeout}) (exitCode, stdout, stderr, error)`

## Steps

1. Mirror: `git clone --mirror` into `<cache-dir>/mirrors/<sha256(repo)>.git` once, `git
   fetch` when a commit is missing. A local path as `repo` is mirrored the same way.
2. Workspace under `<cache-dir>/work/<random>/`: `git init`; in the mirror, a temporary ref
   `refs/orchbench/<random>` → `base_commit`; `git fetch <mirror> <ref>`; delete the ref;
   `git checkout --detach FETCH_HEAD`. No remote is configured. Git identity for the
   workspace set locally (the orchestrator's snapshot commit needs one).
3. `Create(task, fix_commit)` is the same for validation's reference tree.
4. Sandbox: `sh -c <command>` under bwrap — root read-only, workspace writable, `$HOME`
   replaced by an empty tmpfs, `/tmp` tmpfs, the task's `ro_binds` / `rw_binds`, network
   unshared unless `Network: true` (setup only), environment: an allowlist (`PATH`, locale,
   `TERM`, `TZ`, toolchain variables) — the same policy as the core's `testexec.Sandbox` and
   `execution.TestEnv`, written again here (no import) and kept small. Process group killed
   on timeout.
5. Setup runs with `Network: true` but still under bwrap (its `$HOME` hidden): the trusted
   command is not the model's, but nothing it needs lives in the developer's home.

## Acceptance

- In a workspace: `git cat-file -e <fix_commit>` fails, `git log --all` shows nothing after
  `base_commit`, `git remote` is empty.
- Sandbox: no network (a connect fails), `$HOME` empty, a write outside the workspace and
  the rw binds fails, exit code and timeout reported. Skipped with a clear message where bwrap
  cannot run (the tests probe once).
- `Diff()` ignores `.orchestrator/`.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-06. Differences from the plan, and why:

- **`workspace.Manager`** (`New(cache.Dir, verbose)`) holds the cache directory and the `-v`
  writer; `Mirror`, `EnsureCommits` and `Create` are its methods and take a `ctx`.
  `cache.Dir` derives every path under `--cache-dir` (`Mirrors`, `Work`, `Logs`,
  `ValidatedFile`); `cmd/orchbench` takes its default from `cache.DefaultDir`.
- **A local `repo` is keyed by its absolute path**, so the same repository named two ways is
  mirrored once. A mirror is cloned into a temporary directory and renamed; a per-path mutex
  stops parallel tasks cloning or fetching it twice (one process; two `orchbench` processes on
  one cache directory are not serialised). A commit still missing after the fetch is a
  `workspace.ErrMissingCommit`, which `validate` turns into its hint. The fetch does not
  prune: that would delete the temporary refs of workspaces being created in parallel.
- **`Tree` and `ReadBlobs`** read a commit's files from the mirror (`ls-tree`, one
  `cat-file --batch`), for BM07: what the final check writes never comes from the
  workspace's `.git`, which the model may have changed.
- **Git in a workspace** runs with `core.fsmonitor=false` and `core.hooksPath=/dev/null`.
  `Diff` builds its patch in a temporary index, so the model's staged state stays as it is.
- **The sandbox re-binds the workspace's `.git` read-only**, as the core's
  `testexec.Sandbox` does: code run by setup, the oracle or the suite cannot plant a hook or
  an fsmonitor command for orchbench's next git call. The argv order is load-bearing and has
  a unit test. `Run` returns `(exit, stdout, stderr, err)`. A timeout is exit -1 with
  `ErrTimeout`, and a cancelled ctx is -1 with `ctx.Err()`; either way the whole process group
  is killed. `Spec.Stdout`/`Stderr` are optional tees for the log files and `-v`. `ForTask`
  builds the Spec from a task's binds. `Probe` returns a `*ProbeError` with the AppArmor hint.
- **Tests share `internal/testutil`**: throwaway repositories, a toolchain-free shell project
  (`SHProject`: base with a wrong `mul`, fix, a later commit) and `RequireBwrap`, which skips
  where bwrap cannot sandbox. The network test re-executes the test binary inside the sandbox
  to dial a host listener.
- Residual risk, accepted under Q4 (the benchmark runs in a disposable container): `Status`
  and `Diff` still read the workspace's `.git/config` and `.gitattributes`, so a clean or
  textconv filter the model configured there would run on the host. The orchestrator's guard
  fails such a step, and config A has a host shell anyway.
