# Parallel story: ready tasks at once, several parents merged on the spec branch

## Problem

`story run` runs one task at a time (MVP.md §48, §30), so a story of independent tasks takes the
sum of their run times. A task that needs two tasks of its own repository is a spec error
(`story.Base`: "make one depend on the other"), which forces a chain where the work is a tree.

The owner's model (2026-10-23): every ready task runs at once, each in its own worktree; its
branch is pushed and gets a merge request (delivery, `specs/delivery`); a task that depends on one
finished task is branched from it; a task that depends on two has them merged on the spec branch
first and gets a new worktree from there; a merge conflict stops the story for a manual merge.

## Approach

### Ground rules for the tasks of this story

- This is a story task. `story run` writes `specs/parallel-story/README.md` and each task's
  `## As built`: **do not edit anything under `specs/parallel-story/`**. CLAUDE.md's bookkeeping for
  `specs/*/tasks` (ticking, the `Status:` line, As built) is done by `story run` for these tasks.
- Only PS04 edits the shared documents (MVP.md, CLAUDE.md, TODO.md, README.md,
  orchestrator.example.yaml), so the task branches do not conflict on them.
- With one slot (`story.max_parallel: 1`, the default) `story run` behaves and prints exactly as
  today, apart from the spec branch (§1) and the `conflict` state (§3): the existing tests keep
  passing; change one only where §1 changes what it asserts, and say so in the commit.
- CLAUDE.md's invariants hold: Go orders and decides, no model picks what runs; `internal/story`
  stays pure (domain and the standard library only, `imports_test.go`); git is argv only.
- The gate is `scripts/check.sh` (root and `bench/`).

### 1. Bases: one parent is stacked, several are merged on the spec branch

- The **parents** of task T in repository R are the set `story.Base` computes today: T's
  ancestors in R (transitively, across repositories) minus those another of them depends on.
- No parent: R's story base commit (`bases.<repo>.sha`), as today. One parent: stacked on the
  parent's recorded commit, as today. Several: the parents' commits are merged on R's spec branch,
  and T starts from the spec branch's tip.
- The **spec branch** `story/<slug>/spec` is the story's own branch in each repository. Go
  creates it in every repository the story touches at the first start, at the story base commit,
  after the approval and before the Jira children and the first dispatch, and records
  `bases.<repo>.spec_branch`. A continued story whose record lacks it gets it at
  `bases.<repo>.sha` before anything is dispatched. Go never writes the operator's branch (the
  story base branch). `spec` is not a task ID, so the name cannot clash with
  `story/<slug>/<ID>`; the unknown-branch check knows it.
- **The merge** is made by the parent `story run`, in R's checkout, just before T is dispatched,
  never while another merge runs, the parents in ID order, each:
  1. the parent's commit is already an ancestor of the spec tip: nothing;
  2. the spec tip is an ancestor of the parent's commit: fast-forward with
     `git update-ref refs/heads/<spec> <new> <old>`;
  3. otherwise `git merge-tree --write-tree` (objects only: no checkout, no hooks). Clean:
     `git commit-tree <tree> -p <tip> -p <parent>` with the message
     `Merge <parent's branch> for <ID>` and the trailer `Orchestrator-Story: <slug>/<ID>`, then the
     `update-ref` above. A conflict: T becomes `conflict` (§3) and nothing is written.
