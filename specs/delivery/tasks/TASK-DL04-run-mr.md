# DL04 — `run --mr`

**Size** M (3 d) · **Depends on** DL02, DL03 · **Blocks** DL05, DL06, DL07, DL11

## Goal

A completed worktree run pushes its branch and opens (or finds) one draft MR whose description is
`report.md` (`SPEC.md` §7, §8).

## Files

- `internal/app/app.go`: `--mr` parsed; valid with `run` (and `story run`, `review`); with
  `--inplace` a usage error; implies `--worktree`; with `--stage` allowed. Fail-fast: `GitLab: true`
  dependencies, `secret_scan.mode` not `off` (A3), a target branch known (base branch or
  `gitlab.target_branch`) — checked after HEAD is known and before any call — then `Project`.
- `internal/domain/run.go`: `options.mr`, `RunMR` (`mr`), strict validation (§13).
- `internal/app/deliver.go`: `deliver(ctx, s, sha)` called from the shared tail after
  `finishWorktree`'s commit and conflict check and before the worktree removal: the skip reasons
  (no commit / no implement), the operator fingerprint, the branch absent on the remote (`git ls-remote --heads`; present →
  `mr.error`, exit 6), `Repository.Push(remote, branch)` (no
  force, explicit refspec, 5-minute context, full env), the intent, `OpenMR`, `CreateMR` (title
  from the commit header, `Draft: `, `report.md` rendered after the commit, labels), the result
  recorded and printed. Nothing changes the run's status.
- `internal/repository`: `Push` and `RemoteURL` (`git remote get-url`).
- `internal/exitcode`: code 6 `NeedsPerson`; `app` returns a typed error for "completed, no MR"
  so `FromError` maps it without touching the run status.
- `internal/report/summary.go`: a "Merge request" row.
- `resume`: reads `options.mr`; builds the GitLab client only when the resumed run completes.

## Acceptance

- Unit: the skip reasons; the target branch rule; a push refused by the remote → `mr.error`, the
  manual command printed, exit 6; an open MR found → `state: found`, no POST; a create timeout →
  intent kept, `MayExist`, exit 6, no second POST.
- The worktree is removed after delivery as before; the operator's branch is never pushed.
- A branch of the same name already on the remote (even an ancestor of the commit) → not pushed,
  `mr.error`, exit 6.
- `--mr --inplace` → exit 2; `--mr` with `secret_scan.mode: off` → exit 2; wrong token → exit 2
  before any decision call (the fake provider records no call).
- Live check steps 1–3 of `SPEC.md` §15 are the operator's gate; record them in As built.

## Gates

`scripts/check.sh`; the live check (operator).
