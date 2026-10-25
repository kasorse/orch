# CL06 — Stages get the clarification

**Size** S (1 d) · **Depends on** CL02, CL05 · **Blocks** CL07

## Goal

Every stage prompt that takes the task gets the clarification after it (`SPEC.md` §7.2), from
`run.json`, on a run and on its resume alike; `summary.md` shows it.

## Files

- `internal/action/prompts.go`: research, investigate, implement (first and retry) and review
  inputs gain `Clarification string` (the rendered text), placed after the task through `fence`
  with the heading of §7.2; empty → nothing added, so every existing golden stays as it is
- `internal/action/dispatcher.go`: `run.Task.Clarification.Render()` into each input
- `internal/report` / `summary.md`: a "Clarification" section (the points with who answered,
  the additions, the checks against the threshold), and for a `needs input` run the open points
- new goldens: `investigate_clarified.golden`, `implement_clarified.golden`

## Acceptance

- Golden: a clarified investigate prompt holds the request in its task block and the
  clarification in a fenced block after it.
- Integration: a resumed clarified run sends the same clarification block as the first run did.
- `summary.md` of a `needs input` run lists the open questions.

## Gates

`scripts/check.sh`
