# S04 — `run --stage`: CLI, flow and console

**Size** M (1 d) · **Depends on** S01, S02 · **Blocks** S05, S06

## Goal

`orchestrator run --stage <name> [--stage <name> …] "<task>"` works end to end in the order of
`SPEC.md` §6, with no decision-layer object constructed.

## Files

- `internal/app/app.go`: `cliArgs`, `parseArgs`, `Run`, `runTask`; a new `internal/app/stages.go`
- `internal/logging`: `ManualStages` line + golden

## Steps

1. `cliArgs.stages []domain.Stage`. `--stage X` and `--stage=X` through the existing value
   helper, any position, repeatable. A parse error from `ParseStageFlag` is returned from
   `parseArgs` (before config loading). A comma in a value is an error (§4).
2. `Run`: `--stage` with any command other than `run` → `ConfigError`: `--stage is only valid
   with run`, before anything else (as `--jira`). Update both usage strings.
3. `runTask` takes the stages. The fail-fast chain, `repo.IsDirty` and the Jira fetch are
   unchanged and run first.
4. After `store.NewRun` / `Lock` / `SaveTicket` / `RunHeader`, split on `len(stages) > 0`:
   - manual: `dec, err := domain.ManualDecision(stages)`. Do not call `newDecisionProvider`,
     `registry.Resolve(translation)`, build `classification.Service`, or `Classify`. Build a
     `decisionOutcome` holding the decision, the trace (S05) and no project context.
   - otherwise: the existing code, moved unchanged into a function returning the same
     `decisionOutcome`.
   Everything from `workflow.Build` on (persist, `checkWorktree`, `Decided`, `newSession`,
   `executeWorkflow`) is shared and not duplicated.
5. Console: instead of `Classifying task…` and the decision box print `Stages (manual): testing
   → review` once, then `workflow.Gaps` lines to stderr, then the existing authority line and
   mode prompt. `--verbose` changes nothing.
6. `engineContext`: no classification time was spent, so `started` still covers the workflow
   only; `res.Timings.ManualChoice` is 0.

## Acceptance

- `parseArgs` table: `--stage x`, `--stage=x`, repeated, any position, alias, duplicate,
  unknown name, missing value, comma value, `--stage` with `validate`/`resume`/`version`
  (usage error, nothing printed), `--stage` with `--jira`.
- A manual `run` constructs no decision provider and no translator (asserted in S06 through the
  fakes' request counts).
- Without `--stage`, console output and run artifacts are byte-for-byte unchanged (existing
  goldens).
- `golden`: `Stages (manual): …`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03. As planned, with these differences:
- `parseArgs` collapses duplicates and parses each value as it reads it (`parseStageFlags` in
  `internal/app/stages.go`, which also rejects a comma).
- `runTask` splits through a `decisionOutcome` (decision, trace builder, project context, manual
  choice time, an `announce` func). `decideWithProvider` is the old code moved unchanged.
- `ManualStages` takes the enabled stages, so the line shows stage names
  (`testing → review`), not action names. Gap warnings print after `checkWorktree`, to stderr.
