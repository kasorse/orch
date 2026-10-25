---
id: CL08
repo: orche
depends_on: [CL07]
jira: none
---
# CL08 — Scenarios and documentation

**Size** M

## Goal

The change is covered end to end and documented where an operator and the next developer look.
MVP.md becomes the normative home; `specs/clarify/SPEC.md` stays the design record. This is the
only task that edits the shared documents.

## Files

- `test/integration`: end-to-end scenarios with the fake CLI: a clear task runs without clarify;
  an unclear task at a simulated terminal is clarified by the operator and the investigate prompt
  holds the answer; `--mode auto` with instructions that answer the question goes on, without
  them stops with exit 4; `run --stage development` on an unclear task runs clarify; a story task
  never calls clarify; a config with `analysis` fails before any call.
- `internal/report/calibrate.go`: `decisions calibrate` shows the `P(clear)` distribution per
  `questions_hash` and the share of runs that stopped `task needs input`; no proposal (A1).
- MVP.md: a new §49 "Clarify" from SPEC.md (Approach, Contracts, Decisions); updates to §8 (flow),
  §9 (asked stages, fallback, `policy.clarify`), §10/§17/§20 (actions, artifacts, prompts),
  §21/§43 (`task.clarification`, the clarity trace, `summary.md`), §37 (resume refusals), §38.1
  (the clarify call's authority), §40 (stage names), §44.4 (criteria from the investigation), §48
  (a story task skips clarify).
- CLAUDE.md: the decision flow, the stage → action table, the invariants (clarify has `none`; the
  decision provider sees text only), the run artifacts, and a pointer to `specs/clarify/` in the
  opening list.
- README.md and `orchestrator.example.yaml` comments: the `clarify:` section, `policy.clarify`, the
  new template.
- `bench/README.md`: why the bench configs set `clarify: {enabled: false}`.
- TODO.md: tick Q9 and say what was built.

## Acceptance

- Each scenario listed under Files exists in `test/integration` and passes.
- `decisions calibrate` prints the P(clear) distribution and the needs-input share, and proposes no clarity threshold.
- MVP.md has a §49 for clarify, and §8 and §9 describe the clarity question and the two asked stages.
- CLAUDE.md names clarify in the decision flow, lists `investigation → investigate` in the stage table, and points to `specs/clarify/`.
- MVP.md, CLAUDE.md, README.md and `orchestrator.example.yaml` name `analysis`, `analyze`, `planning` or `plan` as a stage or action only in historical notes that say so.
- TODO.md Q9 is ticked with what was built.
- `scripts/check.sh` passes, including `bench/`.

## Notes

Do not edit anything under `specs/clarify/`. The four `bench/` tests that skip under bwrap are run
by the operator outside the sandbox before the branch merges; that is not part of this task.

## As built (2026-10-25)

Run `e7b092ba-7b10-4c30-88bf-38d9376754a5` in orche: completed, 0 fix iterations, tests passed, review pass.
Branch `story/clarify/CL08` on `story/clarify/CL07` @ c3a34bf, commit 1716436.
Cost $5.83. Implementation summary: Add clarify scenarios, calibrate clarity section and docs.
Conflicts with spec/clarify: internal/action/extensions_test.go.
