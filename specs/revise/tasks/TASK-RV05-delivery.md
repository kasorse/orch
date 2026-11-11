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
