# Spec: Story — a task split into a specification and ordered subtasks

Status: **specified** (2026-10-17), in progress (ST01 done, see `README.md`). The owner's decisions are in §19 (taken
2026-10-17 while scoping), the author's defaults in §20. Depends on `specs/worktree/` (WT04,
WT06, WT07). Once ST10 is done, MVP.md §48 becomes the normative home and this file stays the
design record.

The spec touches these MVP.md sections:
- §8–§9: one more classification question, recorded and never used (§13);
- §30: out of scope (Jira writes for an approved story; a story is not a workflow language);
- §39: Jira (a story issue type, child issues);
- §42 and §47: the commit on a story task's branch;
- new §48: the story.

---

## 1. Problem

Some tasks are not one change. A feature that spans two services, or a change that is safe only
as a sequence of steps that each leave the system working, does not fit one run:

1. **One run cannot span repositories.** A run works in the repository it was started in.
2. **One large run is the weakest run.** The context fills up, the diff grows past what review
   reads well (`limits.prompt`), and a fix iteration has to reason about everything at once.
3. **The result cannot be reviewed in pieces.** One diff becomes one merge request.
4. **The operator already works around it by hand.** This repository's own `specs/` are exactly
   that: a specification, tasks with dependencies, each task done as its own change, and an
   "As built" record per task.

## 2. What already exists

- **A run is a complete, guarded unit**: decision, workflow, gates, guard, secret scan, resume,
  summary, outcome. A story task needs nothing else.
- **A task can come from outside the operator's keyboard.** `run --jira` renders a ticket into
  `Task.Description`; no stage learns where the text came from (§39). A story task is the same.
- **The plan's acceptance criteria are checked by review** (§44.4).
- **The pre-run test refuses a red suite** (§41.5): a task can rely on the previous one having
  left the tests green.
- **Worktree runs** (`specs/worktree/`): a run on its own branch from a given commit, committed
  at the end without a question (O2 there), the operator's checkouts untouched, resumable in its
  worktree. Its §15 left two fields for this spec: the base commit and the branch name.
- **The Jira client** reads one issue with strict validation, no redirects, env-only credentials
  and redaction (`internal/jira`), and already reads `issuetype`.
- **The shared decision questions** are a generic map: Jev takes yes/no questions as `noul`, the
  LLM provider asks the same text (`internal/decision/questions.go`).

## 3. Principles

1. **A model proposes; Go validates and orders; the operator approves.** The decomposition is a
   proposal written to files. Nothing runs until the operator runs `story run` on them. Go
   computes the order from the declared dependencies; no model ever picks what runs next.
2. **A story task is an ordinary run.** It goes through the same decision, policy, workflow,
   gates and resume. The story adds only what the run cannot know: the task text, the base
   commit, the branch, and the review criteria from the spec.
3. **Stories do not nest.** A story task is never a story.
4. **A spec is a list of tasks, not a workflow language** (§30). It names what to change and in
   which repository, never stages, models or tools; each task's workflow is decided by the
   policy as for any run.
5. **Sequential.** One task at a time (§30: no parallel execution).
6. **Writes outside the machine happen only after approval and never twice.** Jira children are
   created when `story run` starts, with a recorded intent before each request.
7. **Frozen once dispatched.** A task's text is fixed when its run starts; the record of what was
   done is appended by Go, never by a model.

## 4. How comparable tools do it (summary)

From the author's knowledge, not a fresh survey:

- **GitHub Spec Kit**: specify → plan → tasks → implement, each phase a document a person
  reviews before the next; tasks may be marked as parallelizable.
- **AWS Kiro**: `requirements.md`, `design.md`, `tasks.md`; each task links to the requirements
  it satisfies and is run one at a time.
- **Task Master**: a `tasks.json` with dependencies; a complexity analysis recommends which tasks
  to expand into subtasks.
- **Stacked diffs** (Graphite, Sapling, `git rebase --update-refs`): one small branch per change,
  each based on the one it needs, reviewed and merged in order.

What this spec takes: a reviewed spec before any code; tasks that are vertical slices, each
leaving the tests green; dependencies declared per task; a branch per task, stacked on the branch
it needs. What it does not take: a model choosing the order, or running tasks in parallel.

