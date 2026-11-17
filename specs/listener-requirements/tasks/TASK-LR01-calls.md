---
id: LR01
repo: orch
depends_on: []
jira: none
---
# LR01 — The requirements call and specify without target surveys

**Size** M

## Goal

SPEC §2 and §4: the tool-less requirements call with its strict answer, and specify fed with the
requirements instead of target surveys. Nothing in the listener uses them yet.

## Files

- `internal/story`: `RequirementsAnswer`, `ParseRequirements` (the bounds of §2, unknown keys
  refused, `AnswerError` naming the field).
- `internal/action`: `Requirements(ctx, call, RequirementsInput) (RequirementsResult, error)`
  (`AuthorityNone`, planning kind `requirements`, timeout `actions.investigate.timeout`); its prompt.
  `SpecifyInput.Requirements` and `SpecifyInput.Notes`; the specify prompt's branch of §4.
- Tests: `ParseRequirements` (table); the requirements prompt (golden, with and without
  references); the specify prompt with requirements (golden); the specify prompt without them is
  byte-identical to today's golden.

## Acceptance

- `story plan`'s specify prompt is unchanged (its golden test passes untouched).
- `scripts/check.sh` passes.

## As built (2026-11-17)

Run `8689df97-2835-45a5-8d3f-a64da9451855` in orch: completed, 0 fix iterations, tests passed, review pass.
Branch `story/listener-requirements/LR01` on `spec/listener-requirements` @ c2fd9f5, commit 4f88cb7.
Cost $1.31. Implementation summary: Add requirements call and specify prompt branch without target surveys.
Conflicts with spec/listener-requirements: none.
