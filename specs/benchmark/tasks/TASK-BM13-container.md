# BM13 — Container image for running the benchmark

**Size** S (1 d) · **TODO** B2 · **Depends on** BM10 · **Blocks** BM16

## Goal

The benchmark runs in a disposable container (`SPEC.md` §7.4, Q4): config A's shell and
every model-written line of code stay away from the owner's machine and credentials.

## Files

- `bench/docker/Dockerfile` (base: git, bwrap, the claude CLI pinned inside the verified
  range, `orchestrator` and `orchbench` built from the checked-out commit, Go, Python,
  Node — the owner's languages)
- `bench/docker/run.sh`: the one way to start it

## Steps

1. Image runs as a non-root user; bwrap inside a container needs user namespaces — document
   the `docker run` options it requires (`--security-opt seccomp=…` / `--cap-add` as
   minimal as works) and verify the core's sandbox probe passes inside.
2. `run.sh` passes in only: the claude credentials (`ANTHROPIC_API_KEY`, Q5), the tasks
   directory read-only, the results directory read-write, the cache as a named volume. No
   `$HOME`, no SSH agent, no Docker socket.
3. Language images for contributors are B5 (BM18); this one serves the owner's run.

## Acceptance

- Inside the container: `orchbench validate` on a public task passes; `orchestrator
  validate` with a rendered config passes (sandbox probe included); `env` shows no
  variable beyond the passed ones.
- A test cell of config A cannot read a file placed in the host's home (not mounted).

## Gates

`docker build` succeeds; the acceptance commands above, recorded in "As built".