## 5. Vocabulary and configuration

- **Story home**: the repository where `story plan` / `story run` are invoked. It holds the spec
  (`<spec_dir>/<slug>/`) and the story state (`.orchestrator/stories/<slug>/`). It may also be
  one of the story's repositories.
- **Service**: a local git checkout directly under one of `story.roots`; its name is the
  directory's name. The story home is a service too, under its own directory name.
- **The story's services**: the ones its task names (§7.1). Only they are surveyed, and only they
  can get tasks.

```yaml
story:
  roots: [..]                     # where the service checkouts live, side by side
  max_services: 5                 # services one story may touch; 1..20, default 5
  spec_dir: specs                 # in the story home; default specs
  max_tasks: 12                   # 2..30; default 12
  max_cost_usd: 20                # optional; the whole story, every call and run
  jira_issue_types: [Epic]        # run --jira on these refuses (§12.1); default none
jira:
  create:                         # absent: story run creates nothing in Jira (§12)
    issue_type: Story             # the children's type
    link: parent                  # parent | field
    # field: customfield_10014    # with link: field (Epic Link on Server/Data Center)
    labels: [orchestrator]
    components: {billing: Billing}  # optional, by service name
```

- A root is absolute, `~/`-prefixed, or relative to the story home's root (`..` for checkouts
  side by side, which a team can commit). Nothing is listed per service: a new checkout under a
  root is a service at once.
- A service's story base is the branch checked out in it when the story first starts (§9.2);
  switch it there first if it should be another.
- Each service keeps **its own** `orchestrator.yaml`: its test command, checks, policy, routes
  and limits apply to its tasks. The story home's config contributes only `story` and `jira`.
- Every key is in the strict schema; `jira.create` without the `jira` section, `link: field`
  without `field`, are config errors. A `components` key that names no service is reported when
  a story uses `jira.create`, not at load (services are found, not listed).

## 6. The flow

```
orchestrator story plan "<request>" [--jira KEY] [--repo NAME]...
  fail-fast: home config
  [--jira] one GET of the issue (§39), rendered as the request
  services: the ones the task names, found under story.roots         (§7.1)
            (--repo names them instead); none, or too many → stop, no call made
  survey: one read-only call per service of the story, in it          (§7.2)
  specify: one call with no tools, from the request and the surveys   (§7.3)
  Go validates the answer; one task → print it as a run, write no spec (§7.4)
  Go writes <spec_dir>/<slug>/ and stops                              (§8)

  … the operator reads, edits, commits the spec …

orchestrator story run <spec_dir>/<slug>
  fail-fast: home config; parse and validate the spec; every repository it names
             (orchestrator validate there, git state, branches)     (§10.1)
  first start: record bases and the approval snapshot; print the plan; confirm once
  [jira.create] create the missing child issues                       (§12)
  for each task in Go's order (§9):
    done → skip; failed → resume its run; pending → dispatch a run    (§10.2)
    a child `orchestrator run` in the task's repository, in a worktree
    on story/<slug>/<ID>, based on the commit §9.2 gives
    completed with a commit → done: As built appended, README updated
    anything else → the story stops
  every task done → the story is completed; print branches and MR targets

orchestrator story status <spec_dir>/<slug>
```

`story run` both starts and continues a story: what is done stays done.

## 7. Planning calls

Both are calls outside the workflow, like the translator and the decision calls: fixed in Go,
with a fixed authority, recorded under the story's directory (`logs/`, `artifacts/`). Neither
is a workflow action and neither appears in `domain.AuthorityFor`'s table.

### 7.1 Finding the services

Reading every checkout next to the story home would cost a call each and mostly read
unrelated code. The task is expected to name the services it touches - typed (`… в сервисах
billing и gateway`), in the Jira ticket's text, or as the ticket's components - and Go finds
them, with no model call:

