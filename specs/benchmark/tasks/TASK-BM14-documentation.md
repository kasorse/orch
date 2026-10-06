# BM14 — Documentation

**Size** S (0.5 d) · **TODO** — · **Depends on** BM03, BM12 · **Blocks** BM16

## Goal

What was built is documented where users and future work look for it.

## Files

- `bench/README.md` (new): `SPEC.md` §13 as built — prerequisites, the three steps, each
  command with its flags, configs, results directory, recipes, the container
- `MVP.md`: §21 (persistence) — `executions[].usage`, `decision.json` `usage`,
  `run.json` `outcome`; the CLI section — `runs report [--dir]`, `decisions report` as its
  alias; §30 — a sentence that benchmarking lives in `bench/`, outside the core, and never
  selects models
- `CLAUDE.md`: run artifacts (`outcome`, usage), build & test (`cd bench && go test ./...`),
  the black-box rule for `bench/`
- `README.md`: `runs report`; a pointer to `bench/README.md`
- `TODO.md`: tick B3 (and B1/B2 when BM15/BM16 are done), link this spec
- `specs/benchmark/README.md`, `SPEC.md`: status

## Acceptance

- Every command and flag in `bench/README.md` exists in `orchbench --help`, and the reverse.
- The CLAUDE.md artifacts paragraph matches what a run writes (checked against an
  integration run directory).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty (root and `bench/`)
