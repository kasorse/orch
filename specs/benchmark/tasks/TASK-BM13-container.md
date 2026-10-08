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

## As built (2026-10-08)

The files were written by an orchestrator run with no shell; the build, the acceptance checks
and the narrowing of the `docker run` options it left to the operator were done by hand the
same day (Ubuntu 24.04.5, Docker 29.8.1, `kernel.apparmor_restrict_unprivileged_userns=1`),
which closed the task.

- **`bench/docker/Dockerfile`**, two stages. Build: `golang:1.26-bookworm` compiles
  `orchestrator` (root module) and `orchbench` (`bench/` module). Runtime: `node:22-bookworm-slim`
  with git, bubblewrap, Python 3, the Go toolchain copied from the build stage, and the claude CLI
  from npm (`@anthropic-ai/claude-code`) pinned by `ARG CLAUDE_VERSION=2.1.287`; the build fails
  if `claude --version` is outside `>=2.1.283, <2.2.0`, and `DISABLE_AUTOUPDATER=1` keeps it
  there. The image runs as uid 10001 by default, never root, with `HOME=/home/bench`,
  `GOPATH` under the cache, and `bench/configs` and the B config rendered for the container
  (`bench/docker/B.container.yaml`, installed as `/opt/orche/rendered/B.yaml`) in `/opt/orche`,
  the working directory. That config is not the tests' `B.golden.yaml`: its binds
  (`~/go/pkg/mod`, `/opt/my tools`, …) do not exist in the image, and `orchestrator validate`
  stats every test bind before the sandbox probe. Its `test:` runs `go version && python3
  --version && node --version` with `ro_binds: [/opt/orche/sandbox/ro]` and
  `rw_binds: [/opt/orche/sandbox/rw]`, two directories the image creates (the rw one mode 0777)
  outside the cache volume, which an already existing volume would not fill from the image.
- **`bench/docker/run.sh`**: `run.sh build` builds from `git archive HEAD` (committed content
  only) and records the commit as the image label and in `/etc/orchbench-commit`, since
  `orchestrator version` names no commit yet. `run.sh <tasks> <results> [command...]` runs a
  fresh `--rm` container with `ANTHROPIC_API_KEY` by name, the tasks read-only at `/tasks`, the
  results at `/results`, the named volume `orchbench-cache` at `/home/bench/.cache` (so
  orchbench's default cache dir is on it), and `--user` set to the caller's uid and gid (refused
  for root). It refuses a tasks or results directory that is the home directory or contains it
  (`/`, `/home`, …), and creates the results directory only after that check; a subdirectory
  such as `~/tasks` is fine. No `$HOME`, SSH agent or Docker socket is passed.
- **The `docker run` options, narrowed by trying each one out**: `--cap-drop ALL`,
  `--security-opt seccomp=bench/docker/seccomp-bwrap.json`, `--security-opt apparmor=unconfined`,
  `--security-opt systempaths=unconfined`; no `--cap-add`, no `--privileged`. Without any one of
  them the sandbox probe of `orchestrator validate` fails. `seccomp-bwrap.json` is Docker's default
  profile (moby/profiles `seccomp/default.json` at `6fe7deb`) plus one rule allowing `clone`,
  `unshare`, `mount`, `umount2` and `pivot_root`, which the default allows only with
  `CAP_SYS_ADMIN`; dropping any of the five fails bwrap (`unshare user ns`, `No permissions to
  create new namespace`, `pivot_root`, `unmount old root`). AppArmor stays unconfined:
  docker-default denies `mount`, and a narrower profile would have to be loaded into the host's
  kernel as root.
- **No passwd entry is needed.** As uid 12345 (no entry in `/etc/passwd`) `git commit`, `claude
  --version`, `claude -p` (up to the API error of a dummy key, the same as under a known uid),
  `orchbench` and `orchestrator validate` all work. Only Node's `os.userInfo()` throws, and
  nothing on these paths calls it.

### Acceptance, run 2026-10-08

1. `run.sh build` succeeds from the committed tree; the image's claude is 2.1.287.
2. `run.sh <tasks> <results> orchbench validate --tasks '/tasks/*.yaml'` on a public task
   written for the check (google/uuid, `Compare`, base `53dda83`, fix `e8d82d3`, oracle
   `uuid_test.go`): `1 of 1 valid`, oracle red→green 3/3. The task was not added to the task
   set; that is BM15.
3. `run.sh … orchestrator --config /opt/orche/rendered/B.yaml validate`: configuration valid,
   sandbox probe included, claude 2.1.287 in range.
4. `run.sh … env` shows `ANTHROPIC_API_KEY` and the image's own `HOME`, `PATH`, `GOPATH`,
   `DISABLE_AUTOUPDATER`, `NODE_VERSION`, `YARN_VERSION`, Docker's `HOSTNAME` and the shell's
   `PWD`; nothing from the host.
5. A marker file put in the host's `~` is not readable from the container (`No such file or
   directory`; `/home` holds only `bench` and the image's `node`). This was checked with `cat`
   in a container process, the same place config A's Bash tool runs, not with a paid model call.
   `/tasks` is read-only, `/results` writable and owned by the caller, and there is no
   `/var/run/docker.sock`, `~/.ssh` or `SSH_AUTH_SOCK`.
6. The `docker run` options are the narrowed set above.
7. `run.sh` is committed executable; `scripts/check.sh` is green.
8. Arbitrary uid: see "No passwd entry is needed".
