# DL11 — Integration scenarios 64–78

**Size** M (2.5 d) · **Depends on** DL01–DL09, DL13 · **Blocks** DL12

## Goal

End-to-end scenarios with fake executables, a fake GitLab (`test/integration/gitlab_test.go`, an
httptest server plus a bare repository as the remote) and the fake Jira extended with the write
endpoints.

## Scenarios

| # | scenario |
|---|---|
| 64 | `run --mr` happy path: commit with type and trailers, push, draft MR, description = `report.md` |
| 65 | `run --mr`, review fails: no commit, no push, `mr.skipped`, exit 6 |
| 66 | `run --mr`, the remote refuses the push: `mr.error`, the manual command, exit 6, worktree removed |
| 67 | `run --mr`, an open MR exists for the branch: reused, no POST |
| 68 | `run --mr`, wrong token: exit 2, no provider call, no run directory |
| 69 | `run --mr --jira`: comment and transition; a missing transition warns |
| 70 | `story run --mr`: two stacked MRs; the dependent blocked when the first has none |
| 71 | `--output json`: one line on stdout for success, failure, pre-run error |
| 72 | no terminal, no `--mode`: exit 2 at once |
| 73 | `review --base main`: diff is `main...HEAD`, dirty tree untouched, verdict fail → exit 6 |
| 74 | `review --mr 7 --comment`: fetch, review, one note |
| 75 | the GitLab token never in argv, env of a provider or test process, `run.json`, logs, report |
| 76 | branch from instructions: AGENTS.md names a format, the fake call proposes `fix/PAY-12-x`, the worktree is on it, the MR's source is it |
| 77 | branch from the template when the proposal is invalid (`main`, `../x`, an existing branch); a warning; `branch_naming.rejected` |
| 78 | `run --mr` whose branch already exists on the remote: no push, exit 6 |

## Gates

`scripts/check.sh`
