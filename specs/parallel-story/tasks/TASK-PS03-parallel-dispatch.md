---
id: PS03
repo: orche
depends_on: [PS02]
jira: none
---
# PS03 — Parallel dispatch: slots, one owner of the state, logs, drain, budget, cancel

**Size** M

## Goal

`story run` runs up to N ready tasks at once (`story.max_parallel`, `story run --parallel N`),
each as today's child process in its own worktree. Tasks of one repository may run together.
With one slot nothing changes (SPEC.md §2).

## Files

- `internal/config/story.go` and the schema: `story.max_parallel`, an integer 1..8, default 1;
  out of range is a config error.
- `internal/app` (the arguments, `storyRunOptions`): `story run --parallel N`, 1..8. A value out
  of range, or the flag on any other command, is a usage error. It overrides the config.
- `internal/app/story_run.go`:
  - Replace the sequential walk of `dispatchAll` with a loop that owns all state (`story.json`,
    the README, the task files, the console). Each child is a goroutine that only runs
    `runChild` and sends `{id, exit}` on a channel. The loop fills the free slots with
    `story.Ready` in the story's order (merging first as PS02 does, never two merges at once),
    waits for one child, settles it with today's `settle`, and repeats until nothing is running
    and nothing is ready.
  - One slot: the same calls, order and output as today, including the child's direct stdio and
    the `── PS02 · orche · 2/4 ──` header.
  - More than one slot: the child's stdin is `/dev/null`, and its stdout and stderr go to
    `.orchestrator/stories/<slug>/logs/task-<ID>.<attempt>.log` (0600, opened for append). The
    console prints `StoryTaskStarted` when the child starts and today's result line when it ends.
  - A task that ends not done (failed, stopped, blocked, conflict): nothing new is started, the
    running children are waited for and settled, then `stop` runs for the first not-done task in
    the story's order, and the hints of every not-done task are printed.
  - Budget: before each dispatch or resume, the recorded total plus the cost so far of every
    running child's run (its `run.json` executions, found by `task.source` as `pickStoryRun`
    does) must be under `story.max_cost_usd`. Otherwise the loop drains and stops, as above.
  - Ctrl-C: the context reaches every `runChild`, which sends SIGINT. The loop waits for all of
    them and settles each; the story is stopped.
  - The start: a task recorded as running whose run directory is held by another process's lock
    stops the story run before the checks (`PS02: run <id> is still running`). `reconcile` then
    settles every running task as today.
  - `settle` and the budget lookup skip a run directory that another process holds locked,
    without a warning.
- `internal/logging/story.go`: `StoryTaskStarted(id, repo string, n, total int, log string)`,
  plain and pretty.

## Acceptance

- One slot: every existing story test passes unchanged.
- Two slots, two independent tasks: both children are running at the same time. Prove it with
  fake children that each wait, with a timeout, for a file the other writes when it starts.
- The two-slot test runs once with the two tasks in two repositories and once with both in one
  repository: both commit on their own branches, and the guard reports nothing.
- Two slots, A fails while B runs: B runs to its end and is settled, no third task starts, the
  story is `failed`, and A's hints are printed.
- The budget reached while one child runs: nothing new starts, and the story stops after it.
- Cancel with two children running: both get SIGINT, both are settled as stopped, and the story
  is stopped.
- A running task whose run directory is locked by another process: the story run stops before
  the checks.
- With two slots the log files exist with each child's output, and the console carries only the
  start and result lines.
- `--parallel 0`, `--parallel 9`, and `--parallel` on `story plan` are usage errors;
  `story.max_parallel: 9` is a config error.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/parallel-story/` (story run writes its README and As built).
Leave MVP.md, CLAUDE.md, TODO.md, README.md and orchestrator.example.yaml to PS04.

## As built (2026-10-24)

Run `33eb929e-5b1c-4c26-a4a0-4838362918c9` in orche: completed, 1 fix iteration, tests passed, review pass.
Branch `story/parallel-story/PS03` on `story/parallel-story/PS02` @ 3fab419, commit b14c16d.
Cost $6.04. Implementation summary: Run ready story tasks in parallel slots with one owner of the state.
Conflicts with spec/parallel-story: none.