1. **Candidates.** Every immediate subdirectory of every root that has a `.git` entry and a name
   matching `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, plus the story home (once, by resolved path, if
   it is also under a root). Read with `os.ReadDir` and
   `os.Lstat`; no git runs over directories that are not used.
2. **Mentions.** A candidate is named when its name occurs in the request (the typed text, or
   the rendered ticket with the operator's notes) as a whole word, case-insensitively: not next
   to a letter (any script), a digit, `_` or `-`. A Jira component whose name equals a candidate's
   (case-insensitively) names it too; this needs the ticket's `components` (§39: one more field of
   the same GET).
3. **`--repo NAME`** (repeatable) names the services instead: exactly those, nothing detected.
   Each must be a candidate.
4. **Checks**, before any call: none named → `the task names no service under story.roots
   (<roots>); name them in the task, e.g. "Сервисы: billing, gateway", or pass --repo`; more than
   `story.max_services` → the list and `narrow it with --repo`; a named service found under two
   roots → both paths and `remove the ambiguity in story.roots` (no root wins silently); a named
   service whose `git rev-parse --show-toplevel` is not that directory → refused.
5. **Shown and recorded.** `Services: billing (~/src/billing), gateway (~/src/gateway)`, before
   the first call; `story.json: services` {name: path}.

A short directory name that is also a word (`api`, `web`, `docs`) can be named by accident; the
printed list shows it, and `--repo` corrects it. The cost of the mistake is one survey call.

### 7.2 Survey

- One call per service of the story (§7.1), with **`read` authority**, working directory = that
  service's checkout, in place (it
  changes nothing, like a workflow without `implement`), with that repository's deny list
  (`projectctx.DenyPatterns` and its runs directory).
- Route: that repository's `actions.analyze` base route (provider, model, effort, timeout) -
  static, no new routing keys.
- Prompt: the request (`taskBlock`), the repository's name, and the instruction to describe what
  in this repository relates to the request. Answer, strictly validated and bounded:

```json
{"summary": "…", "relevant_files": ["internal/refund/service.go"],
 "interfaces": ["POST /v1/refunds (api/openapi.yaml)", "event refund.created (events/refund.proto)"],
 "constraints": ["…"]}
```

  At most 4 KiB of summary, 30 files, 20 interfaces, 20 constraints; paths are clean and
  relative (displayed only, never opened by Go).

### 7.3 Specify

- One call with **`none` authority** (no tools), on the story home's `actions.plan` base route.
- Input: the request (`taskBlock`), each survey (`fence`), the story's services as the closed
  list of repository names,
  `story.max_tasks`, and the rules a task must follow:
  - a vertical slice that one run can implement, test and review (size S or M; an L is split);
  - after it, that repository's tests pass;
  - it changes exactly one repository;
  - it lists what it needs in `depends_on`; within one repository the tasks a task needs form a
    chain (§9.2);
  - work it finds necessary in a service the task did not name goes into `out_of_scope` or
    `risks`, never into a task;
  - contracts between repositories (an API, an event, a schema) are written in the spec, and the
    task that provides one comes before the tasks that consume it;
  - the spec and the tasks are written in the language of the request; IDs and keys are ASCII.
- Answer, strictly validated:

```json
{"slug": "refunds", "prefix": "RF", "title": "Partial refunds",
 "spec": {"problem": "…", "approach": "…", "contracts": "…", "out_of_scope": "…", "risks": "…"},
 "tasks": [
   {"id": "RF01", "repo": "billing", "title": "…", "size": "M", "depends_on": [],
    "goal": "…", "acceptance": ["…"], "notes": "…"}
 ]}
```

### 7.4 Validation of the answer

- `slug` `^[a-z0-9][a-z0-9-]{1,39}$` and `<spec_dir>/<slug>` must not exist; `prefix`
  `^[A-Z]{2}$`; task IDs `<prefix><NN>`, unique; `repo` one of the story's services; `size` S, M or L;
  `depends_on` names existing tasks; at most `story.max_tasks` tasks; at most 10 acceptance
  items of 300 runes; each text field bounded (8 KiB for a task, 16 KiB for the spec).
- The graph rules of §9 (acyclic, the same-repository chain).
- Any violation is an `InvalidResponse`: the plan fails, nothing is written under `spec_dir`,
  and the answer is kept in the story's `artifacts/` for the operator.
- **One task means it is not a story**: nothing is written under `spec_dir`; the orchestrator
  prints the task and the command that runs it (`cd <repo> && orchestrator run "<title>"`).

## 8. The spec on disk

Go renders it; the format is this repository's own:

```
<spec_dir>/refunds/
  README.md                      generated: title, status, the task table, how to run
  SPEC.md                        problem, approach, contracts, out of scope, risks
  tasks/TASK-RF01-<title-slug>.md
  tasks/TASK-RF02-…
