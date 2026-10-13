# DS03 — Three stage questions

**Size** M (2–3 d) · **Depends on** — · **Blocks** DS04

## Goal

Ask only `stage_development`, `stage_research` and `stage_analysis`, with their current texts
(`SPEC.md` §6.1). Planning, testing and review are no longer asked, by any provider.

## Files

- `internal/decision/questions.go`: `StageQuestions()` returns the three, in `StageOrder`
- `internal/domain/decision.go`: `StageDecisions.Planning`, `.Testing`, `.Review` become
  optional (pointers, `omitempty`); `Validate` requires the three asked ones; `All()` yields
  only the stages that carry a probability
- `internal/decision/jev/mapper.go`: `checkEnvelope` expects the three; an answer for
  `stage_planning` is an `unexpected answer`
- `internal/decision/jev/mock.go`: drop the three heuristics
- `internal/decision/llm/prompt.go`, `parse.go`: the same three
- golden files: `go test ./internal/decision/jev -update`, `./internal/logging -update`; read
  the diff before committing

## Steps

1. Domain first, then the question list, then each provider and its tests.
2. Until DS04 lands, `policy.SelectStages` treats a missing probability as "not enabled unless
   forced"; nothing else changes in this task.

## Acceptance

- Unit: the Jev mapper refuses an extra `stage_review` answer and a missing
  `stage_analysis`; the LLM parser the same.
- A decision.json written before this task still loads with six probabilities.

## Gates

`scripts/check.sh`

## As built (2026-10-13)

Built together with DS04 and committed with it: on its own, DS03 would stop asking about
testing, and without DS04's "testing with development" a run would implement and never test.
`scripts/check.sh` (root and `bench/`) passed on 2026-10-13, run outside the bwrap sandbox,
after rebuilding `bin/orchestrator`.

- **The asked stages live in `domain`.** `domain.AskedStages()` (research, analysis,
  development) is the one list: `decision.StageQuestions`, `StageDecisions.Validate` and the
  config's thresholds all read it.
- **`StageDecisions` holds pointers for all six stages**, not only for the three dropped ones:
  one shape for every field, `Get` reports a stage that was not answered, `All` yields only the
  answered ones. `Validate` requires the asked three and refuses the others.
- **The mappers needed no change.** Jev's `checkEnvelope` and the LLM's `exactKeys` already take
  their list from `StageQuestions()`, so an answer to `stage_review` is an `unexpected answer`
  (tests added for both).
- **The Jev mock** lost its planning, testing and review heuristics and the test-word signal
  only they read.
- **Golden files:** `go test ./internal/decision/jev -update` removed the three questions from
  both request goldens and nothing else; `internal/logging` goldens did not change.
- **Integration fixtures** answer three stages; `scriptBugfixLow` is now a simple task that
  runs implement and test, so three scenarios expect `[implement]` instead of
  `[implement review]`, `scriptWithPlan` is a complex task, and scenario 45 shows the base model
  on research instead of review.
- `questions_hash` changes with this task, as intended: the decisions after it are a new
  calibration group.
