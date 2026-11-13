---
id: LS03
repo: orch
depends_on: [LS02]
jira: none
---
# LS03 — Blockers: a ticket blocked by an unfinished one waits

**Size** S

## Goal

SPEC §4: with `listener.blockers`, the re-check skips a ticket with a blocker whose status is not
in `done_statuses`. Still one ticket at a time.

## Files

- `internal/listener` (pure): `Waiting(head, linkType, doneStatuses) (blocker TicketRef, waits
  bool)`: the first unfinished blocker of that link type, case-insensitive on the type and the
  status; a blocker without a status is unfinished.
- `internal/app/listen.go`: `eligible` (or the poll right after it) skips a waiting ticket, with
  one log line the first time it waits on that blocker (kept in memory, not in the state); no
  claim, no comment, no attempt.
- Tests: the pure function (table); a listener test: a ticket blocked by one in Code Review is
  skipped, then picked when the fake Jira says Done; without `listener.blockers` nothing changes.

## Acceptance

- A waiting ticket is never claimed and its state entry is not created.
- The log line names the ticket, the blocker and the blocker's status.
- `scripts/check.sh` passes.