```

A task file:

```markdown
---
id: RF02
repo: gateway
depends_on: [RF01]
jira: PAY-124
---
# RF02 — Expose partial refunds in the public API

**Size** M · **Repository** gateway · **Depends on** RF01

## Goal
…
## Acceptance
- …
## Notes
…
```

- **The front matter is the machine part.** Keys `id`, `repo`, `depends_on`, `jira` (written by
  Go after creation, or `none` by the operator); anything else is an error. The file name must
  carry the ID.
- **The body is the task's text**, as the operator left it: every edit before dispatch counts.
  `## Acceptance` list items are the task's acceptance criteria (§11.2). The task's own text is
  bounded to 16 KiB.
- **`## As built`** is appended by Go when the task is done (§10.3); everything from that heading
  on is Go's.
- **README.md is generated** from the task files and the story state, and rewritten whenever a
  status changes. Edits to it are lost; the task files are the source.
- A spec written by hand runs too, given the front matter: `story run` needs no plan.

## 9. Order, bases, branches

### 9.1 Order

Kahn's algorithm over `depends_on`, the smallest ID first among the ready tasks. A cycle, a
dependency on a missing task, or a `repo` that is not a service (a candidate of §7.1) is a spec
error naming the tasks. The order is a pure function of the files. A task the operator adds by
hand may name any service, not only the ones the plan found.

### 9.2 Base of a task

For task T in repository R, take T's ancestors (transitively, across repositories) that are in R,
and keep the ones no other such ancestor depends on:

- **none** → T is based on R's story base: the commit of the branch checked out in R at the
  story's first start, recorded with the branch's name in `story.json: bases.R`;
- **one** (A) → T is based on A's commit: its branch tip, stacked. The commit is the one
  recorded in `story.json` when A was done (the commit As built names), not the branch's tip
  at T's dispatch: a commit an operator added to A's branch afterwards is not in T's base, and
  the operator rebases the stack with git (§9.3);
- **more than one** → a spec error: `RF05 needs RF02 and RF03 in billing on different branches;
  make one depend on the other`. Git could merge them; deciding how is not the orchestrator's
  job.

A dependency in another repository gives order and context (§11.1), not code.

### 9.3 Branches

`story/<slug>/<ID>`; a retry (§10.2) adds `.<n>`. Each task's merge request targets its base's
branch: the dependency's branch for a stacked task, the story base branch otherwise. When a
reviewer changes a lower branch, the operator rebases the stack with git; the orchestrator never
does.

## 10. Running a story

### 10.1 Before the first task

`story run <dir>` resolves the directory to its story home (`git rev-parse --show-toplevel`),
whose `.orchestrator/stories/<slug>/` holds the state; the directory decides the state, not the
working directory. Then, before anything runs:

1. The home config's fail-fast chain (with the Jira dependencies when `jira.create` is set).
2. Parse every task file; validate §8 and §9. On a continued story: each dispatched task's file
   (up to `## As built`) matches its recorded sha256 - a change is refused, naming the task.
3. For every service the pending tasks name, found as in §7.1 (a candidate, unambiguous; its
   path compared with `story.json: services` when recorded): the path is a git checkout with a
   commit and no
   `.gitmodules` (worktree runs do not cover submodules, `specs/worktree/` §16); the story base
   branch exists; no `story/<slug>/<ID>` branch exists that `story.json` does not know; and
   `orchestrator validate` exits 0 there (that repository's whole fail-fast chain: its config,
   provider CLIs, bwrap probe).
4. `story.max_cost_usd`: the cost recorded so far is below it.

