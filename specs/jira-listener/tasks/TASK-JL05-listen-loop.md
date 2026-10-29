---
id: JL05
repo: orche
depends_on: [JL04]
jira: none
---
# JL05 — orchestrator jira listen: poll, claim, outcomes, state, restart, stop

**Size** L

## Goal

The supervisor (SPEC.md §1, §3.2, §3.3, §4, §7, §8, §9, §10), built on JL01–JL04.

## Files

- `internal/listener/state.go` (pure): the state's types (SPEC §9), strict validation, the phase
  transitions. `outcome.go`: the SPEC §8 table from `(exit code, JSON line, run outcome)` to
  `{comment kind, transition, attempt counted, stop}`. `comment.go`: Go's wiki templates, with
  the escaping `internal/jira` uses for §39.12 (export what is needed), an 8 KiB bound,
  `[orchestrator]` as the first line and `Run <id>` as the last.
- `internal/app/listen.go`: `orchestrator jira listen [--config] [--once]` and its refusals; the
  fail-fast chain with `DependencyOptions{Listener: true}`; `GET /myself`; the flock on
  `.orchestrator/listener/lock`; stale clone cleanup; recovery (SPEC §9); the loop (poll only
  while idle); the Go re-check (§3.3); the claim (§4); the task file (§7.2) and the child via
  `runChild` with `selfExecutable()`, in the service directory, `--mode auto --output json` and
  `--config <default>` when needed; the child's environment (no Jira variables, the git
  credential entries, the token kept for git); the outcome, the status re-check and the writes
  with an intent in the state before each; backoff; SIGTERM/SIGINT; the stops of §10.
- Integration tests in `test/integration` with the fake Jira (search, comments, transitions,
  status), fake claude and a local bare repository as the GitLab remote.

## Acceptance

- A clear ticket is claimed, run, gets an MR (fake push), a comment with the link and the `done` transition; the state shows it finished.
- An unclear ticket gets the numbered open points and `blocked`; after a fake answer comment and the status back in the queue, the next run's task holds the discussion.
- A red test, and an exit 6, each give a comment with the reason and `failed`.
- A ticket moved by someone during the run gets the comment and no transition.
- A claim whose transition is no longer offered is skipped and nothing else is written.
- A provider failure sends the ticket to `retry`, counts no attempt, and it is not picked before `next_try_at`.
- `failure_kind: auth` sends the ticket to `retry` and the listener exits 2.
- A spent ticket gets one comment and `failed`, and is never dispatched again.
- Killed during a run and restarted, the listener resumes the run and writes its outcome once; a second listener in the same directory refuses.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira-listener/`, nor MVP.md, CLAUDE.md, TODO.md, README.md
(JL06 writes MVP.md §51).
