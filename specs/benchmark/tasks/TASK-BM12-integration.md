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
