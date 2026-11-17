# Listener story: an Epic becomes child tickets, the children run in parallel

Status: approved, 2026-11-13. Owner's answers L1–L7 (§0); the author's defaults A1–A16 (§14),
confirmed by the owner 2026-11-13.

## Problem

The listener (MVP.md §51) runs one ticket as one run in one service, one ticket at a time. A
ticket that spans services is an Epic, and the listener refuses it (`isEpic`, §51.3: "plan it as a
story"). The story (MVP.md §48) can split such a task, but only at a terminal: `story plan` needs
the checkouts side by side under `story.roots` and an `orchestrator.yaml` in each, the operator
reviews the spec, and `story run` both creates the Jira children and runs them itself.

Seen on PMC-2636 (Jira Cloud, 2026-11-13, an Epic "Digital categories migration"): the work is a
sku-master change (a categories dictionary, two country-specific attributes), a sku-master-fe
change (a category group in the editor, values filtered by country) and pim-qa tests, with the
logic and the UI taken from product-master and product-master-ui, which are not changed. The
owner wants to label the Epic `ai-run` and have the listener split it into tickets that the
listener then runs like any other.

## 0. The owner's answers (2026-11-13)

| # | answer |
|---|---|
| L1 | An Epic labelled `ai-run` is planned by the listener: it is no longer refused. |
| L2 | The listener moves the Epic to `start` (In Progress), creates the child tickets with their description and labels (`ai-run`, `service:`, `gitlab:`) in the queue's status (To Do). The Epic stays In Progress; moving it to Done is the operator's. |
| L3 | Children of different services have no order between them and run **in parallel**. Rolling them out in order is the operator's. |
| L4 | Several children of **one** service are chained with "is blocked by" links and run one after another; the next one starts **after the previous one is merged**. |
| L5 | The **model chooses** the services from the Epic's text. |
| L6 | Reference repositories (`ref:` labels, e.g. `ref:HnBI/pim/product-master`) are cloned and surveyed read-only, never get a task, and every child's text links them. |
| L7 | Real parallelism, in this spec: `listener.max_parallel` runs in different services at once. |

## Approach

The listener keeps its loop, claim, outcomes, state and recovery. Three things are added:

```text
poll (whenever a slot is free, §3):
  search → for each issue, in order, until the slots are full:
    re-check in Go (§3.3 of MVP.md), plus: blocked by an unfinished ticket (§4) → skip
    its service has a run in progress (§3) → skip
    an Epic (story.jira_issue_types) and listener.story set → plan it (§5–§9), alone
    otherwise → claim and run it as today, in a slot of its own
plan of an Epic (exclusive: no other run in progress, none starts meanwhile):
  claim (start) → children already there? (§9) → the catalog (§5.1) → select (§5.2)
  → clone/fetch the refs (§5.3) → survey each ref (§6) → requirements (specs/listener-requirements)
  → specify (§7) → validate
  → create the children, then the links (§8) → comment on the Epic, it stays at start (§9)
```

* **A plan is Go's, the model only proposes** (MVP.md §48): Go fixes the catalog the model chooses
  from, the services it may target, the labels, the links, the order of the writes. No model ever
  decides what runs, where or when: the children are ordinary tickets, picked by the ordinary poll.
* **The children are ordinary tickets.** A child's run is the listener's run of today (§51.7):
  clarify, the decision, investigation with `fetch_repository`, review, delivery. Nothing in the
  run knows it came from an Epic.
* **Parallelism is per service** (as `story run`, specs/parallel-story, TODO S10): at most one run
  per service, at most `listener.max_parallel` runs. Two runs of one service would share its
  checkout (the fresh base, §51.6), its test ports and databases.

### Ground rules for the tasks of this story

- This is a story task, run with `orchestrator story run specs/listener-story`. `story run`
  writes `specs/listener-story/README.md` and each task's `## As built`: **do not edit anything
  under `specs/listener-story/`**.
- Only LS07 edits the shared documents (MVP.md, CLAUDE.md, TODO.md, README.md,
  `orchestrator.example.yaml`, `examples/embed/`). Every other task says in its commit what LS07
  must document.
- The tasks form one chain (LS01 → … → LS07), each stacked on the one before.
- Read this file in full before starting: the task text names its sections (§N).
- CLAUDE.md's invariants hold unless §13 changes them. `internal/listener` stays pure (domain and
  the standard library): blockers, the chain of one service's tasks, the child's labels, the
  comments. HTTP stays in `internal/jira` and `internal/gitlab`, git in `internal/repository`,
  goroutines and processes in `app`.
