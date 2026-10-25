---
id: CL03
repo: orche
depends_on: [CL02]
jira: none
---
# CL03 — The clarity question: pre-check, re-check, threshold, trace

**Size** M

## Goal

The decision provider gives `P(clear)` in the pre-check's call and can be asked it again on a
request with its clarification (Approach §2, Contracts: The clarity question). Go compares it with
`policy.clarify.threshold`. In this task the answer is recorded and decides nothing: CL06 acts on
it.

## Files

- `internal/decision/questions.go`: `ClarityQuestion()` with the texts of Contracts, included in
  `QuestionsHash`.
- `internal/decision/state.go`: `ClarityState{Request, Clarification string}`.
- `internal/decision/provider.go`: `ContextDepthDecision` carries `PClear`; the interface gains
  `Clarity(ctx, request, clarification string) (domain.ClarityDecision, error)`.
- `internal/decision/jev`: the pre-check request asks `clarity` beside `context_depth`; a
  `Clarity` request asks it alone; the mapper is strict; the mock's heuristic: a request under
  about 8 words or without a verb is unclear, a named file, function or error message makes it
  clear.
- `internal/decision/llm`: the pre-check prompt adds the question; the re-check prompt has first
  line `ACTION: decide-clarity`; `parse.go` is strict.
- `internal/domain/policy.go`: `DecisionPolicy.ClarityThreshold`, in `PolicyHash`
  (`TestPolicyHash_PinnedEncoding` changes on purpose: say so in the commit).
- `internal/policy`: `ClearEnough(p float64, pol domain.DecisionPolicy) bool`, inclusive.
- `internal/config`: `policy.clarify.threshold`, required, in (0, 1].
- `internal/domain/trace.go`, `internal/persistence/validate.go`: `decision.json: clarity`
  (`threshold`, `checks`, `status`, `reason`) and `usage.clarity_recheck[]`; old decisions load.
- `internal/classification/service.go`: records the pre-check's `P(clear)` in the trace.
- `internal/app`: `run --stage` asks `Clarity` on the request and records it; the decision stays
  `source: manual`.
- `internal/testutil/fakeexec`: route `ACTION: decide-clarity`.
- Golden files: the Jev request goldens.
- `orchestrator.example.yaml` and test configs: `policy.clarify.threshold: 0.80`.

## Acceptance

- The Jev mapper refuses a pre-check answer without `clarity`, and the LLM parser does the same.
- `policy.ClearEnough` returns true for a probability equal to the threshold.
- `policy.clarify.threshold: 0` and a missing `policy.clarify.threshold` are config errors.
- An ordinary run's `decision.json` holds `clarity.checks` with exactly one value, and the run behaves as before this task.
- A `decision.json` written before this task still loads.
- `QuestionsHash` covers the clarity question: changing its text changes the hash (unit test).
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/clarify/`.
