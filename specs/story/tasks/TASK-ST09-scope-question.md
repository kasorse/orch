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
