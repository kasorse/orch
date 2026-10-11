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

## As built (2026-10-11)

Done as planned (commit b292455, 2026-10-10), except:

- **Gates:** written without being able to run anything; the four gates (`scripts/check.sh`, root
  and `bench/`) passed on 2026-10-11, outside the sandbox.
- **Fewer files.** The pre-run test and the `test` step share one path (`action/testaction.go`),
  which since QL07 runs `testreport.Parse` on every test result, so `run.json: pre_run_test`
  already carried `failed_tests` and `report_format`. No change was needed in `internal/app`,
  `domain/run.go` (`PreRunTest` is a `*TestResult`) or `persistence` (QL07 validates the
  fields). On resume the names come back through `Restore` → `applyPreRunTest`, as before.
- **Split:** `domain.SplitFailedTests(pre, cur)` (in `artifact.go`, so `action` and `report`
  share it) gives old, new and `ok`; `ok` is false unless both sides have names in the same,
  non-empty format. The implement request gets the red pre-run result as a new `PreRunTest`
  field, kept after the first test step (the pre-run output itself is shown only until then);
  `writeFailureSplit` in `prompts.go` prints the two lines (each capped at 30 names) instead of
  the QL07 line, or the QL07 line alone when there is no split.
- **Tests:** prompt tests in `action/prompts_failingtests_test.go` (`TestPrompts_OldAndNewFailures`,
  `TestPrompts_NoSplit`, `TestWriteFailureSplit_CapsAndOneLine`) assert the text in place of a
  golden file; `TestImplementRetry_SplitsOldAndNewFailures` covers the dispatcher and a pre-run
  record without names; `workflow.TestOutcomeOf_OnlyOldFailuresStillFail` holds that a failure
  made only of old tests is still a failure. The run-level check is integration scenario 44
  (QL13).
- **summary.md:** the "N failing before the run, M new" text is in `report/summary.go`, shown
  only when the split applies.
