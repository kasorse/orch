---
id: RV05
depends_on: [RV04]
---
# RV05 — Delivery: the push that updates the MR, new mode, the description, resume

**Size** M

## Goal

SPEC §6.3, §8, §9: the push to the existing branch (remote tip must equal `base_ref`, no `-o`, no
force), `mr.state: updated`, the `Orchestrator-Revises` trailer, the `new` mode through the
unchanged chain, the description appended after the push, §39.12's comment wording for
`updated`, resume without a GitLab client.

## Files

- `internal/app/deliver.go`: an update branch in `deliver` (the ls-remote equality instead of
  absence, the push question text, no merge request question, `PushWithoutUpstream`, no
  options); `domain.MRUpdated`.
- `internal/app/commit.go`: the trailer.
- `internal/app/jira_write.go`: `writeRevisionDescription` (intent first, never repeated, a
  warning on failure); the comment line for `updated`.
- `internal/app/resume.go`: a revise run resumes with no GitLab client, a Jira client only for
  the writes.
- `internal/app/result.go`, `internal/report`: `--output json` `revision`, `summary.md` and
  `report.md` "Revises !N".
- Tests: the remote tip moved → exit 6 and nothing pushed; a non-fast-forward is never forced;
  the description written once across a crash after the intent; `mode: new` opens a merge
  request with the push options.
- Docs: MVP.md §50.6 and §50.14 (the push to an existing branch exists, for `revise`), §54.8–§54.9;
  CLAUDE.md delivery invariant.

## Acceptance

- An update run's push argv has no `--force`, no `-o` and the explicit refspec of the MR's branch.
- `run.json: mr.state` is `updated` with the MR's iid and url; exit 0.
- The ticket's description ends with `Update <date>: <requirement>` after a successful push, and is not touched when the push failed or was declined.
- A resumed revise run builds no GitLab client (a test with the token unset).
- `scripts/check.sh` passes.

## As built (2026-11-11)

- `deliver`: an update (`worktree.existing_branch`) prints `This push updates !N (url).`, asks the
  push question and no merge request question (a branch without a merge request asks it and opens
  one), requires `ls-remote` to show the branch at `base_ref`, pushes without `-u` and without
  options, records `mr.state: updated` with the merge request's iid and url. The push question
  keeps §50.5's wording; the line before it says what the push does (SPEC §6.3 had its own text).
- `domain.MRUpdated` and `RunMR.HasMergeRequest()` (created or updated), used by the result line,
  `report.md`, `jira.write` and the load checks; `summary.md` reads `!N url, updated`.
- `writeReviseDescription` (`jira_write.go`): after `writeJira`, for a revise task with a key, once;
  `run.json: jira_write.description` reuses the comment's record type (`{state}`, no id). The date is
  the run's start date in UTC; the requirement is cut out of the task text at `reviseMarker`.
- The trailer `Orchestrator-Revises: !N` goes on every revise commit with a merge request, a new
  one's included.
- Resume needs nothing new: it never calls `resolveRevise`, so it builds no GitLab client by
  construction; a resume that finds `jira_write.description` writes nothing again. A process that
  dies between the push and the description leaves the requirement out of the ticket (the push is
  never repeated, and a completed run is not resumable); the operator adds it by hand.
- MVP.md §50.6, §50.14, §54.8, §54.9, §54.10; CLAUDE.md's delivery invariant.
