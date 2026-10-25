# CL02 — `planning`/`plan` → `investigation`/`investigate`

**Size** M (2 d) · **Depends on** CL01 · **Blocks** CL06, CL07

## Goal

The one stage that reads the code is named for what it does (`SPEC.md` §3, §8.2): it plans a code
change, or, without development, answers. Its artifact gains the optional `root_cause` that
analysis used to give.

## Files

- `internal/domain`: `StageInvestigation = "investigation"`, `ActionInvestigate = "investigate"`,
  the alias `investigate`; `PlanArtifact` → `InvestigationArtifact` with `RootCause string
  json:"root_cause,omitempty"`; `AuthorityFor(ActionInvestigate) == AuthorityRead`;
  `ParseStageFlag("planning"|"plan")` refused with a message naming `specs/clarify`
- `internal/action/plan.go` → `investigate.go`; `BuildPlanPrompt` → `BuildInvestigatePrompt`, the
  prompt asks for `root_cause` and keeps the acceptance criteria rule; `lastPlan` →
  `lastInvestigation`; implement (first call starts from `affected_files`) and review (criteria)
  read it
- `internal/policy`: the fallback names `investigation`; `SkipPlanning` turns off research and
  investigation
- `internal/config`: `actions.investigate` (with `by_complexity`, `escalate` is implement's only);
  `actions.plan`, `planning` in thresholds/force/explore rejected with a message (D9)
- `internal/persistence`, resume: `plan` in a workflow or `planning` in a decision refused
  (`SPEC.md` §10)
- `internal/logging`, `internal/report`, `summary.md` rendering: the new names
- golden files: `internal/action/testdata/prompts/plan_*.golden` → `investigate_*.golden`
  (`go test ./internal/action -update`), logging goldens

## Steps

1. A mechanical rename first (types, constants, files), green; then the config errors and the
   resume refusal; then `root_cause` in the prompt and the artifact.
2. `bench/` (a separate module) maps action names to stages in
   `bench/internal/runner/orchestrator.go` and names stages in `bench/configs/*.yaml`: update
   both and run the four gates in `bench/`.

## Acceptance

- Unit: a workflow without development whose last read-only step is `investigate` prints its
  `answer` at the end.
- Unit: `actions.plan` in a config fails with a message naming `actions.investigate`.
- Golden: the investigate prompt asks for `root_cause` and has no analysis block.
- `bench/`: `go test ./...` passes with the new names.

## Gates

`scripts/check.sh` (root and `bench/`)