- Before the first merge-tree in a repository in one `story run`, R's git fingerprint must equal
  the one the pre-run checks took: configured merge drivers run code (§47.5's rule).
- commit-tree uses the operator's git identity, as the children's commits do. Without one the
  story stops with git's message.
- T's `base_ref` is the spec tip after the merges; its `base_branch` stays R's story base branch.
  The As built and the plan name the base as `story/<slug>/spec (PS02 + PS03)`.
- The target a task's merge request takes (the end table, `finish`): with one parent, the
  parent's branch; with none or several, the spec branch.

### 2. Parallel dispatch

- **Slots**: `story.max_parallel` (1..8, default 1), overridden by `story run --parallel N`
  (1..8).
- **One goroutine owns the state.** The parent alone reads and writes `story.json`, the README,
  the task files and the console. Each child is a goroutine that only runs the process and sends
  `{id, exit}` on a channel. The loop fills the free slots with ready tasks in the story's order,
  waits for one child to end, settles it as today, and repeats. A task is **ready** when every
  `depends_on` task is done and it is pending (dispatched), failed or stopped with a run
  (resumed), blocked (looked at again) or conflict (merged again). §1's merges happen in the loop,
  just before the dispatch.
- **Two tasks of one repository may run at once**: each is its own worktree run on its own
  branch. No git call of the parent or a child writes the shared `.git/config` or the hooks while
  children run, because every child's guard fingerprints them (contract C3).
- **More than one slot**: a child gets no stdin (`/dev/null`; children run `--mode auto`). Its
  stdout and stderr go to `.orchestrator/stories/<slug>/logs/task-<ID>.<attempt>.log` (0600,
  appended when the run is resumed). The console gets one line when it starts,
  `▶ PS02 · orche · 2/4 · log <path>`, and today's result line when it ends.
- **A task that does not end done** (failed, stopped, blocked, conflict): nothing new is
  dispatched, the running children run to their end and are settled, then the story stops with
  the status of the first task in the story's order that is not done. The hints of every such
  task are printed.
- **Budget**: before each dispatch, the recorded total plus what every running child's run has
  cost so far (its `run.json` executions, found by `task.source` as settle finds it) must be under
  `story.max_cost_usd`; otherwise nothing new is dispatched, the running children are drained and
  the story stops. The overshoot is at most the steps in flight.
- **Ctrl-C**: every running child gets SIGINT (`runChild` does it per child), each is waited for
  and settled, and the story is stopped.
- **The start of a story run**: a task recorded as running whose run directory is locked by a
  live process stops the story run before anything else (`PS02: run <id> is still running`).
  Otherwise every running task is reconciled as today.
- settle and the budget lookup skip, without a warning, a run directory that another process
  holds locked: it is a sibling in progress.

### 3. The `conflict` state

- `conflict`: the task's parents do not merge cleanly on the spec branch. The task is not
  dispatched (no attempt, no run). `story.json` records `tasks.<ID>.status: conflict` and
  `conflict {parents, files}`, and the story is `blocked`.
- Printed: the conflicting files and the manual merge: a worktree of the spec branch, `git merge`
  of the parents' branches, commit, remove the worktree, then `orchestrator story run <dir>`
  again.
- The next run, before the dispatch: the spec tip must descend from `bases.<repo>.sha`, and if
  every parent's recorded commit is now an ancestor of the tip (the operator merged), T is
  dispatched from that tip. Otherwise §1's merge is tried again, and conflicts again.

### 4. Interaction with delivery (`specs/delivery`, not built)

- The merge request targets are §1's last bullet. The spec branch must be on the remote before a
  merge request targets it: delivery's story task (DL05) pushes it at the first start and after
  each merge, from the parent, after the approval.
- A story child's push must not use `-u`: it writes the shared git config, which a sibling's
  guard compares (C3).
- Neither is built here.

## Contracts

- `internal/story` (pure):
  - `Parents(tasks []Task, id string) ([]string, error)`: the parents, sorted; an error only for
    an unknown ID.
  - `Ready(tasks []Task, states map[string]TaskStatus) []string`: in `Order`, the tasks whose
    every `depends_on` is `TaskDone` and whose own state is neither done nor running.
  - `TaskConflict TaskStatus = "conflict"`; the README shows it like any other status.
  - `Base` and the check that refuses several parents go when PS02 switches its callers to
    `Parents`.
- `internal/config`: `story.max_parallel`, an integer 1..8, default 1, in the strict schema.
  CLI: `story run --parallel N`, 1..8, a usage error otherwise and with any other command.
- `internal/repository`, argv only, a ref that is empty or starts with `-` refused without running
  git (as `MergeConflicts` does):
  - `MergeTreeWrite(ctx, ours, theirs string) (tree string, conflicts []string, err error)`
  - `CommitTree(ctx, tree string, parents []string, message string) (string, error)`
  - `UpdateRef(ctx, ref, newSHA, oldSHA string) error` (oldSHA empty: the ref must not exist)
  - `IsAncestor(ctx, ancestor, descendant string) (bool, error)`
- `story.json`, all optional on load and strictly validated when present:
  - `bases.<repo>.spec_branch`: exactly `story/<slug>/spec`;
  - `tasks.<ID>.status` may be `conflict`, with `conflict {parents: [IDs], files: [at most 50
    clean repository-relative paths]}`, which is present only then;
  - a merged task's `base_ref` is a commit on the spec branch.
- `internal/logging`: `StoryTaskStarted(id, repo string, n, total int, log string)` and
  `StoryConflict(id string, parents, files []string, specBranch string, hints ...string)`, each
  with a plain and a pretty form like the other story lines.
- The log file: `.orchestrator/stories/<slug>/logs/task-<ID>.<attempt>.log`.
- **C3**: while children run, no git call writes `.git/config` or the hooks. `worktree add` and
  `remove`, `commit`, `branch`, `update-ref`, `commit-tree` and `merge-tree` do not.

## Out of scope

- Running a run's own stages in parallel; two `story run` on one story (the lock stays); more
  than 8 slots; a limit per repository.
- Resolving merge conflicts automatically; rebasing a stack; merging into the operator's branch;
  the push and the merge requests (delivery).
- Killing the siblings on a failure; starting independent tasks after a failure.
- Isolating test resources (ports, databases, files outside the worktree) between parallel tasks
  of one service: the operator chooses `max_parallel`.

## Risks

- The tests of one service run at once may share ports, databases or files outside the
  worktree. Keep one slot for such a service, or isolate its tests.
- A `git gc --auto` that a child's commit starts may hold `packed-refs.lock` while another call
  updates a ref. The failed step fails its run, and a resume continues it.
- Timing decides which merges land first on the spec branch. The recorded `base_ref` keeps every
  task reproducible on resume.
- Cost: N children spend at once. The budget check bounds the overshoot to the steps in flight.

## Decisions

Owner (2026-10-23):

| # | decision |
|---|---|
| D1 | Every ready task runs at once, each in its own worktree; then its branch is pushed and gets a merge request (delivery). |
| D2 | A task that depends on a finished task gets a new branch from it. |
| D3 | A task that depends on two has them merged on the spec branch, then a new worktree from it. |
| D4 | A merge conflict stops the story for a manual merge. |
| D5 | A task's branch starts from the spec branch or from the task it depends on. |
| D6 | The spec is implemented with `orchestrator story run specs/parallel-story`. |

Author's defaults (the owner may override any):

| # | default | why |
|---|---|---|
| A1 | The spec branch is `story/<slug>/spec`, created by Go, never the operator's branch | the operator's branch is checked out in their tree (the story base is what is checked out at the first start), and in another service it may be `main`: writing it would move their tree and skip review |
| A2 | Tasks of one repository may run at once, under C3 | D1 says every ready task; worktrees keep the trees apart, and C3 keeps the guards apart |
| A3 | One slot by default, at most 8 | today's behaviour stays the default; more children than that mostly wait on the provider |
| A4 | After a failure nothing new starts, and the running children finish | stops the spend as today's sequential stop does, without killing work in flight |
| A5 | With more than one slot, a log file per task and one console line per start and end | interleaved output of several children cannot be read |
| A6 | A task without parents starts from the story base commit, not the spec tip | otherwise its base would depend on which merges happened to land first |
| A7 | Several parents are merged by Go with merge-tree and commit-tree, never in a worktree | no checkout and no hooks run; a conflict is detected without touching any tree |
