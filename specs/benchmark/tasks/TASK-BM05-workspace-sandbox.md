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
