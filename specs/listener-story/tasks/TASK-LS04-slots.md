---
id: LS04
repo: orch
depends_on: [LS03]
jira: none
---
# LS04 — Slots: up to `max_parallel` runs at once, at most one per service

**Size** L

## Goal

SPEC §3: the listener runs tickets of different services at once. With `max_parallel: 1` its
behaviour and output are those of today.

## Files

- `internal/app/listen.go`: the loop polls whenever a slot is free (at start after the recovery,
  when a run ends, every `poll_interval`); `poll` dispatches until the slots are full; the service
  is computed from the head before the claim and a busy service skips the ticket; each run
  (`process` / `resume`) in a goroutine holding a slot; the state behind one mutex (`update`,
  `save`); `contained` per goroutine.
- Output above one slot: `.orchestrator/listener/logs/<KEY>.<attempt>.log` for the run's reporter,
  one stderr line per start and per end.
- Stop: cancel every run, wait for all, save. Restart: recover every unfinished entry within the
  slots and one per service; the rest wait before the first poll.
- `SetListenRunHook` and the test harness: a barrier so a test can hold two runs at once.
- Tests: two services at once (both in progress at the barrier); two tickets of one service one
  after another; a third ticket waits for a slot; SIGTERM with two runs → both stay `start` and
  are resumed on restart; a panic in one run leaves the other running; `go test -race` on the
  listener's tests.

## Acceptance

- With `max_parallel: 1` the existing listener tests pass unchanged.
- No two runs of one service are ever in progress at once, under `-race`.
- `scripts/check.sh` passes.
