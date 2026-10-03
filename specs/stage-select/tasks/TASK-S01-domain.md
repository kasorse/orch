# S01 — Domain: stage aliases, `manual` source, selection → decision

**Size** S (0.5 d) · **Depends on** — · **Blocks** S02, S04, S05

## Goal

`domain` can turn a list of operator-given stage names into a validated `Decision` with
`Source: manual`, and every loader accepts such a decision (`SPEC.md` §4.1, §5, §14 D1–D3).

## Files

- `internal/domain/stage.go` (+ test): `ParseStageFlag`
- `internal/domain/policy.go` (+ test): `DecisionSourceManual`, `ManualDecision`
- the places that validate a loaded `Decision` / `Complexity` (`persistence`, `workflow.Verify`
  callers, `logging/decision.go`, `pretty.go`)

## Steps

1. `ParseStageFlag(s string) (Stage, error)`: trim, lower-case, accept the six canonical names
   and the aliases `analyze`, `plan`, `implement`, `test`. `ParseStage` stays strict (it reads
   persisted data). The error lists the valid names and aliases (§4.1).
2. `DecisionSourceManual DecisionSource = "manual"`.
3. `ManualDecision(stages []Stage) (Decision, error)`: reject an empty list; collapse
   duplicates; return `Source: manual`, `ContextDepth{Selected: none}`, empty
   `Complexity.Selected`, and one `StageOutcome` per stage in `StageOrder` with `Enabled`, a
   `Reason` (`selected with --stage` / `not selected`), and no probability or threshold (not
   `0`). The caller's order never matters.
4. Relax every validator that demands a valid complexity or a stage probability so it accepts
   them for `manual` only; any other source still fails on a missing one.

## Acceptance

- Table test: every canonical name and alias, case and spaces, an unknown name (error text), a
  duplicate through both spellings.
- `ManualDecision`: order-independent; each single stage; all six; empty list errors;
  `workflow.Build` of the result gives the expected steps and `workflow.Verify` accepts it.
- A `manual` decision round-trips through JSON; a `jev` decision with empty complexity is still
  rejected.
- `internal/domain` still imports nothing but the standard library.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-03. As planned, with one difference: step 4 (relax the validators) needed no
change, because nothing that loads a `Decision` or `run.json` checks complexity or stage
probability (`persistence.validateRun` does not look at the decision). `ManualDecision` also
rejects an unsupported stage, not only an empty list. `ParseStageFlag` is in `domain/stage.go`,
`ManualDecision` and `DecisionSourceManual` in `domain/policy.go`. The build-and-`Verify` test
lives in `internal/workflow/builder_test.go`, since `domain` cannot import `workflow`.
