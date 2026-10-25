---
id: CL06
repo: orche
depends_on: [CL05]
jira: none
---
# CL06 — The clarify loop: service, operator, auto, needs input, persistence

**Size** L

## Goal

An unclear task is clarified before the decision (Approach §2–§4): one clarify call with the
instructions, the operator's answers at a terminal or the model's own under `--mode auto`, a
re-check, the operator's additions, and the `task needs input` stop. The result is persisted as
`run.json: task.clarification`.

## Files

- `internal/interaction`: `Clarifier`: shows the points as in Approach §3 (Enter keeps, text
  replaces, `-` leaves open), reads an addition ended by an empty line; waits on `UserCtx`; EOF
  cancels.
- `internal/classification/service.go`: after the pre-check, `ClearEnough` or the loop: `Docs`
  (CL04) → clarify call → answers (operator or auto) → re-check (`Decisions.Clarity`) → additions
  up to `max_rounds` → clear, or needs input. After a clarification the decision text and the
  Context Manager's query are the request plus `Render()`, translated again when the translator
  applies. `clarify.enabled: false` skips the loop; `run --story-task` skips the question too.
- `internal/app`: builds the clarify call's provider with `AuthorityNone` and `ScrubbedEnv`;
  passes the existing `interactive`; `run --stage` runs the same loop; a needs-input result ends
  the run `status: stopped`, `failure_reason: "task needs input"`, exit 4, the open points
  printed; a failed clarify call (not auth) fails the run with `ProviderError`; usage goes into
  `decision.json` (`usage.clarify[]`), `decision_overhead` and the budget.
- `internal/persistence`: `task.clarification` validated strictly on load; the logs and artifacts
  of Contracts; resume refuses a run stopped with `task needs input`.
- `internal/testutil/fakeexec`: route `ACTION: clarify`.

## Steps

1. The service loop with fakes for the provider, the clarify call and the operator.
2. App wiring, persistence, the resume refusal, the exit code.
3. Integration scenarios with the fake CLI.

## Acceptance

- When the pre-check's P(clear) is at or above the threshold, no clarify call is made and `run.json` has no `task.clarification`.
- Interactive: an operator's answer is recorded with `by: operator`, and the re-check's state contains it.
- Auto: points with basis docs and assumption are recorded as answered, basis none stays open, and no operator input is read.
- At a terminal with `max_rounds: 1` and two re-checks below the threshold, the run stops with `task needs input`.
- `--mode auto` on an unclear task exits 4; `run.json` has `status: stopped`, `task.clarification.status: unclear` and no workflow.
- `orchestrator resume` refuses a run that stopped with `task needs input`.
- A failed clarify call that is not an auth failure fails the run with a provider error.
- The clarify call is made with authority `none`, and the claude adapter gives it no tools.
- `clarify: {enabled: false}` makes no clarify call whatever P(clear) is, and `decision.json` records `clarity.status: disabled`.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/clarify/`.

## As built (2026-10-25)

Run `338047e1-552e-4ada-a998-e0eecf06b7c5` in orche: completed, 2 fix iterations, tests passed, review pass.
Branch `story/clarify/CL06` on `story/clarify/CL05` @ 2e6b6dd, commit 8956391.
Cost $9.30. Implementation summary: Count clarify calls in the budget and in an undecided run's outcome.
Conflicts with spec/clarify: internal/action/extensions_test.go.
