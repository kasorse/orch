---
id: DP01
repo: orche
depends_on: []
jira: none
---
# DP01 — delivery.push: false in run: the chain ends at the commit

**Size** S

## Goal

SPEC.md §1, §2, §3 (for `run` and `orchestrator validate`) and §5: the key, the recorded option,
the end of a worktree run without a push, exit 0. `story run` is DP02.

## Files

- `internal/config`: `Delivery.Push *bool` in the strict schema, `PushEnabled()`; `validate`
  skips the remote check when it is false.
- `internal/domain`: `RunOptions.Push *bool` with `Pushes()`, its load validation;
  `MRSkippedPushDisabled`.
- `internal/app`: the option recorded at run start (next to `jira_write`); the pre-worktree
  remote check skipped when the run does not push; `finishWorktree`/`deliver`: with
  `!run.Options.Pushes()`, record `mr.skipped`, print the SPEC §2.5 line, ask nothing, return nil
  (exit 0); the worktree removed as after any commit; `--output json` fields as SPEC §2.6.
- `internal/report`/summary: the Merge request row of SPEC §2.7.
- `internal/logging`: the console line, in the reporter like the other delivery lines.
- Tests: unit tests next to the existing delivery tests, and one integration scenario with
  `--mode auto` and a repository whose remote rejects every push.

## Acceptance

- With `delivery.push: false` a completed `--mode auto` worktree run commits, removes the worktree, runs no `git push` and no `git ls-remote`, and exits 0.
- run.json has `options.push: false` and `mr.skipped` "push disabled (delivery.push: false)"; `--output json` has `mr: null`, that `mr_skipped`, the branch and the commit.
- In manual mode the run asks the commit question and neither the push nor the merge-request question.
- A repository with no remote runs and validates with `push: false`; with the key absent it is refused as today.
- A resumed run follows `options.push` from run.json even when the config changed meanwhile.
- With the key absent or true, every existing test passes unchanged.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/delivery-push/`, nor MVP.md, CLAUDE.md, TODO.md, README.md,
`orchestrator.example.yaml` (DP03).
