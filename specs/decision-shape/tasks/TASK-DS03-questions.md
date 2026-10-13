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
