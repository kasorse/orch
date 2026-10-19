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

## As built

2026-10-19. Written without running anything (no shell in the run that wrote it): `scripts/check.sh`
is the run's test step, and gofmt was kept by hand. **Left to the operator:** `scripts/check.sh`
outside the sandbox (the `bench/` tests that skip under bwrap), once before the story's branch
merges, as the Acceptance asks.

- **Scenario.** Most of the end-to-end scenario already existed: `story_plan_test.go` (plan on
  fake repositories, a fake Jira ticket with components), `story_run_test.go` (four tasks in two
  repositories, order, one stacked branch per repository, As built, README, the final table, the
  operator's checkouts and a re-run starting nothing, failure/resume, retry, blocked, edited task,
  budget, lock, Ctrl-C) and `story_jira_type_test.go`. ST07 left the one missing piece, the whole
  `story run` driving the Jira creation, so `story_jira_children_test.go`
  (`TestStoryRun_CreatesJiraChildrenBeforeTheFirstDispatch`) adds it: a story planned from `PAY-1`
  with `jira.create`, a fake Jira that now answers `POST /rest/api/2/issue` with a 201 (`jira_test.go`
  records the method and body of a creation), four creations in the story's order with the
  parent, type, labels and summary, the keys in `story.json`, the task files and the commit
  headers, no second creation on a second `story run`, the checkouts untouched. `jiraOptions`
  gained `create` (the `jira.create` lines) and the run fixture a `jira` option. `story plan` and
  `story run` are not chained in one test: the plan fixture uses the fake providers and the run
  fixture a fake Jev with the test binary as the child orchestrator, and the plan's output is
  pinned against golden files that the run fixture's hand-written spec mirrors.
- **MVP.md.** New §48 (§48.1–§48.10) from `SPEC.md` §5–§15; new §39.11 (the story issue type, the
  children) with the §39.10 "not covered" line amended; §30 (the child issues are the one Jira
  write; a story's spec is a list of tasks, its tasks run one at a time); §8.2 (the question) and
  §9.1 (the recalibration note); §42.2 and §47.5 (the story task's commit).
- **CLAUDE.md.** The pointer to §48, the scope question in the decision flow, the two invariants,
  §30's summary and the story and run artifacts. **README.md.** "Planning and running a story",
  the usage lines, "What is sent to Jira" and the limitations. `orchestrator.example.yaml` already
  had `story` and `jira.create` (ST02, ST07). **TODO.md.** W6 ticked with a link here; W3 notes
  the child issues.
