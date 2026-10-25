---
id: CL01
repo: orche
depends_on: []
jira: none
---
# CL01 — Analysis leaves the stages: two asked stages, fallback, config errors

**Size** M

## Goal

`analysis` is no longer a stage and `analyze` no longer an action (Approach §1, §5). Two stages are
asked: research and development. Nothing enabled falls back to `planning` here (CL02 renames it
to `investigation`). Every place that still names analysis is a config error or a refusal with a
message naming `specs/clarify` (D9).

## Files

- `internal/domain/stage.go`, `workflow.go`, `decision.go`, `authority.go`: drop `StageAnalysis`,
  `ActionAnalyze`, the `analyze` alias and `StageDecisions.Analysis`; `AskedStages()` returns
  research and development; `ParseStageFlag` refuses `analysis` and `analyze` with a message
  naming `specs/clarify`.
- `internal/domain/artifact.go`: drop `AnalysisArtifact`.
- `internal/decision/questions.go`: drop the `stage_analysis` question.
- `internal/decision/jev/mock.go`, `mapper.go`, `internal/decision/llm/prompt.go`, `parse.go`: two
  stage answers; `stage_analysis` is an unexpected answer.
- `internal/policy/engine.go`: the fallback enables `planning` with the reason
  `forced: no stage cleared its threshold, defaulting to planning`; `force.go` `SkipPlanning`
  without analysis.
- `internal/action/analyze.go`, its prompt builder, `lastAnalysis`, `applyAnalysis`: removed; the
  plan prompt loses its Analysis block.
- `internal/config`: `policy.stages.thresholds.analysis`, `analysis` in `policy.stages.force`
  (repository and org layer) and in `policy.explore`, and `actions.analyze` are declared in the
  schema and rejected with a message naming the replacement (Contracts: Configuration).
- `internal/persistence` and resume: a `workflow.json` with `analyze` or a decision with `analysis`
  is refused with the message of Approach §6.
- `internal/interaction` (the manual chooser's stage list), `internal/logging`, `internal/report`.
- Integration fixtures in `test/integration` answer two stages; scenarios that expected `analyze`
  now expect what the template gives.
- Golden files: `go test ./internal/decision/jev -update`, `./internal/action -update`,
  `./internal/logging -update`.

## Steps

1. Domain, then the questions and both providers, then the policy, then action, then config and
   persistence, then the fixtures and goldens.
2. `orchestrator.example.yaml` is loaded by a test: drop `analysis` from its thresholds and
   template in this task (the rest of its text is CL08's).

## Acceptance

- A config with `policy.stages.thresholds.analysis` fails validation with a message naming `policy.clarify.threshold` and `specs/clarify`.
- `actions.analyze` in a config fails validation with a message naming `specs/clarify`.
- `run --stage analyze` and `run --stage analysis` are refused with a message naming `specs/clarify`.
- `domain.AskedStages()` returns exactly research and development, and `decision.StageQuestions()` asks only those two.
- The Jev mapper and the LLM parser refuse a classification answer that contains `stage_analysis`.
- A decision with every asked stage below its threshold and no template enables `planning` with the fallback reason.
- Resuming a run whose `workflow.json` holds `analyze` is refused with the Approach §6 message.
- No `analyze` action, `AnalysisArtifact` or analysis prompt builder is left in `internal/`.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/clarify/`. `questions_hash` changes in this task on purpose.

## As built (2026-10-25)

Run `5010f31b-2288-4c1d-906f-3aef6e0331f8` in orche: stopped, 4 fix iterations, tests passed, review fail.
Branch `story/clarify/CL01` on `spec/clarify` @ cb98408, commit c0b2997.
Cost $3.55. Implementation summary: Remove the analysis stage and analyze action.
Conflicts with spec/clarify: none.
