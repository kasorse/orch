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
    branch, unknown `story/<slug>/*` branches, `orchestrator validate` as a child; budget);
    each task's `repo` found as in §7.1 (`candidates`, unambiguous) and compared with
    `story.json: services` when recorded - a hand-written spec gets `services` at its first start
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

## As built

2026-10-19. Written by run `be10ef23`, whose implement step hit `execution.timeout` on its last
README edit, before its test and review steps; `scripts/check.sh` was then run by hand and
passed. No review step ran.

- **`story.json`** (`internal/persistence/story.go`): `StoryRecord` gains `approved_at`, `bases`
  and `tasks`, all optional, so a plan-time record loads; the loader stays strict. `StoryTask`
  holds the §14 fields, validated by format (`isRunID`, `ValidBaseRef`, `ValidBranch` with the
  branch of that very attempt, `ValidJiraKey`); a dispatched task needs `approved_at`. Two
  decisions: `jira_intent` is a flag (ST07 may change its type), and the cost of the runs a
  `--retry` leaves behind is kept in `abandoned_cost_usd`, because `cost_usd` is the current run's
  and is *replaced* when that run is resumed (its outcome already sums the earlier attempts).
  `SaveStoryRecord` is atomic, `StoryDir` derives the directory from the home and a checked slug,
  `LockStory` is the non-blocking directory `flock` of `Store.Lock` (a held lock is a config error),
  `WriteStoryTaskInput` validates the input as the child will, `ReplaceFile` keeps a spec file's mode.
- **`internal/story`**: `ParseSpec` (title, Approach, Contracts), `AsBuiltText` and
  `RenderInputText` (the story's title and the task's place, its text, then as background the
  approach, the contracts and the done dependencies' As built; cut to 32 KiB background first,
  dependencies last to go, never the task itself, which is refused if it alone is too long).
- **`internal/repository`**: `HasCommit`, `BranchTip`, `Branches`, `CommitsBeyond`; every ref is
  checked (`safeBranchName`) and put after `refs/heads/`, every base is a full SHA.
- **`story run`** (`internal/app/story_run.go`), in the order of §10.1: the story's directory
  decides the home; the home's config chain; the lock; `story.json` (or a fresh record for a
  hand-written spec) and every task file; `story.Validate`, `CheckAcceptance`, `story.Base`;
  frozen hashes; per repository the pending tasks name (`findServices` on the one name, the
  recorded path, a commit, no `.gitmodules`, the base branch, no unknown `story/<slug>/*` branch -
  every attempt's branch of every recorded task is known - and `<self> validate` as a child under
  a `Checking <repo>` line); the budget; the Jira seam (`ensureJiraChildren`, which for now only
  refuses a task whose `jira_intent` has no key); the first start's plan and one `[Y/n]` read a
  byte at a time (skipped under `--mode auto` and a non-terminal stdin).
- **The loop**: running tasks are settled first (a crash between a child's end and the save), then
  `--retry` (pending again on attempt `n+1`, branch `<ID>.<n+1>`; its file is re-read), then the
  order. A blocked task is done once `CommitsBeyond(base_ref, branch) > 0`; a failed or stopped
  task with a run is resumed (`resume <id> --mode auto`); anything else is dispatched. The
  stacked `base_ref` is the dependency's *recorded* commit, not its branch's tip now (SPEC §9.2).
  The child's run is looked up by story, task and attempt, newest first (or by the recorded ID),
  never by "the newest run"; none found is `failed` with the exit code (`stopped` after a Ctrl-C).
  A run the operator's Ctrl-C failed mid-call is `failed` in `run.json`, but the task is
  `stopped`. Children are `os.Executable()` with symlinks resolved, argv only, the terminal's
  stdio; cancelling sends SIGINT (`cmd.Cancel`), with a 30 s `WaitDelay` before a kill.
- **After a done task**: As built into the file (re-read first; its frozen hash must be what was
  dispatched and is unchanged by the append), then `story.json`, then `README.md`. A story with
  all tasks done prints the table of §10.4 and is `completed`.
- **`story status`** reads `story.json` and the task files and prints the README table; no
  config, no lock, no write (asserted by file bytes and mtimes in the integration test).
- **Arguments** (`app.go`): `story run <dir> [--retry ID]`, `story status <dir>`; `--retry` is
  valid with `story run` alone and takes a task ID; `--claude-auth` is passed to the children;
  the default config path is looked for in the story home.
- **Integration** (`test/integration/story_run_test.go`): `TestMain` answers as the orchestrator
  when `ORCHE_TEST_ORCHESTRATOR=1`, so the children are the test binary. Two service repositories
  (billing, gateway), a home with a hand-written spec of four tasks; one scenario per Acceptance
  item, plus the budget and the refusals of the first start. "Operator checkouts byte-for-byte"
  is asserted as HEAD, the checked-out branch, the index and every tracked file (the home's spec
  directory excepted), not raw `.git` bytes: the children add refs, branches and worktree
  metadata there by design.
- **Not done here**: the Jira children (ST07); `jira_intent` is in the schema and the seam is in
  place. Pushing and merge requests stay TODO W2.
- Tests: `internal/persistence/story_test.go`, `internal/story/{spec,input}_test.go`,
  `internal/repository/git_test.go`, `internal/logging/story_test.go`,
  `internal/app/{args,story_run}_test.go`, `test/integration/story_run_test.go`.
