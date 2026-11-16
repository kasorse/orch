---
id: LS05
repo: orch
depends_on: [LS04]
jira: none
---
# LS05 — Planning an Epic: catalog, selection, references, survey, specify

**Size** L

## Goal

SPEC §5, §6, §7: with `listener.story`, an Epic is claimed and planned, exclusively (§3), up to a
validated plan saved in `.orchestrator/listener/plans/<KEY>.<attempt>/`. Nothing is created in
Jira yet: the Epic ends `failed` with "creating the children is not built yet" until LS06.

## Files

- `internal/listener` (pure): `Refs(labels, prefix)` (the `gitlab:` segment rules, at most
  `max_refs`), `EpicProjects(labels, prefix)` (several `gitlab:` labels, A2), the per-service
  chain `Chain(tasks, order) []Link` (§7.2), the validation of the select answer against the
  catalog.
- `internal/action`: the `select` call (tool-less, strict answer, SPEC §5.2); `Survey` told when
  the repository is a reference; `SpecifyInput.References` and the parallel rule (§7.1).
- `internal/app`: `listenRunner.plan` — claim, the catalog (`GroupProjects`), labels or select,
  `serviceCheckout.prepare` for targets and references, a survey each on the route of `configFor`,
  specify, `story.Validate`, the chain, the budget (`max_cost_usd`), the plan's artifacts and
  `plan.json`; the exclusive slot; the outcomes of §9 that need no Jira write besides the comment.
- `listener.Entry`: `kind: plan`, `plan_dir`, validated on load; an old state loads.
- Tests: an Epic → select picks two of a catalog of five → two surveys plus one reference survey →
  a plan with a chain; `gitlab:` labels skip select; a select answer outside the catalog → `failed`
  with the answer in the artifacts; a reference that is also a target → `blocked`; the budget
  stops before specify; without `listener.story` the Epic is refused as today.

## Acceptance

- A plan never starts while a run is in progress, and no run starts while it plans.
- Every call's prompt and answer is in the plan's directory, also when it failed.
- `scripts/check.sh` passes.

## As built (2026-11-13)

Run `d4b4a77d-f45e-4b68-bd4c-25dc239e97f8` in orch: completed, 0 fix iterations, tests passed, review pass.
Branch `story/listener-story/LS05` on `story/listener-story/LS04` @ e455f3b, commit ccd7108.
Cost $5.28. Implementation summary: Plan an Epic in the listener up to a validated plan.
Conflicts with spec/listener-story: none.
