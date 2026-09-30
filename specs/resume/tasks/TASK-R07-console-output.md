# R07 — Resume header, history, next step; golden files

**Size** S (0.5 d) · **Depends on** R06 · **Blocks** R08

## Goal

Before approving a resume, the operator sees what happened, what changed and what runs
next (`SPEC.md` §12).

## Files

- `internal/logging/progress.go` — `ResumeHeader`, `History`, `NextStep`, `WorkingTree`,
  `AlreadyCompleted`
- `internal/logging/testdata/` — golden files
- `internal/app/app.go` — calls

## Steps

1. `ResumeHeader(run, dir)`: the run ID and directory, and the previous attempt's status
   and reason. A `running` status is rendered as "interrupted".
2. `History(run)`: one line per execution: ✓/✗, index, action, provider/model, authority,
   and for failures the first line of the error. Test and review lines show pass/fail from
   the trusted result.
3. `WorkingTree(summary)`: the `StatusSummary` output, only when it is not empty. It is
   printed after the baseline check passed.
4. `NextStep(entry, why, remaining)`: the entry action, the reason from `workflow.Entry`,
   and the remaining workflow as it would run forward. Retries are not predicted.
5. The existing `Authority` and `RestorePoint` lines are reused unchanged.
6. `--verbose` changes nothing here.

## Acceptance

- Golden files for: an interrupted run, failed at implement; limit-stopped after a test
  failure, after the limit was raised; and a plan-mode run (no history, fresh-run output).
- `go test ./internal/logging -update` regenerates them. The review diff shows only the
  new files.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
