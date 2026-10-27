---
id: DL11
repo: orche
depends_on: [DL01, DL02, DL03, DL04, DL05, DL06, DL07, DL08, DL13]
jira: none
---
# DL11 — Integration scenarios 64–77

**Size** M

## Goal

End-to-end scenarios with fake executables, a fake GitLab remote (`test/integration/gitlab_test.go`:
a bare repository with `receive.advertisePushOptions true` and a `post-receive` hook that records
`GIT_PUSH_OPTION_*` and prints GitLab's `View merge request` lines) and the fake Jira extended with the write
endpoints.

## Scenarios

| # | scenario |
|---|---|
| 64 | worktree by default, `--mode auto`: commit with type and trailers, push, MR options received (target, title, `report.md` with `\n`-encoded breaks), `mr.state: created`, worktree removed |
| 65 | auto, review fails: no commit, no push, `mr.skipped`, exit 6, worktree kept |
| 66 | the remote refuses push options (plain bare repository): `mr.error`, the manual command, exit 6, no second push |
| 67 | non-auto answers: n at the commit → nothing; y/n → committed, not pushed; y/y/n → pushed without options; all exit 0 |
| 68 | `--inplace`: §42 question, no push, no MR; no remote → exit 2 naming `--inplace` before the worktree, a workflow without implement still runs |
| 69 | auto with `--jira`: comment and transition after the MR; a missing transition warns |
| 70 | `story run`: two stacked MRs; the dependent blocked when the first has none; with a service `worktree.branch.template` the branches follow it and B's MR targets A's templated branch |
| 71 | `--output json`: one line on stdout for success, failure, pre-run error |
| 72 | no terminal, no `--mode`: exit 2 at once |
| 73 | `review --base main`: diff is `main...HEAD`, dirty tree untouched, verdict fail → exit 6 |
| 74 | the push gets the full environment; no GitLab token variable reaches a provider or test process |
| 75 | branch from instructions: AGENTS.md names a format, the fake call proposes `fix/PAY-12-x`, the worktree is on it, the pushed branch is it |
| 76 | branch from the template when the proposal is invalid (`main`, `../x`, an existing branch); a warning; `branch_naming.rejected` |
| 77 | a branch that already exists on the remote: no push, exit 6 |

## Gates

`scripts/check.sh`

## Notes

Do not edit anything under `specs/delivery/` (story run writes its README and As built). Leave
MVP.md, CLAUDE.md, TODO.md and README.md to DL12.
