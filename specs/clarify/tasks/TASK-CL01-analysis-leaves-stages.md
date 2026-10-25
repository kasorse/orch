# CL01 — Analysis leaves the stages

**Size** M (2–3 d) · **Depends on** — · **Blocks** CL02, CL07

## Goal

`analysis` is no longer a stage and `analyze` no longer an action (`SPEC.md` §3, §8.1). Two stages
are asked: research and development. Nothing enabled falls back to `planning` (CL02 renames it).
Every place that still names analysis is a config error or a refusal with a message (D9).

## Files

- `internal/domain/stage.go`, `workflow.go`, `decision.go`, `authority.go`: drop `StageAnalysis`,
  `ActionAnalyze`, the `analyze` alias, `StageDecisions.Analysis`; `AskedStages()` is
  `research, development`; `ParseStageFlag("analysis"|"analyze")` returns an error naming
  `specs/clarify`
- `internal/domain/artifact.go`: drop `AnalysisArtifact`
- `internal/decision/questions.go`: drop the `stage_analysis` question (`questions_hash` changes)
- `internal/decision/jev/mock.go`, `mapper.go`, `internal/decision/llm/prompt.go`, `parse.go`: two
  stage answers; `stage_analysis` is an `unexpected answer`
- `internal/policy/engine.go`: the fallback enables `planning`, reason
  `forced: no stage cleared its threshold, defaulting to planning`; `force.go` `SkipPlanning`
  without analysis
- `internal/action/analyze.go`, its prompt builder, `lastAnalysis`, `applyAnalysis`: removed; the
  plan prompt loses its `Analysis` block
- `internal/config`: `thresholds.analysis`, `analysis` in `force` (repository and org layer) and
  in `explore`, `actions.analyze` declared and rejected with the message of `SPEC.md` §9
- `internal/persistence`, resume: a workflow with `analyze` or a decision with `analysis` is
  refused with the message of `SPEC.md` §10
- `internal/interaction` (the manual chooser's stage list), `internal/logging`, `internal/report`
  (calibrate groups by `questions_hash`, so old decisions stay in their own group)
- golden files: `go test ./internal/decision/jev -update`, `./internal/action -update`,
  `./internal/logging -update`; read every diff

## Steps

1. Domain, then questions and providers, then policy, then action, then config and persistence.
2. Integration fixtures (`test/integration`) answer two stages; scenarios that expected
   `analyze` in the workflow now expect `plan` or nothing, by the template.
3. `orchestrator.example.yaml` and this repository's own config lose `analysis` in the same
   change, so `scripts/check.sh` keeps passing.

## Acceptance

- Unit: a config with `thresholds.analysis: 0.8` fails `config.Validate` with a message naming
  `policy.clarify.threshold` and `specs/clarify`; `run --stage analyze` is refused the same way.
- Unit: a decision with every asked stage below its threshold and no template enables `planning`.
- Unit: the Jev mapper and the LLM parser refuse an answer with `stage_analysis`.
- Resume of a run whose `workflow.json` holds `analyze` is refused with the §10 message.

## Gates

`scripts/check.sh`