On the first start it records `bases` and prints the plan: order, repository, base, branch,
Jira key. At a terminal it asks once, `Run 6 tasks in 2 repositories? [Y/n]`; `--mode auto` and
a non-terminal stdin go on. That answer and the command itself are the approval.

### 10.2 Each task

- **pending** → dispatched: Go writes the task input (§11.1) to
  `.orchestrator/stories/<slug>/tasks/<ID>.json`, records the task file's sha256 (frozen from now
  on), and runs `<orchestrator> run --mode auto --story-task <that file>` as a child process in
  the task's repository (`--claude-auth` passed through). Its output streams to the terminal
  under a header `── RF02 · gateway · 2/6 ──`.
- **The child's run is found** by its `task.source` (`kind: story`, the slug and the ID) in that
  repository's runs directory, newest first, and loaded with the strict loader. No run found
  (the child failed its own fail-fast chain) → the task `failed` with the exit code; the next
  `story run` dispatches it again.
- **done** when the run is `completed` and has a `commit`. Otherwise:
  - `completed` without a commit (a failing review, a secret warning, `approval: manual`) →
    the task and the story are `blocked`: the operator commits in the kept worktree or drops the
    work, and runs `story run` again, which counts the task done once its branch has a commit
    beyond its base;
  - `failed` or `stopped` → the story stops; the next `story run` resumes that run
    (`orchestrator resume <id> --mode auto` in its repository, §37);
  - `story run --retry RF02` abandons a failed task's run instead (its branch stays) and
    dispatches the task again on `story/<slug>/RF02.<n>`, re-reading its file (a retry is the one
    way to edit a dispatched task). Dependents then stack on the retry's branch.
