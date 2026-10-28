# BM18 — Contributed results: export, merge, images, guide

**Size** L (about 4 days, plus the contributors' time) · **TODO** B5 · **Depends on** BM17 ·
**Blocks** —

**Cancelled 2026-10-28** (TODO.md B5): no bundles from contributors. Tasks in other languages
are mined from commits of open GitHub repositories, and the owner runs every configuration.

## Goal

Colleagues and friends outside the company add results in Node.js, Go, C++ and PHP from
their own repositories, sending only an anonymised bundle (`SPEC.md` §17). Started only
after B4; split into smaller tasks (BM18a…) when it starts, if the scope proves larger.

## Files

- `bench/internal/export` + test; `orchbench export` subcommand
- `bench/internal/summary`: several inputs (directories and bundles), `--by
  language|contributor|category`, per-build columns, thresholds, contributor shares
- `bench/docker/`: `orchbench-base` and `-go`, `-node`, `-php`, `-cpp`, `-python` images
- `bench/CONTRIBUTING.md` (Russian and English): the kit, the steps of §17.2, what the bundle
  holds and what never leaves the machine, costs
- the kit for a round: a release tag, configs, CLI range, image tags

## Steps

1. `export` (§17.3): salt in `<cache-dir>/export-salt` (created once, 32 random bytes,
   never exported); task id → `HMAC-SHA256(salt, id)`; drop free text, `task_sha256`, logs,
   runs, configs; round `started_at` to the day; print to stdout.
2. Merge in `summarize` (§17.5): bundles and directories alike; pairing within contributor
   and task; a task without the baseline left out and counted; thresholds (10 tasks, 2
   contributors per language, configurable); builds side by side, never averaged;
   contributor shares.
3. Images (§17.4): language layers on the base; C++ with CMake and a compiler, PHP with
   Composer, Node with npm/pnpm/yarn; a two-line extension example in the guide.
4. Language defaults of §5.5 checked on one real task per language (node, php, cpp) by a
   contributor before the round.
5. First round: send the kit, collect bundles, `summarize --by language`.

## Acceptance

- Unit: an exported bundle contains no field outside §17.3's list (a test walks the JSON);
  the same task maps to the same hash across two exports with the same salt, to different
  hashes with different salts.
- Merge golden: two contributors, two builds, one language below the threshold.
- A contributor guide dry run by one friend before the round.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
