# ST09 — the scope question (shadow)

**Size** M (2 d) · **Depends on** — · **Blocks** ST10

## Goal

Every provider-decided run records how story-like its task looks, and nothing uses it
(`SPEC.md` §13).

## Files

- `internal/decision/questions.go`: `ScopeQuestion()` (yes/no, ID `scope_story`, the text of
  §13 with true/false criteria); part of `QuestionsHash`
- `internal/decision/jev`: sent as a `noul` question; the answer mapped and validated like the
  stage answers; the mock answers 0.15
- `internal/decision/llm`: asked and parsed with the others
- `internal/domain/decision.go`, `trace.go`: `scope.story` probability in the classification
  result and `decision.json`, validated on load (absent for `--stage`, `--story-task` and older
  decisions)
- `internal/classification`: not asked for a story task (a flag from ST05; until then always
  asked)
- goldens: `go test ./internal/decision/jev -update` and the LLM prompt goldens

## Acceptance

- Unit: the hash changes; a provider answer without the scope answer is an `InvalidResponse`
  only when it was asked; `decision.json` round trip.
- The policy, the workflow and the routes are byte-for-byte the same for the same stage and
  complexity answers with any scope answer (a test over the policy with two scope values).
- `decisions calibrate` groups the new decisions under the new hash (no code change expected:
  check it).
- MVP.md §9.1 gets a note in ST10: re-check the cuts once enough decisions exist under the new
  hash.

## Gates

`scripts/check.sh`

## As built

- `decision.ScopeQuestion()` (ID `scope_story`, the §13 text with true/false criteria, no stage)
  is part of `QuestionsHash`, which therefore changes; `StageQuestions` does not return it.
- `domain.ScopeDecision{Story}` is `TaskClassification.Scope` and `DecisionTrace.Scope`
  (`scope.story` in `decision.json`), both optional. `TaskClassification.Validate` and
  `decodeDecision` (`validateDecisionScope`) range-check it when present; a trace without it, as
  from `--stage`, `--story-task` or before ST09, still loads.
- ST05 added no flag, so the flag is new: `classification.Request.StoryTask` sets
  `domain.ClassificationInput.SkipScope`, and `decideWithProvider` passes its `storyTask`. The
  zero value asks the question. The state sent to a provider does not carry it.
- Jev: the question is sent as a `noul` question only when asked, and the answer is required
  (`InvalidResponse` when missing) only then; an answer to an unasked one is an "unexpected
  answer". The mock answers 0.15.
- LLM: an asked question adds a "Scope question" block and `"scope": <p>` to the prompt; the
  parser requires `scope` when asked and refuses it when not. There are no prompt goldens; the
  Jev goldens (`classification_*.golden.json`) and the `classification_response.json` fixture were
  edited by hand to what `-update` writes, and the Jev golden tests pass against them.
- The questions hash of a story task is the same constant as every other run's, although it did
  not ask the question: the hash names the question set, and a test pins it. `decisions
  calibrate` groups by `trace.QuestionsHash` (`internal/report/shape.go`), so the new decisions
  form their own group with no code change there; the policy, workflow and route code was not
  touched, and `TestScopeAnswerChangesNothingButItself` runs it with scope 0.05 and 0.95 and
  compares the results byte for byte (routes depend on the `Decision` alone).
- MVP.md is unchanged; the §9.1 re-check note is ST10's.
- `scripts/check.sh`: every package's tests, the integration tests included, passed; the first
  run stopped only on `gofmt -l` for `internal/domain/trace.go`, whose field alignment was then
  fixed.
