---
id: DL04
repo: orche
depends_on: [DL01, DL02, DL03]
jira: none
---
# DL04 — The end of a worktree run: commit, push, merge request

**Size** M

## Goal

A completed worktree run with `implement` commits, pushes its branch and opens an MR with the same
push; `--mode auto` without questions, every other mode with a question before each step
(`SPEC.md` §7).

## Files

- `internal/app/worktree.go` (`finishWorktree`): the commit is automatic only under `--mode auto`
  (the §42.6 rules); in any other mode the §42.2 question is asked with its warnings;
  `auto_commit` no longer applies to a worktree run.
- `internal/app/deliver.go`: `deliver(ctx, s, sha)` from the shared tail after the commit and the
  conflict check, before the worktree removal: the push and MR questions (non-auto; a no stops
  and records `skipped: declined at …`), the operator fingerprint, `git ls-remote --heads`
  (present → `mr.error`, exit 6), the intent, `Repository.Push(remote, branch, opts)` with the
  `merge_request.*` options (title from the commit header, the description `report.md` rendered
  after the commit with line breaks encoded as `\n`, re-rendered smaller until the option fits
  64 000 bytes), the MR URL parsed from the push's stderr (marker, the remote's host, `/-/merge_requests/<n>`
  exactly), the result recorded and printed (§7.3 table). Nothing changes the run's status.
- `internal/repository`: `Push(ctx, remote, branch string, options []string) (stderr string, err error)`
  — `git push -u --no-follow-tags <remote> refs/heads/B:refs/heads/B [-o …]`, argv, full
  environment, no force, 5 minutes; `LsRemoteHead`.
- `internal/domain/run.go`: `RunMR` (`mr`), strict validation (§13).
- `internal/exitcode`: code 6 `NeedsPerson`; `app` returns a typed error for "completed, no MR
  nobody declined" so `FromError` maps it without touching the run status.
- `internal/report/summary.go`: a "Merge request" row; `report.md`'s footer names the MR.
- `resume`: the same chain when the resumed run completes, from its own `--mode`.

## Acceptance

- Unit: the URL parser (the marker, a `…/merge_requests/new?…` link not matched, another host not
  matched, userinfo dropped); the `\n` encoding (CR dropped, LF/NUL never in an option); a report
  over 64 000 bytes encoded is cut to fit.
- Against a bare repository with `receive.advertisePushOptions true` and a `post-receive` hook that
  echoes GitLab's lines from `GIT_PUSH_OPTION_*`: auto → `state: created`, iid and url recorded,
  exit 0, the worktree removed; the options received equal the ones sent.
- Non-auto answers y/y/n → pushed without `-o`, `state: pushed`, exit 0; n at the commit → nothing
  pushed, worktree kept, exit 0.
- Auto with a failing review → no commit, no push, `mr.skipped`, exit 6, worktree kept.
- A bare repository without `advertisePushOptions` → push refused, `mr.error`, the manual command,
  exit 6, the intent kept, no second push.
- A branch of the same name already on the remote (even an ancestor) → not pushed, exit 6.
- Live check steps 1–3 and 5 of `SPEC.md` §15 are the operator's gate; record them in As built,
  with whether gitlab.com decoded `\n`.

## Gates

`scripts/check.sh`; the live check (operator).

## Notes

Do not edit anything under `specs/delivery/` (story run writes its README and As built). Leave
MVP.md, CLAUDE.md, TODO.md and README.md to DL12. The live check is the operator's gate: say in
the summary that it was left to the operator, and do not claim it was done.

## As built (2026-10-27)

Run `139f06cf-d12d-4fcf-9bde-6048e26682d1` in orche: completed, 0 fix iterations, tests passed, review pass.
Branch `story/delivery/DL04` on `story/delivery/spec (DL01 + DL02 + DL03)` @ c4e9802, commit 1c43c51.
Cost $2.15. Implementation summary: Commit, push and open a merge request at the end of a worktree run.
Conflicts with spec/delivery: none.
