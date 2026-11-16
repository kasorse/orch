---
id: LS07
repo: orch
depends_on: [LS06]
jira: none
---
# LS07 — The end-to-end scenario and the documentation

**Size** M

## Goal

SPEC §11 (the scenario) and §13 (the documents): one test from an Epic to its children's merge
requests, and every shared document brought up to date.

## Files

- A scenario under the listener's tests: an Epic with `ai-run` and a `ref:` label → three children
  (two services, two of them in one) → with `max_parallel: 2` the two services' first children run
  at once, the second child of a service waits until the fake Jira moves its blocker to Done → three
  merge requests, the Epic still at `start`.
- MVP.md §51.2, §51.3, §51.12, new §51.13 and §51.14, §48.1, §39 (SPEC §13); CLAUDE.md's listener
  section and the pointer to `specs/listener-story/`; README.md's listener section;
  `orchestrator.example.yaml` (the keys, commented); `examples/embed/` (the pod's resources for
  `max_parallel`); TODO.md.
- Whatever LS01–LS06 said in their commits that this task must document.

## Acceptance

- The scenario passes under `-race`.
- `grep -n "several tickets at once" MVP.md` finds nothing under "Not covered".
- `scripts/check.sh` passes.

## As built (2026-11-13)

Run `259dd84b-219b-4474-a6d3-a457bcdad608` in orch: completed, 0 fix iterations, tests passed, review pass.
Branch `story/listener-story/LS07` on `story/listener-story/LS06` @ 200240b, commit dc5662c.
Cost $7.07. Implementation summary: Add the Epic-to-merge-requests listener scenario and document it.
Conflicts with spec/listener-story: none.
