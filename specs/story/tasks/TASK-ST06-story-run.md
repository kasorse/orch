# ST06 — `story run` and `story status`

**Size** L (4 d) · **Depends on** ST01, ST02, ST05, WT07 · **Blocks** ST07, ST10

## Goal

The driver of `SPEC.md` §10: validate, approve, dispatch in order, follow each child, stop on
anything but a committed task, continue later.

## Files

- `internal/persistence/story.go`: `story.json` load/save with strict validation (§14); the
  story directory derived from the spec directory's repository; `flock`
- `internal/app/story_run.go`:
  - pre-checks of §10.1, in order, before anything runs (the home's chain; parse and validate;
    frozen hashes of dispatched tasks; per repository: checkout, commit, no `.gitmodules`, base
    branch, unknown `story/<slug>/*` branches, `orchestrator validate` as a child; budget)
  - first start: `bases`, `approved_at`, the plan printed, one confirmation at a terminal (A11)
  - the loop: pending → write `tasks/<ID>.json` (§11.1 text rendered from the task, the spec's
    approach and contracts, the done dependencies' As built lines, bounded), freeze, spawn
    `os.Executable() run --mode auto --story-task …` in the repository with the terminal's
    stdio; find the run by `task.source`; done / blocked / failed / stopped as in §10.2;
    failed → `resume <id> --mode auto`; `--retry <ID>` → attempt `n+1` on `<branch>.<n>`
  - after done: `story.AppendAsBuilt` and README rewritten (§10.3); cost added; the budget
    checked before the next dispatch
  - the end (§10.4): the table of branches and merge request targets
- `internal/app/story_status.go`: `story status <dir>` — README's table on the terminal, from
  `story.json`, read-only
- `internal/app/app.go`: the subcommands and usage

## Acceptance

- Integration (fake executables, two repositories): order and stacking (a second billing task
  based on the first's commit; a gateway task on gateway's base); As built appended and README
  ticked; a failing child stops the story and the next `story run` resumes that run to
  completion; a blocked task counts as done once its branch has a commit; `--retry` dispatches
  `RF02.2` and dependents stack on it; an edit to a dispatched task refused; a repository whose
  `orchestrator validate` fails stops everything before the first dispatch; a second
  `story run` on the same story while one runs is refused by the lock; Ctrl-C (context
  cancel) leaves the task `stopped`; every operator checkout byte-for-byte unchanged.

## Gates

`scripts/check.sh`
