# JL05 — `orchestrator jira listen`: poll, claim, outcomes, state, restart, stop

**Size** L

## Goal

The supervisor (SPEC.md §1, §3.2, §3.3, §4, §8, §9, §10).

## Files

- `internal/listener/state.go` (pure): the state's types, strict validation, the phase
  transitions; `outcome.go`: the §8 table from `(exit, json line, run outcome)` to `{comment kind,
  transition, counts}`; `comment.go`: Go's wiki templates (escaping from `jira`'s §39.12 code,
  8 KiB bound, `[orchestrator]` first line, `Run <id>` last).
- `internal/app/listen.go`: the command, args, the fail-fast chain with `Listener: true`, `GET
  /myself`, the flock, the stale clone cleanup, recovery (§9), the loop, the claim, the child via
  `runChild` with the child's environment (no Jira variables, the git credential env), the
  outcome, the writes with intents, the status re-check, backoff, SIGTERM/SIGINT, the stops of §10.
- `--once` for tests.
- MVP.md §51 (new, the command and its loop), §4 if anything changes (it should not).

## Acceptance (integration, fake Jira + fake claude + a local bare repository)

- A clear ticket: claimed, run, MR (fake push), comment with the link, `Ready to Review`.
- An unclear ticket: comment with the numbered open points, `Blocked`; after a fake human comment
  and the status back in `To Do`, the next run's task contains the discussion.
- A red test: `Failed` with the reason. Exit 6: `Failed`.
- A ticket moved by "a person" during the run: comment, no transition.
- A claim that fails (the transition no longer offered): skipped, nothing else written.
- A provider failure: back to `To Do`, no attempt, not picked again before `next_try_at`.
- `failure_kind: auth`: the ticket back to `To Do`, the listener exits 2.
- Attempts spent: one comment, `Failed`, never picked again.
- Kill during a run, restart: the run is resumed and the outcome written once.
- A second listener in the same directory refuses.
- `scripts/check.sh` passes.
