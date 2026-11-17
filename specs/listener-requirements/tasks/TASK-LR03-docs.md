---
id: LR03
repo: orch
depends_on: [LR02]
jira: none
---
# LR03 — The end-to-end scenario and the documentation

**Size** S

## Goal

SPEC §8 (the scenario) and §9 (the documents).

## Files

- The listener-story end-to-end scenario: references surveyed first, the requirements comment
  before the children, no target survey; a second scenario where a question blocks the Epic and a
  human answer lets the next plan through.
- MVP.md §51.13 and §51.2; `specs/listener-story/SPEC.md` amendment notes (§5.3, §6, §7.1, §9);
  README.md's listener section; CLAUDE.md if it names the plan's steps; TODO.md (SPEC Risks).
- Whatever LR01 and LR02 said in their commits that this task must document.

## Acceptance

- The scenarios pass under `-race`.
- `grep -n "survey of each target\|one survey per target" MVP.md` finds nothing that still says
  the plan surveys the targets.
- `scripts/check.sh` passes.
