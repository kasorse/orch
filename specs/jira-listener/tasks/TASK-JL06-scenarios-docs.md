---
id: JL06
repo: orche
depends_on: [JL05]
jira: none
---
# JL06 — The documentation of the listener and the changed invariants

**Size** M

## Goal

Every shared document the listener makes out of date (SPEC.md §12), written once all its code
exists. Read the commits of JL01–JL05 (`git log`) for what they left for this task.

## Files

- MVP.md: a new §51 (the listener: CLI, configuration, polling, claim, service and project,
  checkout, the task, outcomes, state and restart, stopping, security), a summary of SPEC.md that
  names `specs/jira-listener/` as its design record; §30 out of scope amended (SPEC §12); §39.3,
  §39.4, §39.5, §39.7, §39.10 (search, comments, `/myself`, the discussion section, the
  `listener` source flag); §50.10 (`failure_kind`); §50.13 (the GitLab token reaches only git,
  through the helper); §5/§6 (the `listener` and `gitlab` keys); the Jira scenarios list.
- CLAUDE.md: the pointer to MVP.md §51 and `specs/jira-listener/` in the opening list; an
  invariant paragraph ("the listener owns Jira, its child never calls it; the GitLab token
  reaches only git through the credential helper; only allowlisted groups are cloned; only
  listed reporters start a run"); the out-of-scope paragraph; the run artifacts
  (`.orchestrator/listener/state.json`, `lock`, `tasks/`).
- `orchestrator.example.yaml`: the `listener` and `gitlab` sections, commented.
- README.md: running the listener in Docker (the working directory is a volume; the stop timeout
  above the child's wait delay; check the Jira workflow's transitions and their screens first; a
  cloned repository's `orchestrator.yaml` is trusted because its group is allowlisted).
- TODO.md: JL ticked with a "Сделано" line; new items for D18 (story mode for Epics and
  multi-service tickets) and D19 (every reporter).

## Acceptance

- MVP.md has §51, and every SPEC.md section from §1 to §11 is summarised or referenced there.
- MVP.md §30 and §50.13 and CLAUDE.md's out-of-scope paragraph no longer contradict the listener (JQL search, comments, writes without an MR, the GitLab token).
- CLAUDE.md names MVP.md §51 and `specs/jira-listener/` and states the listener's invariants.
- `orchestrator.example.yaml` loads with `orchestrator validate` when its listener lines are uncommented (a test or a check in the commit message).
- TODO.md has JL ticked and the D18 and D19 follow-ups.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira-listener/` (story run writes its README and As built).
