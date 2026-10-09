# QL09 — Old vs new failures in the retry prompt

**Size** S (0.5–1 d) · **TODO** Q7.3 · **Depends on** QL03, QL07 · **Blocks** QL13

## Goal

When a run started red (`--allow-red-tests`), the retry prompt says which failing tests were
already failing before the run and which are new. This is a hint only; pass/fail stays the
exit code (`SPEC.md` §8.3).

## Files

- `internal/app/app.go`: `preRunTest` keeps `failed_tests` in `run.json: pre_run_test`
- `internal/domain/run.go`: the pre-run record gains `FailedTests`
- `internal/action/restore.go`: the dispatcher keeps the pre-run names (also on resume)
- `internal/action/prompts.go`: the split in `BuildImplementPrompt`
- `internal/report`: `summary.md` shows "N failing before the run, M new"
- `internal/persistence/validate.go`

## Steps

1. Run `testreport.Parse` on the pre-run test's output, and persist the names (≤ 200) and the
   format.
2. In the implement retry prompt after a failed test: when both the pre-run record and the
   current result have names in the **same** format, print the two lines of `SPEC.md` §8.3
   (each capped at 30 names). Otherwise print only the QL07 line.
3. No change to `outcomeOf`, `Next` or the test step's success. Add a test that asserts a
   failure made only of "old" tests still fails the step.

## Acceptance

- Prompt golden for the split. A different format, or no names, gives no split.
- A run whose only failures are old ones still sends the run back to implement and still
  fails at the limit (integration in QL13).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
