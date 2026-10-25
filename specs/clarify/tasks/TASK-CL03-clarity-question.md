# CL03 — The clarity question

**Size** M (2–3 d) · **Depends on** — · **Blocks** CL05

## Goal

The decision provider gives `P(clear)` in the pre-check's call, and can be asked it again on a
request with its clarification (`SPEC.md` §4, §7.3). Go compares it with
`policy.clarify.threshold`. In this task the answer is recorded and decides nothing yet: CL05
acts on it.

## Files

- `internal/decision/questions.go`: `ClarityQuestion()` (yes/no, ID `clarity`), included in
  `QuestionsHash`. Text:
  - instructions: "Is `request` clear enough to act on: is it stated what has to be achieved,
    where, and how to tell that it is done?"
  - yes: "A competent engineer new to the task could start without asking anything: the goal, the
    part of the system it concerns and the expected result are stated or follow unambiguously."
  - no: "Something essential is missing or ambiguous: the goal, the affected part, the expected
    behaviour or result, or a choice the request leaves open."
- `internal/decision/state.go`: `ClarityState{Request, Clarification string}`
- `internal/decision/provider.go`: `ContextDepthDecision` carries `PClear`; new
  `Clarity(ctx, text, clarification string) (domain.ClarityDecision, error)`
- `internal/decision/jev`: the pre-check request asks `clarity` beside `context_depth`; a
  `Clarity` request asks it alone; mapper strict; the mock's heuristic (a request under ~8 words
  or without a verb is unclear; a named file, function or error message makes it clear)
- `internal/decision/llm`: the pre-check prompt adds the question; `ACTION: decide-clarity` for
  the re-check; `parse.go` strict
- `internal/domain/policy.go`: `DecisionPolicy.ClarityThreshold`, in `PolicyHash`
  (`TestPolicyHash_PinnedEncoding` changes on purpose: say so in As built)
- `internal/policy`: `ClearEnough(p float64, pol DecisionPolicy) bool` (inclusive)
- `internal/config`: `policy.clarify.threshold`, (0, 1], required
- `internal/domain/trace.go`, `internal/persistence/validate.go`: `decision.json: clarity`
  (`threshold`, `checks`, `status`, reason), `usage.clarity_recheck[]`; old decisions load
- golden files: Jev request goldens

## Steps

1. Question, state, domain types, then each provider with its tests.
2. `classification.Service` records the pre-check's `P(clear)` in the trace; nothing else uses it.
3. `run --stage`: `Clarity` is called on the request; the decision stays `source: manual`.

## Acceptance

- Unit: the Jev mapper refuses a pre-check answer without `clarity`; the LLM parser the same.
- Unit: `P(clear)` equal to the threshold is clear.
- Unit: `policy.clarify.threshold: 0` is a config error.
- `decision.json` of an ordinary run holds `clarity.checks` with one value.

## Gates

`scripts/check.sh`
