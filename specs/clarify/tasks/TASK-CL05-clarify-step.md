# CL05 — The clarify step

**Size** L (4–5 d) · **Depends on** CL03, CL04 · **Blocks** CL06, CL07

## Goal

An unclear task is clarified before the decision (`SPEC.md` §4–§7): one clarify call with the
instructions, the operator's answers at a terminal or the model's own under `--mode auto`, a
re-check, the operator's additions, and a `needs input` stop. The result is
`run.json: task.clarification`.

## Files

- `internal/config`: the `clarify` section (`enabled` default true, `provider`, `model`, `effort`,
  `timeout`, `max_rounds` 0..5 default 2; `docs`/`max_bytes` from CL04), required: a route or
  `enabled: false` written out (D14); with `enabled: true` checked in
  `ValidateDependencies`, `ValidateAuthorities` (`AuthorityNone`), the CLI version check, the
  org layer's `allowed_models`, and `CostReporter` under `limits.max_cost_usd`
- `internal/action`: `BuildClarifyPrompt` (marker `ACTION: clarify`, `taskBlock` for the request,
  `fence` for rules, excerpts and the clarification so far), `ParseClarify` (strict, caps: 8
  points, 300 bytes per field); a golden `clarify_interactive.golden`, `clarify_auto.golden`
- `internal/domain`: `Clarification`, `ClarifyRound`, `ClarifyPoint`, `Render()` (`SPEC.md` §7.2),
  `Validate()`
- `internal/interaction`: `Clarifier` — shows the points, reads the answers (Enter keeps, text
  replaces, `-` leaves open), reads an addition (ended by an empty line); on `UserCtx`; EOF
  cancels
- `internal/classification/service.go`: after the pre-check, `ClearEnough` or the loop: clarify
  call → answers (operator or auto) → re-check (`Decisions.Clarity`) → additions up to
  `max_rounds` → clear, or `needs input`; the decision text and the Context Manager's query
  become request + rendered clarification, translated again when the translator applies
- `internal/app`: build the clarify call's provider with `AuthorityNone` and `ScrubbedEnv`; pass
  `interactive` (already computed before the decision); `run --stage` runs the same loop;
  `run --story-task` skips it; a `needs input` result ends the run `stopped`, `failure_reason:
  "task needs input"`, exit 4, the open points printed; usage into `decision_overhead` and the
  budget
- `internal/persistence`: `task.clarification` strict on load (`SPEC.md` §7.1); logs and artifacts
  of §7.4; resume refuses a run that stopped with `task needs input`
- `internal/testutil/fakeexec`: route `ACTION: clarify` and `ACTION: decide-clarity`

## Steps

1. Domain types, render and validation; prompt and parser with goldens.
2. The service loop with fakes for the provider, the clarify call and the operator.
3. App wiring, persistence, resume refusal, exit code.

## Acceptance

- Unit (service): P(clear) above the threshold makes no clarify call; below, interactive, the
  operator's answer is recorded `by: operator` and the re-check sees it in its state; auto takes
  `docs` and `assumption` answers; two failed re-checks at a terminal with `max_rounds: 1` stop
  with `task needs input`.
- Unit: a clarify answer with `basis: assumption` in interactive mode is `InvalidResponse`.
- Unit: a failed clarify call (a non-auth provider error) fails the run with `ProviderError` (D13).
- Unit: `clarify: {enabled: false}` is a valid config without a route; the run makes no clarify
  call whatever `P(clear)` is, and `decision.json: clarity.status` is `disabled`.
- Unit: the clarify call is made with `AuthorityNone` and the claude adapter gives it no tools.
- Integration (fake CLI): `--mode auto` on an unclear task exits 4, `run.json` has
  `status: stopped`, `task.clarification.status: unclear`, no workflow; `resume` refuses it.
- Integration: a clear task's `run.json` has no `task.clarification`.

## Gates

`scripts/check.sh`