- Ctrl-C reaches the child; its run is `stopped`, the task too.
- Before every dispatch, `story.max_cost_usd` against the recorded total (the planning calls and
  every child's `outcome.usage`), fail closed. A task's `cost_usd` is its current run's
  `outcome.usage.cost_usd`, replaced and not added when that run is resumed (its outcome is
  computed over the whole history); the cost of the runs a `--retry` left behind moves to
  `abandoned_cost_usd`. A finished run without an outcome has no known cost: with a budget set
  the story stops rather than count it as 0. A call that reported no cost (a partial one) counts
  what it reported, so the total is then a lower bound.
- A `story run` that dies between a child's end and the save of its result leaves the task
  `running`; the next one settles it before anything is dispatched, from the run it finds (the
  same lookup as after a child), and does not write As built twice.

### 10.3 After each task

Go appends to the task file:

```markdown
## As built (2026-10-20)

Run `3f2a…` in gateway: completed, 1 fix iteration, tests passed, review pass.
Branch `story/refunds/RF02` on `story/refunds/RF01` @ 8d0e4b2, commit 1c9f2e7.
Cost $0.42. Implementation summary: Add partial refund amount to POST /v1/refunds.
Conflicts with main: none.
```

and rewrites README.md (the task ticked, the `Status:` line). Nothing a model wrote goes there
except the run's `summary_short`, as data.

### 10.4 The end

All tasks done → the story is `completed`. It prints, per task: repository, branch, the branch
its merge request targets, commit, Jira key, conflicts. Push and merge requests are TODO W2.

## 11. The child run

### 11.1 The task input

`run --story-task <file>` is hidden (like `mcp-verify`). The file is the parent's, strictly
validated:

```json
{"story": "refunds", "task": "RF02", "attempt": 1,
 "branch": "story/refunds/RF02", "base_ref": "<full sha>", "base_branch": "main",
 "jira": "PAY-124", "acceptance": ["…"], "text": "…"}
```

- `text` is what Go rendered (bounded to 32 KiB): the story's title and the task's place in it,
  the task file's body, the spec's approach and contracts (as context, not the task), and for
  every done task this one depends on, its As built line. It becomes `Task.Description`, and
  `task.source` records `{kind: story, story, task, attempt, jira}`, displayed only, and also
  the `branch`, `base_ref` and `base_branch` the run was asked for: a resume that comes before
  the worktree exists has no other place to find them, and run.json is the only thing a resume
  reads. All of them are validated on load, the branch against the story and task recorded.
- `base_ref` must be a commit in this repository; `branch` must match
  `^story/[a-z0-9-]+/[A-Z]{2}[0-9]{2}(\.[0-9]+)?$` and not exist; `base_branch` (for the
  conflict check, `specs/worktree/` §10.3) is the repository's story base branch, not the
  stacked parent: that is where the change finally goes.
- `--story-task` excludes `--stage`, `--jira`, `--inplace` and positional text.

### 11.2 What differs from `run`

- **Always a worktree run**, whatever `execution.isolation` says (`options.isolation: worktree`
  is recorded), on the given base and branch.
- **Development is always on.** A story task is a code change by construction: after the policy,
  `development` (and so `testing`) is added with the reason `story task` in `decision.json`,
  like an explored stage. Everything else is the policy's.
- **The scope question is not asked** (§13): a story task is never a story.
- **Review criteria**: the task's `acceptance` items are review criteria, ahead of the plan's own
  (§44.4), and recorded in `run.json: task.acceptance` so a resume uses the same ones.
- **The commit message** starts with `<jira>: ` when the task has a key (as for `run --jira`) and
  gains the trailer `Orchestrator-Story: <slug>/<ID>`.

Everything else - translation, decision, gates, guard, secret scan, resume, the commit on the
run's branch, the conflict check - is the worktree run of `specs/worktree/`.

## 12. Jira

### 12.1 A story issue type

`run --jira KEY` on an issue whose type is in `story.jira_issue_types` refuses before creating a
run: `PAY-1 is an Epic (story.jira_issue_types): plan it with orchestrator story plan --jira
PAY-1`. `story plan --jira` accepts any type.

### 12.2 Child issues

- Only for a story planned from a Jira issue (`story.json: jira_parent`) and only with
  `jira.create`. A story without a parent creates nothing (a note says so).
- When `story run` starts or continues, before the first dispatch: one `POST
  /rest/api/2/issue` per task without a key, in the story's order, to `jira.base_url` only, no
  redirects followed, credentials from the environment, redacted, scrubbed from children:

```json
{"fields": {"project": {"key": "<parent's project>"}, "issuetype": {"name": "Story"},
 "summary": "RF02 · Expose partial refunds in the public API",
 "description": "<wiki markup>", "parent": {"key": "PAY-1"},
 "labels": ["orchestrator"], "components": [{"name": "Billing"}]}}
```

  With `link: field` the parent goes into `fields.<field>` instead of `parent`.
- The description is rendered by Go from the task's sections into Jira wiki markup (headings,
  lists, code, inline code), plus a line naming the repository, the spec path and the
  dependencies' keys. Issue links between children are not created (§17).
- **Idempotent.** Before each POST `story.json` records an intent for the task; after a 201 the
  key goes into `story.json` and the task file's front matter, and the intent is cleared. A
  response is validated strictly (`key` is `<project>-<n>`). A request is never retried after it
  may have reached Jira (a transport error after sending, a timeout); only a 429 or 503 with
  `Retry-After` is. An intent without a key on the next start stops the story:
  `RF02: a Jira issue may already exist; set jira: <KEY> or jira: none in its front matter`.
- A failure stops the story before any dispatch; nothing is half-applied beyond the keys already
  recorded.

## 13. The scope question (shadow)

- A fourth yes/no question joins the classification call (Jev and the LLM provider alike): *Does
  fulfilling `request` need several changes that are each worth reviewing and merging on their
  own - in different services or repositories, or as steps that each leave the system
  working?*
- Its probability is recorded in `decision.json` as `scope.story` and **decides nothing**:
  no threshold, no hint, no workflow change. It is data for a later calibration against the
  runs that turned out too large (failed on `max_fix_iterations`, budget, a large diff).
