# R03 — `Dispatcher.Restore` from artifacts; implement's artifact after the guard

**Size** M (1 d) · **Depends on** R01 · **Blocks** R06

## Goal

A resumed Dispatcher has the same prompt context the original process had after its last
completed step. The orchestrator writes nothing to the run directory while a guarded call
runs (`SPEC.md` §7.3, §9.3 G13).

## Files

- `internal/action/restore.go` (new) — `ArtifactReader`, `Restore`
- `internal/action/implement.go` — move `saveArtifact` after `Guard.Changed`
- `internal/persistence/filesystem.go` — `ReadArtifact(runID, index, action) ([]byte, error)`
- `internal/action/restore_test.go`

## Steps

1. `ArtifactReader` interface: `ReadArtifact(runID string, index int, action
   domain.ActionType) ([]byte, error)`. `Store` implements it from the **derived** path.
   A missing file returns a distinguishable error.
2. `(*Dispatcher).Restore(run, r) error` replays each completed execution in order, using
   the table in `SPEC.md` §7.3. Failed executions are skipped. Decoding is strict
   (`DisallowUnknownFields`). Review goes through `ReviewResult.Validate`. The test result
   comes from `exec.TestResult` in `run.json`, and must equal the artifact if both exist.
   Missing or undecodable → `RunStateError{corrupt}` naming the file.
3. Factor the state changes out of each `execute*` into small helpers
   (`d.applyImplement(a)`, `d.applyTest(r)`, …) that both the live path and `Restore`
   call, so the two cannot drift.
4. `executeImplement`: call `saveArtifact` after `Guard.Changed` returns, on both the
   violation path and the success path. The returned `StepOutcome` is unchanged. Leave a
   comment on why: nothing may be written to the run directory while the guard is open.

## Acceptance

- For each action, live execution against a fake provider and `Restore` from the files
  that execution wrote give identical Dispatcher fields (a table test shared by both).
- `Restore` over a history with a failed implement leaves `pendingTestFailure` as the
  preceding test failure.
- Missing artifact, extra field, a review with an unknown severity → `corrupt`.
- `dispatcher_test.go` and the integration scenarios pass unchanged.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
