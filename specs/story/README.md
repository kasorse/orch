# Story — a task split into a specification and ordered subtasks

Status: **in progress** (specified 2026-10-17; 2026-10-19): ST01 done (`internal/story`), ST02
done (`story` and `jira.create` config, the service finder), ST03 done (the survey and specify
calls, strict answers), ST04 done (`orchestrator story plan`: the spec on disk, the one-task
case, `.orchestrator/stories/`, `story.json`); the rest not started. Owner's
decisions D1–D7 and author's defaults A1–A11 in `SPEC.md` §19–§20. Builds on
[`specs/worktree/`](../worktree/README.md), which comes first.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 10 implementation tasks |

## The problem in one paragraph

Some tasks are not one change: they span several services, or are safe only as a sequence of
steps that each leave the system working. One run cannot span repositories, grows past what
review reads well, and ends as one diff. The task names the services it touches; `orchestrator
story plan` finds their checkouts under `story.roots` (no model call), surveys each of them with
a read-only call, asks a tool-less call for a specification and tasks with dependencies, checks
the answer and writes `specs/<slug>/` in this repository's own format. The operator reads and
edits it. `orchestrator story run` is the approval: Go orders the tasks from their declared
dependencies, optionally creates the child issues in Jira, and runs each task as an ordinary
`orchestrator run` in its repository, in a worktree on `story/<slug>/<ID>`, stacked on the branch
of the task it needs. Each task gets its own decision and stages, its acceptance items become
review criteria, and Go appends an "As built" record when it is done. A failure stops the story;
the next `story run` continues it. A shadow classification question records how story-like
every ordinary run looks, for a later calibration, and decides nothing.

## Tasks

| task | title | size | depends on |
|---|---|---|---|
| [ST01](tasks/TASK-ST01-model.md) | `internal/story`: task files, graph, order, bases, README rendering ✓ | M | — |
| [ST02](tasks/TASK-ST02-config.md) | config: `story`, `jira.create`; finding the services the task names ✓ | S | ST01 |
| [ST03](tasks/TASK-ST03-survey-specify.md) | survey and specify: prompts, calls, strict answers, logs ✓ | M–L | ST02 |
| [ST04](tasks/TASK-ST04-story-plan.md) | `story plan`: writing the spec, the one-task case, `.plan-<id>` ✓ | M | ST01, ST03 |
| [ST05](tasks/TASK-ST05-story-task-run.md) | `run --story-task`: input, base and branch, development on, criteria, commit message | M | ST01, WT04, WT06 |
| [ST06](tasks/TASK-ST06-story-run.md) | `story run` / `status`: state, lock, pre-checks, dispatch, resume, retry, As built, budget | L | ST01, ST02, ST05, WT07 |
| [ST07](tasks/TASK-ST07-jira-children.md) | Jira children: create, wiki rendering, intents, front matter write-back | M | ST06 |
| [ST08](tasks/TASK-ST08-jira-story-type.md) | `run --jira` refuses a story issue type | S | ST02 |
| [ST09](tasks/TASK-ST09-scope-question.md) | the scope question (shadow) | M | — |
| [ST10](tasks/TASK-ST10-integration-docs.md) | scenarios, MVP.md §48, CLAUDE.md, README, example, TODO.md | M | all |

```
ST01 ─┬─ ST02 ─┬─ ST03 ─ ST04 ──────┐
      │        ├─ ST08 ─────────────┤
      │        └──────┐             │
      └─ ST05 ─────── ST06 ─ ST07 ──┼─ ST10
         (ST05 also needs WT04, WT06; ST06 needs WT07)
ST09 ───────────────────────────────┘
```

Estimate: 20–24 engineering days, after `specs/worktree/`. ST01, ST02 and ST09 can start before
the worktree work is done; ST05 and ST06 cannot.

## Ground rules for every task

- **A model proposes, Go orders, the operator approves.** No model output decides what runs
  next, in which repository, on which branch, or with which stages.
- **A story task is an ordinary run.** Anything a story needs from `run` goes through
  `--story-task` and is recorded in `run.json`, so a resume of the child needs no story.
- **The operator's checkouts are never touched.** Every task runs in a worktree.
- **Nothing outside the machine is written before approval, and nothing twice** (Jira).
- **Paths are derived, never read.** `story.json`, the task files and the child's input are
  validated strictly; a task file's front matter has a closed set of keys.
- `internal/story` imports only `domain` and the standard library (the front matter has a closed
  format and is parsed by hand); `internal/workflow` and `internal/policy` import only `domain`;
  `internal/jira` keeps its import rule (`domain`, `apperr`, the standard library).
- Any wording change in `internal/decision/questions.go` changes `questions_hash` and needs a
  recalibration note.
- Every new config key is in the strict schema; every new `run.json`, `decision.json` and
  `story.json` field is validated on load.
- Tests need no network and no real CLI.
- Each task ends green on `scripts/check.sh` (root and `bench/`). In an orchestrator run that is
  the run's `test` step; `scripts/check.sh` runs once outside the sandbox before the branch merges.
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add an
  `## As built` section to the task file with the date and anything built differently, and why.
  In an orchestrator run, do this in the same change as the code; the run commits only after its
  test step passes.