- With `listener.max_parallel: 1` and no `listener.story` the listener behaves exactly as today,
  except §4 when `listener.blockers` is set; its tests stay as they are.
- The gate is `scripts/check.sh`. No live gate (`-tags security`) is needed; the live check on
  Jira Cloud and gitlab.com is the operator's (§12).

## 1. CLI

Nothing new. `orchestrator jira listen` and `orchestrator.Listen` take the new keys from the
configuration. `orchestrator validate` checks them (§2).

## 2. Configuration

```yaml
listener:
  max_parallel: 2                 # new: 1..8, default 1; runs at once, at most one per service
  labels:
    ref_prefix: "ref:"            # new: default "ref:"; ref:<group>/<path> is a reference repository
  blockers:                       # new, optional: §4
    link_type: Blocks             # the link type's name; its inward side is "is blocked by"
    done_statuses: [Done]         # a blocker in one of these is finished (case-insensitive)
  story:                          # new, optional: an Epic is planned (§5–§9); absent: refused as today
    max_services: 5               # 1..20: the services the selection may choose
    max_refs: 3                   # 0..10: ref: labels on one Epic
    max_tasks: 12                 # 1..30
    max_catalog: 500              # 1..2000: projects listed for the selection (§5.1)
    max_cost_usd: 5               # optional: every planning call of one Epic
story:
  jira_issue_types: [Epic]        # existing: the types the listener plans (default [Epic], as isEpic)
jira:
  create:                         # existing (MVP.md §48.1); required with listener.story
    issue_type: Task
    link: parent                  # parent | field
    labels: []                    # added to the child's own labels (§8.1)
    components: {}                # by service name
gitlab:
  api_token_env: GITLAB_READ_TOKEN   # existing (revise); required with listener.story (§5.1)
```

* Every key is in the strict schema. `listener.story` without `jira.create`, without
  `gitlab.api_token_env`, or without `listener.blockers` is a config error: a plan creates links,
  and links that nothing reads would not chain anything (L4).
* `listener.story` does not use `story.roots`, `story.spec_dir` or `story.max_parallel`: the
  listener's services are under `listener.services_dir`, and its plan is not written into any
  repository (§7.3). A `story` section is not required; only `jira_issue_types` is read from it.
* At start, with `listener.story`, the listener's own account (GET /myself, which the listener
  already makes) must be one of `listener.query.reporters`: the children it creates are reported
  by it, and the poll would never pick them otherwise. Not one: a config error before the first
  poll (A15).
* `max_parallel` above 1 is checked against nothing else: the pod's CPU and memory are the
  operator's (§12).

## 3. Slots

