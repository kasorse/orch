# JL06 — End-to-end scenarios, MVP.md §51, CLAUDE.md, README, TODO

**Size** M

## Goal

What only exists once JL01–JL05 are done: the summary of the feature and the changed invariants.

## Files

- MVP.md: §51 complete (a summary of SPEC.md), §30 out of scope amended (SPEC §12), §50.13
  (the GitLab token, the credential helper), the Jira scenarios list.
- CLAUDE.md: the new section pointer, the invariant "the listener owns Jira; its child never
  calls it; the GitLab token reaches only git through the helper", the out-of-scope paragraph, the
  run artifacts (`.orchestrator/listener/`).
- README.md: running the listener in Docker (the working directory is a volume; the stop timeout
  above `childWaitDelay`; a cloned repository's `orchestrator.yaml` is trusted because its group
  is allowlisted); `examples/listener/` with a config and a `docker run` line.
- TODO.md: the item ticked; new items for D18 (story mode for Epics and multi-service tickets) and
  D19 (every reporter).

## Acceptance

- `scripts/check.sh` passes; `go test ./internal/action -update` not needed (no prompt changes).
- Every SPEC.md section is reachable from MVP.md §51.
