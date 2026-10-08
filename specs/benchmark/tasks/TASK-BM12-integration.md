# BM12 — End-to-end test with fakes

**Size** M (1 d) · **TODO** B2 · **Depends on** BM10, BM11 · **Blocks** BM14

## Goal

The whole pipeline — validate, run every config, summarize — works without network, a real
model or a real CLI (`SPEC.md` §16).

## Files

- `bench/test/e2e_test.go` (new), fixtures under `bench/test/testdata/`
- fake `claude` and fake `orchestrator` executables (built by the test, like the core's
  `internal/testutil/fakeexec`, but in `bench/` — no import)

## Steps

1. Fixture repository created by the test: a small Go module with a base commit, a fix
   commit (code + test) and a question task.
2. Fake `claude`: reads stdin, applies a canned patch chosen by an environment variable
   (correct fix / no change / fix plus regression), prints a stream-json `result` event
   with a cost.
3. Fake `orchestrator`: parses `--config`, `--stage`, the task; applies the same canned
   patches; writes a run directory with `run.json` (with `outcome`) and `decision.json`.
4. Scenario: `validate` → ok; `run --configs A,B,C,D --reps 2` with patches chosen so that
   every result kind appears; interrupted and continued; `--max-cost` stop;
   `summarize` → golden `summary.md`.

## Acceptance

- The scenario passes in `cd bench && go test ./...` with no network (bwrap-dependent steps
  skip with a message where bwrap is unavailable, as in BM05).

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-08)

The test was written by an orchestrator run with no shell; the operator steps it left (the
golden file and the gates) were run by hand on the same day, which closed the task.

- `bench/test/e2e_test.go` (`TestPipeline`) builds the real `orchbench` with `go build` and
  drives it as subprocesses: `validate`; `run --max-cost 0.25` (exit 3, the first three cells);
  `run` interrupted with SIGINT while one cell sleeps (exit 130, no line for that cell, no
  partial line); `run --dry-run`; the same `run` again (exit 1, the two infra_error cells
  remain, the manifest is unchanged, 16 cells in 18 lines); `summarize --seed 1` against
  `testdata/summary.golden.md` (`-update` rewrites it). `testutil.RequireBwrap` comes first, so
  the whole scenario skips where bwrap cannot sandbox; `-short` skips it too.
- **One fake program, two names.** `testdata/fake` is a single stdlib-only program that plays
  `claude` or `orchestrator` by the name it is run as; the test builds it once and copies it.
  It is its own Go module (`module fake`), so `bench`'s `./...` and `boundary_test.go` skip it.
  The plan file (`$FAKE_PLAN`) says per `<task>/<config>` what it does: `fix`, `regression`,
  `nochange`, `answer-ok`, `answer-bad` or `infra`. The tasks carry a `[e2e:<id>]` marker so
  that the fake finds its row from the prompt.
- **The fixture is `testutil.NewSHProject`, not a Go module.** `go test` inside the sandbox
  needs a toolchain that `$HOME` being a tmpfs can hide, a build cache and a module cache; the
  shell project needs none, and BM05–BM08 test the same way. The canned fixes are two trees
  written by the test (`fix`, `regression`).
- **`wall_ms` and `started_at` are pinned** in `results.jsonl` (per config, by line) before
  `summarize`, since the only varying numbers of a result line are those two.
- **The golden `testdata/summary.golden.md`** was written by `go test ./test -run TestPipeline
  -update` and read: 8 pass, 6 fail, 2 infra_error, 2 replaced result lines; A passes both
  tasks, B fails on the oracle and the answer, C on a regression, D's bugfix cells are the two
  infra_error ones. A missing golden now fails the `summarize` step instead of skipping it, so
  losing the file cannot quietly turn the comparison off.
- Gates: `scripts/check.sh` green outside the sandbox (both modules, `TestPipeline` runs all
  five steps); `go vet` and `go build` of `testdata/fake` clean; `-short` skips with its message.
