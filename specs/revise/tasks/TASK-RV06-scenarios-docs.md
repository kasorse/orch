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
