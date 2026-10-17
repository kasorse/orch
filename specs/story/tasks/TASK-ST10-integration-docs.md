# ST10 — scenarios, documentation

**Size** M (2.5 d) · **Depends on** all · **Blocks** —

## Goal

The story is covered end to end, normative in MVP.md, and visible in the project's own
instructions (`SPEC.md` §16).

## Files

- `test/integration`: the end-to-end scenario of `SPEC.md` §16 not already added by
  ST04–ST08: `story plan` on two fake repositories with a fake Jira parent → `story run` → the
  children created → three tasks in order with one stacked branch → completed; the operator's
  checkouts untouched; the final table
- MVP.md: new §48 (from `SPEC.md` §5–§15); §30 (Jira writes for an approved story; a spec is not
  a workflow language); §39 (the story issue type, the children); §9 and §9.1 (the shadow
  question, the recalibration note); §42 and §47 (the story task's commit)
- CLAUDE.md: the decision flow mentions the scope question; the invariants "a model proposes,
  Go orders, the operator approves", "Jira writes only after approval, never twice"; the new
  run and story artifacts
- README: planning and running a story
- `orchestrator.example.yaml`: `story`, `jira.create`
- TODO.md: a line for the story feature with a link here; W3 notes what this delivered

## Acceptance

`scripts/check.sh` passes outside the sandbox, root and `bench/`.

## Gates

`scripts/check.sh`
