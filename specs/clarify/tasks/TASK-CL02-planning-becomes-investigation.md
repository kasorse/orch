---
id: CL02
repo: orche
depends_on: [CL01]
jira: none
---
# CL02 — planning becomes investigation, with root_cause

**Size** M

## Goal

The one stage that reads the code is named for what it does (Approach §1, §5): it plans a code
change or, without development, answers. Its artifact gains the optional `root_cause` analysis
used to give.

## Files

- `internal/domain`: `StageInvestigation = "investigation"`, `ActionInvestigate = "investigate"`,
  the `--stage` alias `investigate`; `PlanArtifact` becomes `InvestigationArtifact` with
  `RootCause string` (`json:"root_cause,omitempty"`); `AuthorityFor(ActionInvestigate)` is
  `AuthorityRead`; `ParseStageFlag` refuses `planning` and `plan` with a message naming
  `specs/clarify`.
- `internal/action/plan.go` becomes `investigate.go`; `BuildPlanPrompt` becomes
  `BuildInvestigatePrompt`, which asks for `root_cause` and keeps the acceptance criteria rule;
  `lastPlan` becomes `lastInvestigation`; implement (the first call starts from
  `affected_files`) and review (the criteria) read it.
- `internal/policy`: the fallback enables `investigation` with the reason
  `forced: no stage cleared its threshold, defaulting to investigation`; `SkipPlanning` turns off
  research and investigation.
- `internal/config`: `actions.investigate` takes what `actions.plan` took, `by_complexity`
  included; `actions.plan`, and `planning` in thresholds, `force` (repository and org layer) and
  `explore`, are rejected with a message naming the replacement.
- `internal/persistence` and resume: `plan` in a workflow or `planning` in a decision is refused
  with the message of Approach §6.
- `internal/logging`, `internal/report`, the `summary.md` rendering: the new names.
- Golden files: `internal/action/testdata/prompts/plan_*.golden` become `investigate_*.golden`
  (`go test ./internal/action -update`); logging goldens.
- `bench/` (a separate module): the action → stage map in `bench/internal/runner/orchestrator.go`
  and the stage names in `bench/configs/*.yaml` and their goldens; run the four gates in `bench/`.
- `orchestrator.example.yaml`: the template names `investigation` (it is loaded by a test).

## Steps

1. A mechanical rename first (types, constants, files), green.
2. Then the config errors and the resume refusal.
3. Then `root_cause` in the prompt and the artifact.

## Acceptance

- A workflow without development whose last read-only step is `investigate` prints the investigation's `answer` at the end of the run.
- `actions.plan` in a config fails validation with a message naming `actions.investigate` and `specs/clarify`.
- `run --stage plan` and `run --stage planning` are refused; `run --stage investigate` runs the investigation stage.
- The investigate prompt golden asks for `root_cause` and keeps the acceptance criteria rule.
- `AuthorityFor(ActionInvestigate)` is `read`.
- Resuming a run whose `workflow.json` holds `plan` is refused with the Approach §6 message.
- No `ActionPlan`, `StagePlanning` or `PlanArtifact` is left in `internal/` or `bench/`.
- `scripts/check.sh` passes, including `bench/`.

## Notes

Do not edit anything under `specs/clarify/`.
