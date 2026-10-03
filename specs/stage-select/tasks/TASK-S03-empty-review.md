# S03 — Review fails on an empty diff

**Size** S (0.25 d) · **Depends on** — · **Blocks** S06

## Goal

`review` does not call the model when the working tree has no changes against `HEAD`
(`SPEC.md` §9, §14 D5). A review of nothing is a false pass.

## Files

- `internal/action/review.go` (+ test), `internal/apperr` only if no suitable error exists

## Steps

1. After `d.Repo.Diff`, if the diff is empty (whitespace only), return a `WorkflowError`:
   `nothing to review: the working tree has no changes against HEAD`. The provider is not
   called and no artifact is saved.
2. The step fails the run through the existing path (`review` is not retried: it is not a
   verdict).
3. Check existing integration scenarios: any whose `implement` makes no change and then
   reviews must now supply a change or expect the failure.

## Acceptance

- Unit test with the fake repo and a fake provider: empty diff → error, provider call count 0.
- Non-empty diff: behaviour unchanged.
- Existing integration scenarios still pass (fixed where step 3 requires).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03, with a follow-up found by S06. The check in `executeReview` was right but
never fired in a real run: `Repository.Diff` listed the run's own `.orchestrator/` directory as
an untracked file. `Diff` now takes optional excluded paths (git `:(exclude)` pathspecs, for
both `git diff` and `git status`) and `executeReview` passes `Dispatcher.RunsDir`. As a side
effect, decided runs' review prompts no longer carry that noise line.
