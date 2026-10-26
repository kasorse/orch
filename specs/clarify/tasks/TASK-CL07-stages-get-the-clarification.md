---
id: CL07
repo: orche
depends_on: [CL06]
jira: none
---
# CL07 — Stages get the clarification; summary.md

**Size** S

## Goal

Every stage prompt that takes the task gets the clarification after it (Contracts: Render), from
`run.json`, on a run and on its resume alike; `summary.md` shows it.

## Files

- `internal/action/prompts.go`: the research, investigate, implement (first and retry) and review
  inputs gain `Clarification string` (the rendered text), placed after the task through `fence`;
  empty adds nothing, so every existing golden stays as it is.
- `internal/action/dispatcher.go`: `run.Task.Clarification.Render()` into each input.
- `summary.md` rendering: a Clarification section (the points with who answered, the additions,
  the checks against the threshold); for a `task needs input` run, the open points.
- New goldens: `investigate_clarified.golden`, `implement_clarified.golden`.

## Acceptance

- The clarified investigate prompt golden holds the request in its task block and the clarification in a fenced block after it.
- The clarified implement prompt golden holds the same clarification block.
- Every prompt golden that existed before this task is unchanged.
- A resumed clarified run sends the same clarification block as the first run did (integration).
- `summary.md` of a `task needs input` run lists the open questions and the last P(clear) against the threshold.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/clarify/`.

## As built (2026-10-25)

Run `c1c20098-c481-45b4-b4ba-d985c9d336aa` in orche: completed, 0 fix iterations, tests passed, no review.
Branch `story/clarify/CL07` on `story/clarify/CL06` @ 8956391, commit c3a34bf.
Cost $0.62. Implementation summary: Pass the clarification to stage prompts after the task.
Conflicts with spec/clarify: internal/action/extensions_test.go.
