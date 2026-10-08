#!/usr/bin/env bash
# The one way to start the benchmark's container (specs/benchmark/tasks/TASK-BM13-container.md).
#
#   bench/docker/run.sh build
#   ANTHROPIC_API_KEY=... bench/docker/run.sh <tasks-dir> <results-dir> [command [args...]]
#
# `build` builds the image from the committed tree (git archive HEAD, so uncommitted edits and
# untracked files never get in) and records the commit in it. The second form runs `command` in
# a fresh container (default `orchbench --help`), for example:
#
#   run.sh ~/tasks ~/results orchbench validate --tasks '/tasks/*.yaml'
#   run.sh ~/tasks ~/results orchbench run --tasks '/tasks/*.yaml' --out /results/pilot --max-cost 20
#   run.sh ~/tasks ~/results env
#
# What the container gets, and nothing else:
#   - ANTHROPIC_API_KEY, by name: the value is read from the environment of this script and
#     never appears on a command line;
#   - <tasks-dir> at /tasks, read-only;
#   - <results-dir> at /results, read-write;
#   - the named volume ORCHBENCH_CACHE_VOLUME (default orchbench-cache) at /home/bench/.cache:
#     orchbench's mirrors, workspaces and validated.json, and the Go module cache.
# Not the host's $HOME, SSH agent or Docker socket. The container runs as the caller's uid and
# gid (never root), so files in <results-dir> belong to the caller.
set -euo pipefail

image=${ORCHBENCH_IMAGE:-orchbench:latest}
cache_volume=${ORCHBENCH_CACHE_VOLUME:-orchbench-cache}
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

die() {
	echo "run.sh: $*" >&2
	exit 2
}

if [ "${1:-}" = "build" ]; then
	[ $# -eq 1 ] || die "build takes no arguments"
	commit=$(git -C "$repo" rev-parse HEAD)
	if [ -n "$(git -C "$repo" status --porcelain)" ]; then
		echo "run.sh: warning: the working tree has changes; the image is built from the commit $commit only" >&2
	fi
	git -C "$repo" archive HEAD |
		docker build -f bench/docker/Dockerfile --build-arg "GIT_COMMIT=$commit" -t "$image" -
	exit 0
fi

[ $# -ge 2 ] || die "usage: run.sh build | run.sh <tasks-dir> <results-dir> [command [args...]]"
[ -n "${ANTHROPIC_API_KEY:-}" ] || die "ANTHROPIC_API_KEY is not set: it is the only credential the container gets"
[ "$(id -u)" -ne 0 ] || die "refusing to run as root: the container runs as the caller's uid"

tasks=$1
results=$2
shift 2

[ -d "$tasks" ] || die "the tasks directory $tasks does not exist"
tasks=$(cd "$tasks" && pwd -P)
# The results directory is created only after the checks below, so it is resolved through its
# parent, which must exist.
if [ -d "$results" ]; then
	results=$(cd "$results" && pwd -P)
else
	parent=$(cd "$(dirname -- "$results")" 2>/dev/null && pwd -P) ||
		die "the parent of the results directory $results does not exist"
	results=${parent%/}/$(basename -- "$results")
fi

# --mount takes comma-separated fields, and the home directory must never be handed over: not
# itself and not a directory that contains it (/home, /). A subdirectory such as ~/tasks is fine:
# only that directory is mounted.
home=$(cd "$HOME" && pwd -P) || die "the home directory $HOME does not exist"
for path in "$tasks" "$results"; do
	case $path in
	*,*) die "$path: a comma in the path cannot be mounted" ;;
	/) die "refusing to mount /" ;;
	esac
	case $home/ in
	"$path"/*) die "$path: refusing to mount the home directory or a directory that contains it" ;;
	esac
done
mkdir -p -- "$results"

# docker run options bwrap needs inside the container. bwrap makes a user, pid and mount
# namespace, mounts a fresh /proc and pivots into a new root; Docker's defaults forbid each of
# them. Each option below was dropped in turn on Ubuntu 24.04 (Docker 29.8) and the sandbox
# probe of `orchestrator validate` failed without it (TASK-BM13, "As built"):
#   seccomp=seccomp-bwrap.json  Docker's default profile (moby/profiles) plus clone, unshare,
#                           mount, umount2 and pivot_root, which it allows only with
#                           CAP_SYS_ADMIN; each of the five is needed
#   apparmor=unconfined     docker-default denies mount; on Ubuntu 24.04+ it also meets the
#                           kernel's restriction of unprivileged user namespaces. A narrower
#                           profile would have to be loaded into the host's kernel as root
#   systempaths=unconfined  /proc is masked in the container, and the kernel refuses a new /proc
#                           mount where one is masked (bwrap --proc /proc)
#   --cap-drop ALL          bwrap gets its capabilities in its own user namespace; the container
#                           keeps none, and no --cap-add and no --privileged are used
sandbox_opts=(
	--cap-drop ALL
	--security-opt "seccomp=$repo/bench/docker/seccomp-bwrap.json"
	--security-opt apparmor=unconfined
	--security-opt systempaths=unconfined
)

tty=()
if [ -t 0 ] && [ -t 1 ]; then
	tty=(-it)
fi

if [ $# -eq 0 ]; then
	set -- orchbench --help
fi

exec docker run --rm --init ${tty[@]+"${tty[@]}"} \
	--user "$(id -u):$(id -g)" \
	"${sandbox_opts[@]}" \
	-e ANTHROPIC_API_KEY \
	--mount "type=bind,src=$tasks,dst=/tasks,readonly" \
	--mount "type=bind,src=$results,dst=/results" \
	--mount "type=volume,src=$cache_volume,dst=/home/bench/.cache" \
	"$image" "$@"