* The listener has `max_parallel` slots. A run (a new one, a resumed one, a restart's recovery)
  takes a slot until its outcome is written; an Epic's plan takes **every** slot (A12): it starts
  only when no run is in progress, and nothing starts until it ends.
* **When it polls**: at start (after the recovery), whenever a run ends, and every
  `poll_interval` while a slot is free. Not while every slot is taken.
* **What it dispatches from one poll**: the issues in the search's order, as today; each one that
  passes the re-checks is dispatched if a slot is free and its service is free. The service is
  computed from the head before the claim (`listener.Service` on the labels and components; an
  issue whose service cannot be named is dispatched as today and blocked in `process`). A ticket
  skipped for a busy service is not claimed and not logged more than once per poll.
* **Each run is a goroutine** calling `runTask` / `resumeRun` as today (specs/embed): both take
  the service's directory, nothing reads the working directory, and the process environment is
  set once at start (§51.6), so runs in different directories do not share mutable state. The
  state (`listener.State`) is changed under one mutex and saved after each change, as today.
* **Output**: with `max_parallel: 1` unchanged. Above 1, each run's progress goes to
  `.orchestrator/listener/logs/<KEY>.<attempt>.log` (as `story run --parallel`,
  `.orchestrator/stories/<slug>/logs/`), and the listener's stderr gets one line per start and per
  end (`PMC-12 running in sku-master (log: …)`, `PMC-12 done: merge request …`).
* **Stopping**: SIGTERM cancels every run and the plan in progress, waits for all of them, saves
  the state; each run's outcome is exit 5 as today (stays `start`, resumed on restart).
* **Restart**: every unfinished entry is recovered as today (§51.9), the running ones resumed,
  each in a slot, at most `max_parallel` at once and at most one per service; the others wait for
  a slot before the first poll.
* A panic in one run is contained in its goroutine as today (`contained`): exit 1, `failed`, the
  other runs go on.

## 4. Blockers

* The search asks for `issuelinks` too. `domain.TicketHead` gains `BlockedBy []TicketRef{Key,
  Status}`: the issues of the links whose type's name equals `blockers.link_type`
  (case-insensitive) on the issue's **inward** side (`inwardIssue`: "is blocked by"), with the
  status Jira gives in the link. A link Jira returns without a status counts as unfinished.
* With `listener.blockers` set, a ticket with any blocker whose status is not in `done_statuses`
  is skipped: not claimed, no comment, no attempt; one log line when it is first skipped for that
  reason (`PMC-14 waits for PMC-13 (Code Review)`). It is picked by a later poll once every
  blocker is finished. This holds for every ticket, not only the children of a plan.
* The re-check is Go's (the JQL does not filter links). The run's fresh base (§51.6) then starts
  it from the default branch with the blocker's merge in it (L4); the listener does not check that
  the merge happened, only the status.
* An Epic's own blockers are read the same way: an Epic blocked by an unfinished ticket waits.

## 5. The services of a plan

### 5.1 The catalog

* `internal/gitlab` gains `GroupProjects(ctx, group) ([]domain.Project{Path, Name, Description,
  DefaultBranch}, error)`: `GET /api/v4/groups/<group>/projects?include_subgroups=true&archived=false&per_page=100`,
  following `X-Next-Page` / the `Link` header within `gitlab.base_url`, strict decoding, the same
  read-only, no-redirect, redaction and retry rules as the client's other calls. A project whose
  path is not under the group or fails `validProject` is dropped and counted in a log line.
* The catalog is every `allowed_groups` group's projects, deduplicated by path, sorted by path,
  at most `listener.story.max_catalog` (more: the first ones by path and a log line naming the
  cut). A description is one line, cut to 300 runes, its control characters removed; it is model
  input, fenced, never instructions.
* The catalog is fetched once per plan. A GitLab failure: network/5xx → `retry` with backoff as a
  clone's (§51.8), 401/403 → `failed` with a comment naming `gitlab.api_token_env`.

### 5.2 The selection

* **Labels first** (A2): when the Epic has `gitlab:<path>` labels (several are allowed on an
  Epic, unlike a ticket), they are the services and no call is made. Each must be under
  `allowed_groups`; the service's name is the path's last segment; two with the same last segment
  are `blocked`.
* **Otherwise one call** (`select`), tool-less (`none` authority), on the listener configuration's
  `actions.investigate` route and timeout, outside the workflow like `specify` (MVP.md §48.2): the
  Epic's rendered text (§51.7, with its discussion), the catalog (fenced) and the reference paths
  (which are not choosable). The answer is `{"services": [{"path": "...", "why": "..."}]}`,
  strictly validated: 1..`max_services` items, each a catalog path, no duplicate, no reference,
  `why` one line ≤ 200 runes; two paths with the same last segment are an invalid answer. An
  invalid answer is an `InvalidResponse` → `failed` (the answer is kept in the plan's artifacts).
* The services are printed in the plan's log and named in the Epic's comment with their `why`.

### 5.3 Checkouts

> **Amended by specs/listener-requirements (2026-11-17):** a plan checks out the references only.
> A target is not checked out by the plan: its child's run does it, as for any ticket.

* Every target and every reference goes through the listener's checkout of today
  (`serviceCheckout.prepare`, §51.6): clone when absent, else fetch and fast-forward the default
  branch, the same refusals. The name is the path's last segment. A reference's name equal to a
  target's is `blocked`.
* **References**: labels `<ref_prefix><group>/<path>`, parsed like `gitlab:` (the same segment
  rules, `.git` stripped), at most `max_refs`, under `allowed_groups`. They are checked out under
  `services_dir` like services; nothing is ever written into them.
* A checkout that fails for the network → `retry`; any other checkout problem → `failed`, as a
  ticket's.

## 6. The survey

> **Amended by specs/listener-requirements (2026-11-17):** one `survey` call per **reference**
> only; no target is surveyed. The surveys feed one tool-less `requirements` call, whose answer is
> written on the Epic (MVP.md §51.13); studying a target is its child's run's job.

* One `survey` call per target and per reference (`action.Survey`, MVP.md §48.2), `read`
  authority, in the checkout, in place, on the listener's own `actions.investigate` and
  `context.extra_deny`: the plan only reads, so it needs no service's configuration; a target's
  is required when its child ticket runs (amended 2026-11-17).
* A reference's survey is told it is a reference: what the targets must reproduce or read from
  it (models, dictionaries, rules, UI), not what to change in it.
* The calls run one after another (the plan is exclusive, §3; a survey is short).

## 7. The specification

### 7.1 The call

> **Amended by specs/listener-requirements (2026-11-17):** the listener's specify is fed the
> Epic's requirements and the targets by name and note instead of target surveys
> (`SpecifyInput.Requirements`, `Notes`): a task describes behaviour and contracts, never files or
> packages. `story plan`'s specify is unchanged.

* `action.Specify` as today, with two inputs added: `References` (the reference surveys, fenced,
  labelled "reference: not changed, never a task's repo") and `Ordered: false` — the listener
  says the tasks of different services run in parallel from their default branches, so a task
  must not need another service's unmerged change to pass its own tests (L3). The `Repos` are the
  targets only. The rules of MVP.md §48.2, step 4, hold (one repository, a vertical slice, its tests pass, its
  own docs).
* `story.Validate(tasks, targets, max_tasks)` as today. One task is a plan too (A6): it gets one
  child.

### 7.2 The order of one service's tasks

* Go chains the tasks of each service in the story's order (Kahn's order of `depends_on`, the
  smallest ID first, `story.Order`): each task of service S is blocked by the previous task of S
  (L4). A `depends_on` within S is thereby kept; a `depends_on` across services gives no link and
  is shown as a line in the child's text (`Needs PMC-21 (sku-master), not linked`).

### 7.3 Where the plan lives

* `.orchestrator/listener/plans/<KEY>.<attempt>/`, made with `persistence.NewStoryPlan`: the
  ticket, every call's prompt, answer, output and usage (`select`, `survey-<name>`, `specify`),
  and `plan.json` (the answer, the targets, the references, the chain, the children as created).
  Nothing is written into any repository.
* `listener.story.max_cost_usd` is checked before each call against the plan's calls so far
  (priced as a run's are); reached → `failed`, nothing created.

## 8. The children

### 8.1 Each child

| field | value |
|---|---|
| project | the Epic's project |
| issue type | `jira.create.issue_type` |
| parent | the Epic, by `jira.create.link` (`parent`, or the `field`) |
| summary | the task's title, cut to 255 runes (no task ID: the ID is the plan's, not the team's) |
| labels | `listener.query.label`, `<service_prefix><name>`, `<project_prefix><path>`, then `jira.create.labels`, deduplicated |
| components | `jira.create.components[<name>]` when set |
| description | `jira.Wiki` of the task (§8.2) |

* The child's status is the workflow's initial one; the listener does not transition it. When
  that status (read back once, after all creations) is not one of `query.statuses`, the Epic's
  comment says so: the poll will not pick the children (A16).

### 8.2 The description

`jira.WikiTask` gains three fields; the wiki is, in order:

1. the task's body (goal, `## Acceptance`, notes), as today;
2. `Story: <Epic key> — <Epic summary>` and `Service: <name> (<path>)`;
3. `References:` one line per reference, its project URL (`<base_url>/<path>`), so the child's
   investigation can `fetch_repository` it (MVP.md §44.3, "Fetch a repository");
4. `Needs:` the cross-service `depends_on` keys (§7.2), when any;
5. the spec's approach and contracts, under `h3. From the plan`, cut so that the whole description
   stays within `jira.max_bytes / 2`, with `… (cut; the whole plan is in <Epic> comment)` when cut.

The description is model text: escaped by `jira.Wiki`, then secret-scanned like the listener's
comments (§51.11). A finding in any child stops the plan before the first creation: `failed`,
the comment names the task, not the text.

### 8.3 Order of the writes

* The children are created in the story's order, one POST at a time (`jira.Client.Create`), each
  after an intent saved in the Epic's state entry and its key saved after the 201, never repeated
  once it may have reached Jira (`jira.MayExist`) — the rules of `story run`'s children
  (`createJiraChild`).
* Then the links, one `POST /rest/api/2/issueLink` each (`jira.Client.Link(type, inward,
  outward)`: `inwardIssue` the later task, the one that is blocked), same intent rules.
* A write that may have reached Jira and has no answer stops the plan: `failed`, the comment lists
  the children created so far and the one in doubt ("look under PMC-2636 and finish it by hand").
  A refused write (400, 403, 404) stops it the same way, without the doubt. Nothing is ever
  deleted.

## 9. The Epic's outcomes

> **Amended by specs/listener-requirements (2026-11-17):** a row is added: requirements that leave
> an open question → the requirements and the numbered questions as the one comment, `blocked`, no
> attempt counted, no child. The person answers in a comment and moves the Epic back to the queue.

| plan | comment | transition | attempt counted |
|---|---|---|---|
| children created | the services (with `why`), the table of children (key, service, title, blocked by), the references, a status warning (§8.1), the plan's cost | none: stays `start` (L2) | yes |
| children exist already (below) | their keys; "add the label ai-restart to plan it again" | `blocked` | no |
| no service, a bad label, a bad reference | the reason | `blocked` | no |
| an invalid answer, the budget, a secret, a refused or doubtful write | the reason, the children created so far | `failed` | yes |
| GitLab, Jira or provider: network, 5xx, rate limit | none | `retry` with backoff | no |
| 401 from Jira | none | `retry`; the listener stops | no |
| the listener stopping | none | none: stays `start`; re-planned from the start on restart (A10) | no |

* **Children exist already** (A9): before the catalog, one search `parent = <KEY>` (or
  `cf[<id>] = <KEY>` with `link: field`) for issues reported by the listener's account. Any →
  the row above. With the restart label (`listener.labels.restart`) the search is skipped, the
  label removed as for a ticket, and a new plan creates new children; the old ones are not
  touched.
* The Epic's state entry has `kind: plan` and `plan_dir`; its run fields stay empty. A plan cut
  off by a stop has nothing to resume: the restart plans it again, and the children check (above)
  sees what was already created (the intents in the entry name any in doubt: the comment names
  them and the Epic is `failed`).
* An Epic never runs as a ticket: without `listener.story` it is refused as today.

## 10. Security

* The catalog, the references and every target are under `allowed_groups`, whose
  `orchestrator.yaml` is already trusted (§51.11). The selection cannot leave the catalog; Go
  validates it.
* The children are created by the listener's account and run by it: no authority comes from the
  Epic's text or the model's (§38.5). What a child may do is its service's configuration, as for
  any ticket. The reporter check still holds: the account must be in `query.reporters` (§2).
* The model's text reaches Jira only as the children's descriptions and the `why` lines: escaped,
  bounded, secret-scanned (§8.2).
* `gitlab.api_token_env` is the read-only token of revise: scrubbed from every child process,
  never in a log, a URL or Jira.

## 11. Testing

* `internal/listener` (pure): the blockers filter (§4), the per-service chain (§7.2), the child's
  labels, the outcome table (§9), the comment bodies; golden files for the Epic's comment.
* `internal/jira`: `issuelinks` in a search answer (Cloud and Data Center shapes, a link without a
  status), `Link` (201, 400, a 5xx after sending not retried), the children search.
* `internal/gitlab`: `GroupProjects` with two pages, a project outside the group, a redirect
  refused.
* `internal/app`, with the fake Jira, fake GitLab and fake providers of the listener's tests:
  * an Epic labelled `ai-run` with `ref:` labels → select picks three services → three children
    with their labels, in To Do, two of one service linked, the Epic at `start` with its comment;
  * `gitlab:` labels on the Epic → no select call;
  * `max_parallel: 2`: two tickets of two services run at once (a barrier in the fake provider);
    two tickets of one service do not; a third waits for a slot;
  * a ticket blocked by one in Code Review is skipped, then picked when the blocker is Done;
  * a creation that times out after sending → `failed`, no second POST, the comment names it;
  * an Epic with children already → `blocked`; with `ai-restart` → a new plan;
  * SIGTERM with two runs in progress → both exit 5, both resumed on restart;
  * no `listener.story`: an Epic is refused as today (the existing test stays).

## 12. Left to the operator

* The live check on Jira Cloud and gitlab.com: the Epic workflow's `start` transition, the link
  type's name, the children's initial status, a read-only API token that can list the groups.
* The pod's CPU and memory for `max_parallel` runs (a Go service's compile peaks near 1 GB).
* The deployment's configuration (the host repository).

## 13. Changes to MVP.md and CLAUDE.md

* MVP.md §51.3: an Epic is planned with `listener.story`, refused without it; the blockers
  re-check. §51.2: the new keys. New §51.13 "Planning an Epic" (§5–§9 of this spec) and §51.14
  "Slots" (§3). §51.12: drop "Story mode for Epics and multi-service tickets" and "several
  tickets at once". §48.1: `jira.create` is also the listener's. §39: links in the search, the
  link write.
* CLAUDE.md: the listener section (one ticket at a time → slots, at most one per service; the
  Epic's plan), `internal/gitlab` gains a second GET, the pointer to `specs/listener-story/`.
* README.md (the listener section), `orchestrator.example.yaml` (the keys, commented), TODO.md.

## 14. The author's defaults (confirmed 2026-11-13)

| # | default |
|---|---|
| A1 | The catalog is GitLab's project list of `allowed_groups` (read-only API token), not a list in the configuration. |
| A2 | `gitlab:` labels on an Epic, when present, are the services and the selection call is skipped: the operator's override of L5. |
| A3 | A service without `orchestrator.yaml` is surveyed under `listener.default_config`. |
| A4 | Tasks across services are told they run in parallel from their default branches; a cross-service `depends_on` is a line in the text, not a link. |
| A5 | The tasks of one service are chained by Go in the story's order, whatever the model's `depends_on` says. |
| A6 | One task is a plan too: one child. |
| A7 | A child's summary is the task's title, without the task ID. |
| A8 | Writes follow `story run`'s rules: intent, one POST, never repeated; nothing is deleted. |
| A9 | Children reported by the listener under the Epic already → `blocked`, unless `ai-restart`. |
| A10 | A plan cut off by a stop is planned again from the start; the children check keeps it from duplicating. |
| A11 | No clarify step on the Epic: each child runs clarify on its own text. |
| A12 | A plan is exclusive: it starts when no run is in progress and nothing starts while it plans. |
| A13 | `max_parallel` 1..8, default 1; above 1, per-ticket log files and one stderr line per start and end. |
| A14 | `listener.blockers` applies to every ticket, and is required with `listener.story`. |
| A15 | With `listener.story`, the listener's account must be in `query.reporters` (checked at start). |
| A16 | The listener does not transition the children; a non-queue initial status is a warning in the Epic's comment. |
