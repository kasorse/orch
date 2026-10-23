# DL05 — `story run --mr`

**Size** S (1.5 d) · **Depends on** DL04 · **Blocks** DL11

## Goal

Every story task gets its own MR, stacked on its dependency's branch (`SPEC.md` §9).

## Files

- `internal/app/story_run.go`: `--mr` accepted; the per-service `validate` becomes
  `validate --mr`; children get `--mr` (and `resume` children read it from their run); exit 6 from a
  child is "completed without an MR": the task is `completed`, the story stops before any task that
  depends on it (`blocked: <ID> has no merge request`), independent tasks continue.
- `internal/story` / `story.json`: `tasks.<ID>.mr {iid, url}` read from the child's `run.json`,
  validated; `story status` and the end table print it next to the target branch.
- The child's target is its run's `base_branch`, which the story already sets; assert that it equals
  the target `finish` prints.

## Acceptance

- Two tasks in one repository, B depends on A, fake GitLab: two MRs, B's target `story/<slug>/A`.
- A with a failing review (no commit → exit 6): B blocked, an independent C runs.
- `story run --mr` with a service lacking `gitlab`: stops before the first dispatch.

## Gates

`scripts/check.sh`; live check step 5 (operator).
