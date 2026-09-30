# R04 — Run-start baseline, `head_ref`, resume inputs under the guard

**Size** M–L (1–1.5 d) · **Depends on** R01 · **Blocks** R06

## Goal

A resume can prove that the protected state is what it was when the run started. Nothing on
the resume path runs `git status` before the git fingerprint matches. A model cannot change
what a later resume reads back without the guard stopping the step (`SPEC.md` §9.1–§9.3).

## Files

- `internal/action/guard.go` — `GuardState` JSON, `CheckBaseline`, run-directory patterns
- `internal/app/app.go` — record `head_ref` + `resume_baseline`; two guards
- `internal/action/guard_test.go`, `internal/app` tests where present

## Steps

1. `GuardState` implements `json.Marshaler`/`Unmarshaler`
   (`{"git_fingerprint": …, "protected": {path: hash}}`). The fields stay unexported.
2. `RunDirPatterns(runsRel string) []string` returns `<runsRel>/*/run.json`,
   `<runsRel>/*/workflow.json`, `<runsRel>/*/decision.json` and
   `<runsRel>/*/artifacts/**`. `runsRel` is the runs directory relative to the
   repository top level (the orchestrator may be started in a subdirectory). Logs are not
   included. If the runs directory is outside the worktree, return nil.
3. `app`: build **two** guards from the same repository.
   - The *baseline guard*: default patterns + `security.protected_paths` + the config
     file. It is used to record `resume_baseline` and to check it on resume.
   - The *step guard*: the same patterns plus `RunDirPatterns`. This is
     `dispatcher.Guard`.
   The baseline must exclude the run directory, because `run.json` changes with every step.
4. Record at the point `recordRestorePoint` runs (after approval, before the first step):
   `head_ref` (`HeadRef`) and `resume_baseline` (baseline guard `Snapshot`). If the
   snapshot fails, the run is refused before any step: without a baseline it could never be
   resumed safely, and the fingerprint read failing is a real problem. This differs from
   the restore point, whose failure is only reported.
5. `CheckBaseline(ctx, guard, raw json.RawMessage) ([]string, error)` unmarshals and calls
   `Changed`. `Changed` already compares the fingerprint before calling `git status`, and
   this is the property the resume path relies on. Add a test that asserts, through a
   recording executor, that no `status` argv is issued when the fingerprint differs.
6. The violation message: reuse `protectedViolation` with a heading parameter
   ("changed since the run started" for resume, the step name at run time).

## Acceptance

- `GuardState` round-trips through JSON, and `Changed(roundTripped)` is empty on an
  unchanged tree.
- A fresh run's `run.json` has `head_ref` and `resume_baseline` whenever `pre_run_ref` is
  set.
- A fake implement that writes `.orchestrator/runs/<id>/run.json` or an artifact of
  another run fails the step. Writing `.orchestrator/runs/<id>/logs/x` does not.
- `CheckBaseline` after `.git/config` changes → `.git (config or hooks)`, and no
  `git status` ran.
- Scenario 15 (worktree guard) passes unchanged.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
