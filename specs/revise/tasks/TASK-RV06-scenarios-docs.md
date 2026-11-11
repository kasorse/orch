---
id: RV06
depends_on: [RV05]
---
# RV06 — Integration scenarios and the changed invariants

**Size** S

## Goal

End-to-end scenarios in `test/integration` with the fake executables, a fake GitLab API, the
fake Jira and a bare remote; the closing summary of the feature.

## Files

- `test/integration`: a fake GitLab (`gitlab_test.go`, like `jira_test.go`); scenarios: Jira
  target, MR opened → update, description appended; MR merged → new MR; run target; branch target
  without MR → push with merge_request.create; the remote moved → exit 6; a write-scoped token →
  exit 2 before any call.
- Docs: MVP.md §54 intro and §54.10 (not covered); CLAUDE.md the summary line for
  `specs/revise/`; TODO.md; this spec's README `Status:`.

## Acceptance

- Every scenario passes with no network and no real CLI.
- `scripts/check.sh` passes.

## As built (2026-11-11)

- `test/integration/revise_test.go`, scenarios 80-83: a Jira key updating an open merge request
  (no push options, the parent is the merge request's tip, the review prompt has the first
  commit's file, the description PUT, only GETs to GitLab); a merged one opening a new merge
  request; a token with `api` refused before any other request (exit 2, no run, no Jira call); a
  planned revise resumed with the token unset (no GitLab request on resume).
- The fake GitLab API is a TLS `httptest` server reached under the remote's host `gitlab.test`
  through `app.SetGitLabHTTPClient` (a test seam like `SetOrgConfigEtcPath`) and
  `gitlab.Options.HTTPClient`; the fake Jira now answers a PUT with 204 and records its body.
- Not run as scenarios, covered by unit tests instead: an MR URL, a run and a branch target
  (`internal/app/revise_test.go`), the remote moving before the push and the manual-mode questions
  (`revise_deliver_test.go`), the local-branch cases (`revise_worktree_test.go`).
- A push updates the remote-tracking ref of the branch it pushed, as every push does; the fetches
  write only under `refs/orchestrator/revise/`.
- MVP.md §54.11; CLAUDE.md's spec line and run artifacts; TODO.md `RV`.