- It changes `questions_hash`, so `decisions calibrate` keeps old and new decisions apart; the
  cuts and thresholds of §9.1 must be re-checked once enough new decisions exist (one more
  question in the same call may move the others' answers).
- Not asked for `--stage` (no provider) or `--story-task` (§11.2).

## 14. Persistence

`<home>/.orchestrator/stories/<slug>/`:

| file | content |
|---|---|
| `story.json` | slug, spec dir, `jira_parent`, `created_at`, `approved_at`, `services` {name: path}, `bases` {name: {branch, sha}}, `status`, per task: `status`, `file_sha256`, `attempt`, `run_id`, `repo`, `branch`, `base_ref`, `commit`, `jira`, `jira_intent` (a flag), `cost_usd`, `abandoned_cost_usd`; `calls` (survey, specify: usage, cost) |
| `ticket.json` | the fetched issue (`--jira`), an audit record |
| `artifacts/survey-<repo>.json`, `artifacts/specify.json` | the prompts as sent and the answers |
| `logs/survey-<repo>.*`, `logs/specify.*` | the calls' output |
| `tasks/<ID>.json` | the child's task input (§11.1) |

`story.json` is validated strictly on load (paths derived, never read; run IDs and SHAs by
format; a task's `run_id` is loaded only from its repository's runs directory). Every field
story run adds to what story plan wrote is optional, so a plan-time record loads; a task's
`branch` must be `story/<slug>/<ID>` for attempt 1 and `story/<slug>/<ID>.<attempt>` after,
and a task that was dispatched needs `approved_at`. The story
directory is `flock`ed by the one `story run` that owns it. A `story plan` that does not reach a
spec keeps its directory as `.plan-<id>` for the operator.

Each child's `run.json` gains `task.source` (kind `story`) and `task.acceptance`, validated on
load. `decision.json` gains `scope.story` and the `story task` reason on development.

## 15. Security

- **Authorities stay fixed in Go.** Survey: `read`, in one repository, with its deny list.
  Specify: `none`. A child run: as any run. No call reads two repositories; no new authority
  and no multi-directory access is needed.
- **The spec is model-written data that becomes a task after the operator's approval.** It
  reaches each child as `Task.Description` through `taskBlock`, like a Jira ticket. It cannot
  name a stage, a model, a tool, an authority or a command; the front matter is parsed by Go with
  a closed set of keys, and `repo` must be a service found under `story.roots` (§7.1).
- **Names are checked before they reach git**: slug, prefix, IDs and branch names by pattern; the
  base by SHA.
- **Jira writes** only after approval, idempotent, to the configured host only, with the same
  credential handling as the read (§39). It is the first write to a remote system; §30 and §39
  change to say so.
- **Child processes** are the orchestrator binary itself (`os.Executable`, symlinks resolved)
  with argv only; each child runs its own fail-fast chain, guard and sandbox.

## 16. Tests

- **Unit** (`internal/story`, pure): front matter parse and render round trip; unknown keys;
  ordering with ties; each graph error; the base rule (none, stacked, cross-repository ancestor
  in the same repository, diamond refused); the frozen-hash boundary at `## As built`; README
  rendering (golden).
- **Prompts** (golden): survey and specify, with the fixed nonce.
- **Jira** (fake server): create with `parent` and with a field; a 400 with `errors`; a 429 with
  `Retry-After` retried; a timeout after sending not retried and the intent kept; redaction.
- **Integration** (fake executables, two fake repositories, a fake Jira): plan writes the spec;
  one task prints a run; `story run` creates the children, dispatches in order, stacks the
  same-repository branch, commits each, appends As built; a failing task stops the story and
  the next `story run` resumes it; `--retry`; a blocked task; an edited dispatched task refused;
  `run --jira` on an Epic refused; the operator's checkouts untouched throughout.

## 17. Not covered

- Pushing and merge requests (TODO W2), Jira transitions, comments and issue links (W3).
- Running independent tasks in parallel (§30).
- Rebasing a stack after review, re-planning, a model editing the spec after approval.
- Nested stories; a story task without a code change.
- Submodules (as for worktree runs).
- The benchmark: story tasks are not in `bench/` (a later task could mine this repository's own
  specs as story tasks).
- Calibrating and using the scope probability (a later spec, on data).

## 18. Plan and estimate

| task | title | size | depends on |
|---|---|---|---|
| ST01 | `internal/story`: task files, graph, order, bases, README rendering | M | — |
| ST02 | config: `story`, `jira.create`; finding the services the task names | S | ST01 |
| ST03 | survey and specify: prompts, calls, strict answers, logs | M–L | ST02 |
| ST04 | `story plan`: writing the spec, the one-task case, `.plan-<id>` | M | ST01, ST03 |
| ST05 | `run --story-task`: input, worktree base and branch, development on, criteria, commit message | M | ST01, WT04, WT06 |
| ST06 | `story run` / `status`: state, lock, pre-checks, dispatch, resume, retry, blocked, As built, budget | L | ST01, ST02, ST05, WT07 |
| ST07 | Jira children: client create, wiki rendering, intents, front matter write-back | M | ST06 |
| ST08 | `run --jira` refuses a story issue type | S | ST02 |
| ST09 | the scope question (shadow): questions, Jev, LLM, `decision.json`, goldens | M | — |
| ST10 | scenarios, MVP.md §48 and the sections above, CLAUDE.md, README, example, TODO.md | M | all |

```
ST01 ─┬─ ST02 ─┬─ ST03 ─ ST04 ──────┐
      │        ├─ ST08 ─────────────┤
      │        └──────┐             │
      └─ ST05 ─────── ST06 ─ ST07 ──┼─ ST10
         (ST05 also needs WT04, WT06; ST06 needs WT07)
ST09 ───────────────────────────────┘
```

Estimate: 20–24 engineering days, after `specs/worktree/`. ST01, ST02 and ST09 can start
before the worktree work is done; ST05 and ST06 cannot.

## 19. Owner's decisions (2026-10-17)

| # | decision |
|---|---|
| D1 | Story is a separate axis (`scope`), not a fourth complexity level; each task gets its own complexity. |
| D2 | Triggers in v1: the explicit `story plan`, a Jira issue type, `specify` as the final arbiter (one task → not a story), and the scope probability recorded in shadow (§13). |
| D3 | Services are separate local repositories; each task runs in its repository with that repository's config. Revised the same day: the task names its services and Go finds them under `story.roots` (§7.1), instead of a closed `story.repos` list and a survey of every repository. |
| D4 | A branch per task, stacked at once on the branch it needs in the same repository; a rebase after review is the operator's, with git. |
| D5 | Jira: child issues created after approval, idempotently; their type and link from config. |
| D6 | The spec lives in the story home, by default the current repository, as `specs/<slug>/` in this repository's format. |
| D7 | The worktree run (`specs/worktree/`) comes first and is used as is. |

## 20. Author's defaults (change before the task starts if wrong)

| # | default | why |
|---|---|---|
| A1 | A survey call per service the task names (`read`, in place) before specify, rather than the Context Manager's snapshot. | Contracts between services (OpenAPI, proto, events) are what a decomposition needs; a read session finds them, a file list may not. Only the named services are read (D3), so the cost is one call per service the story touches. One call per repository needs no multi-directory authority. |
| A2 | Survey and specify reuse the `actions.analyze` and `actions.plan` routes. | Static routing with no new keys. |
| A3 | A story task always has development on (§11.2). | A task that changes nothing would leave no branch for its dependents. |
| A4 | The task's acceptance items become review criteria (§11.2). | The approved spec, not a later plan, is what the task is checked against. |
| A5 | `story run` both starts and continues; a failed task resumes its run; `--retry` starts over on a new branch. | One command; resume keeps the fix context, retry allows an edited task. |
| A6 | A dispatched task's file is frozen up to `## As built`. | What ran is what the record says. |
| A7 | Jira children only for a story planned from a Jira issue. | Without a parent there is nothing to attach them to; creating parents is a later step. |
| A8 | `run --jira` on a story issue type refuses, with the `story plan` command. | Running an epic as one task is the case this spec exists to avoid. |
| A9 | `story.roots` may be relative to the story home; a service's base is the branch checked out in it at the first start. | Side-by-side checkouts make the config shareable, and nothing is listed per service. |
| A10 | The spec is written in the language of the request. | The operator and the Jira readers read it. |
| A11 | The first start asks one confirmation at a terminal. | The order and the bases are Go's and are seen only then. |
