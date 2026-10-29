# Jira listener: tickets labelled `ai-run` become runs, one at a time

## Problem

Today a person starts every run (`run --jira KEY`, or a CI job that passes `$JIRA_KEY`). The owner
(2026-10-29) wants a long-lived mode, run in a Docker container: the orchestrator polls Jira every
minute for tickets with the label `ai-run`, takes them one at a time, works out the service,
clones it from GitLab when it is not there, runs it there under its own instructions, and reports
back to the ticket: questions and `Blocked` when the task is unclear, `Failed` with the reason when
the run fails, `Ready to Review` when the merge request is open.

## Approach

`orchestrator jira listen` is a supervisor, like `story run` (MVP.md §48.6): it owns Jira, the
services directory and its own state, and runs each ticket as a child `orchestrator run`
(`runChild`, `internal/app/story_run.go`) in the service's directory with `--mode auto --output
json`. The child is an ordinary worktree run: clarify, the decision, the guard, the sandbox,
delivery (commit, push, merge request) are unchanged and know nothing about the listener. The
listener reads the child's one JSON line and its `run.json`, and only then writes to Jira.

```text
start: fail-fast chain of the listener's config; GET /myself; flock; recover the state (§9)
loop (only when idle, every listener.poll_interval):
  search (JQL built by Go, §3) → for each issue, in order, until one is dispatched:
    re-check in Go (§3.3) → skip
    attempts spent (§8) → comment + failed
    claim: transition → listener.transitions.start                 (§4)
    service and project (§5) → unknown/ambiguous → comment + blocked
    checkout: clone or fetch + fast-forward (§6) → service problem → comment + failed
    render the ticket + the comments since the last question (§7)
    child: run --listener-task <file> --mode auto --output json [--config <default>]
    outcome → comment + transition (§8)
```

### Ground rules for the tasks

- CLAUDE.md's invariants hold unless §12 changes them, and the task that changes one edits
  CLAUDE.md and MVP.md in the same change.
- `internal/listener` is pure (domain and the standard library, an `imports_test.go` like
  `internal/story`'s): label parsing, project paths, JQL building, the outcome table, the state's
  validation. HTTP stays in `internal/jira`, git in `internal/repository`, processes in `app`.
- Git is argv only. No model is called by the listener itself: every word it writes to Jira is
  Go's, and model-written text (the clarify points) is escaped and bounded.
- Each task updates the documentation its change makes out of date (CLAUDE.md "Tasks from
  `specs/*/tasks`"). The gate is `scripts/check.sh`.

## 1. CLI

```bash
orchestrator jira listen [--config orchestrator.yaml] [--once]
```

- Runs in the current directory (the container's working directory, a volume). It needs no git
  repository there. `--once` runs one poll and the ticket it dispatches, then exits (tests,
  cron). Not combinable with `--mode`, `--stage`, `--jira`, task text, `--inplace`.
- Exit: 0 after SIGTERM/SIGINT with nothing left half-written; 2 for a config, credential or
  `FailureAuth` stop (§10); 1 for an unexpected error. Logs go to stderr, one line per event
  (`listen: PAY-12 claimed`, `listen: PAY-12 → Ready to Review (!12)`).

## 2. Configuration

The listener's own config file (in the working directory by default). Only `jira`, `listener`,
`gitlab`, `story.jira_issue_types`, `claude`/provider sections it checks, and `security`
matter to the listener; each service's run uses that service's config (§6.4).

```yaml
jira: { base_url: …, auth: basic, email_env: JIRA_EMAIL, token_env: JIRA_API_TOKEN }
listener:
  poll_interval: 1m           # 30s..1h
  services_dir: .             # relative to the working directory
  default_config: service.yaml  # optional; --config for a service without orchestrator.yaml
  max_attempts: 3             # 1..10, finished runs per ticket (§8)
  clone_timeout: 10m
  query:
    projects: [PAY]           # required, at least one
    label: ai-run
    statuses: ["To Do"]       # required; tickets are picked only from these
    reporters: [ "5b10a2844c20165700ede21g" ]  # required for now (D19): accountId on Cloud, username on DC
  labels:
    service_prefix: "service:"   # label service:billing
    project_prefix: "gitlab:"    # label gitlab:backend/billing
  transitions:                # a transition's name or its target status, as jira.write.transition
    start: In Progress
    blocked: Blocked
    failed: Failed
    done: Ready to Review
    retry: To Do              # back to the queue after an infrastructure failure (§8)
gitlab:
  base_url: https://gitlab.corp      # https, no path but an optional context path, no userinfo
  token_env: GITLAB_TOKEN            # env only; a literal token is refused
  username: oauth2                   # default
  allowed_groups: [backend, platform/payments]   # required; clone only from under these
```

- Strict schema; every value validated in `config.Validate`; JQL values are restricted to one
  printable line without `"` or `\` (so Go's quoting is total). `listener` without `jira` or
  `gitlab` is a config error. `gitlab.token_env` and the Jira variables are registered with
  `execution.ScrubCredentials` right after `Validate`.
- `ValidateDependencies` with `DependencyOptions{Jira: true}` plus the GitLab variable, `git`
  and the listener's own executable. The provider CLIs are checked per service by the child.
- The org layer applies to the listener's file as to any other.

## 3. Polling

### 3.1 The query

Go builds it; no JQL comes from the config:

```text
project in ("PAY") AND labels = "ai-run" AND status in ("To Do")
AND reporter in ("…") ORDER BY priority DESC, created ASC
```

- Jira Cloud (`auth: basic`): `GET /rest/api/2/search/jql?jql=…&fields=…&maxResults=50` with
  `nextPageToken`. Data Center (`auth: bearer`): `GET /rest/api/2/search?jql=…&startAt=…`.
  At most 200 issues a poll; the rest wait for the next one.
- Fields: `summary,status,labels,components,reporter,issuetype`. Strictly validated like §39.4;
  `labels` an array of at most 100 strings, `reporter` `{accountId}` (Cloud) or `{name}` (DC).
- Same client rules as the read (§39.3, §39.8): no redirects, 1 MiB per page, retries for
  408/429/5xx, redaction. A failed poll is a log line and the next poll waits twice as long
  (up to 1 h); 401 stops the listener (§10).

### 3.2 When

Only while no child runs. A poll that dispatched a ticket is followed by an immediate poll after
the ticket finishes; otherwise the listener sleeps `poll_interval`.

### 3.3 Go re-checks what JQL filtered

The search is a filter, not a boundary. Each issue is used only if, in Go: its project is in
`query.projects`, it has the label exactly, its status name equals one of `query.statuses`
(case-insensitive), its reporter is in `query.reporters`, its issue type is not in
`story.jira_issue_types`, and its state (§9) has no `next_try_at` in the future. An Epic-type
issue: comment "plan it as a story" + blocked (D18; story mode is a later task in TODO.md).

## 4. Claim

- The transition `start` from the issue's current status (`GET …/transitions`, then `POST`;
  the existing `jira.Client.Transitions/Transition`). The workflow allows it only from the
  queue's status, so a second listener, or a person who moved the ticket meanwhile, makes it
  fail: the issue is skipped with a log line. This is the only lock in Jira.
- Before the claim the state records `{key, phase: claiming}`; after it `phase: claimed`.
- Assigning the ticket to the bot is not done (needs an edit permission; later).

## 5. Service and project

From the ticket, never from a model:

- **Service**: every label `service:<name>`, and every component whose name equals (fold case)
  a directory under `services_dir`. Exactly one distinct name, valid by `story.ValidRepo`;
  none or several → comment ("name the service with a label service:<name>"; the names found)
  + blocked. The description is not read for the service (D4).
- **Project**: at most one label `gitlab:<group>/<path>`, matching
  `^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`, no `.`/`..` segment, `.git` suffix stripped; several
  → blocked.
- **The directory exists** (`<services_dir>/<service>`): it must be a git work tree's top level.
  With a project label, `origin`'s URL must name that project on `gitlab.base_url` (normalised:
  scheme, host, port, path, optional `.git`); a mismatch → comment + failed. Without a label it
  is used as is: the operator put it there.
- **It does not exist**: a project label is required (blocked without one) and the project must be
  under one of `gitlab.allowed_groups` (whole segments: `backend` allows `backend/x`, not
  `backend-old/x`); outside → comment + blocked. Then §6.1.

## 6. Checkout

### 6.1 Clone

`git clone --no-recurse-submodules -- <base_url>/<path>.git <services_dir>/.clone-<service>-<rand>`
under `clone_timeout`, then `os.Rename` to `<services_dir>/<service>`: a cut clone never looks
like a service. A `.clone-*` directory left by a crash is removed at start. Failure: infrastructure
(§8), except an authentication answer, which stops the listener (§10).

### 6.2 The GitLab credential

The token never enters a URL, argv or `.git/config`. Every git process the listener starts, and
every child (whose push needs it), gets in its environment
`GIT_CONFIG_COUNT/KEY_n/VALUE_n` = `credential.<gitlab.base_url>.helper` →
`!<listener executable> git-credential`, a hidden subcommand that answers git's `get` with
`username` and the token from `gitlab.token_env`, only when git asks for exactly that protocol
and host, and says nothing otherwise. Provider CLIs never see the token: the child registers
the names the task input lists (§7.2) with `ScrubCredentials`, and `test.command` has
`TestEnv`'s allowlist. The child's environment has no Jira variables at all.

### 6.3 Fresh base

Before each run: `git fetch origin`, the default branch from `origin/HEAD`, the checkout must be
on it and clean (`git status --porcelain` after the fingerprint, §38.3), then `git merge
--ff-only origin/<default>`. Not on it, dirty, or diverged → comment ("the service checkout is
not on a clean <default>") + failed: the listener owns the services directory, nobody works in
it by hand (D16). The run's worktree is then based on the fresh head (MVP.md §47).

### 6.4 The service's config

`<service>/orchestrator.yaml` when it exists: the repository's own rules (test command, checks,
protected paths, routes, limits) and its instruction files apply (D7). Otherwise
`listener.default_config` is passed as `--config` (D6); without one → comment + failed. The child
runs the whole fail-fast chain; a config error there is the service's fault (§8).

## 7. The task

### 7.1 Rendering

`jira.Fetch` (gaining `status`, `labels`, `reporter`, fields as configured) and
`jira.Render` as today (§39.5), plus one new section after the description:

```text
Discussion since the orchestrator's questions:
--- orchestrator, 2026-10-29 10:02
<the bot's last question comment, as posted>
--- comment, 2026-10-29 11:40
<a person's answer>
```

- Comments: `GET /rest/api/2/issue/{key}/comment?orderBy=created`, paged, at most 100. The bot is
  the account `GET /myself` answered at start; its comments carry the first line
  `[orchestrator]`. The section holds the bot's **last** comment that asked questions (§8) and
  every comment after it; none such: no section. Authors are not named; mentions render as
  today.
- Longer than `jira.max_bytes` → comment ("the ticket and its discussion are longer than … ;
  shorten it") + blocked.

### 7.2 `run --listener-task <file>` (hidden)

Like `--story-task`: a task input file written by the listener under
`.orchestrator/listener/tasks/<KEY>.<attempt>.json`, read with a size cap and validated strictly:
`{v: 1, key, url, fetched_at, description, scrub_env: [names]}`. The child:

- records `task.source {kind: jira, key, url, fetched_at, listener: true}` and
  `task.description` = the rendered text; builds no Jira client and needs no `jira` section;
- runs the clarify step as any run (unlike a story task); `jira.write` is off
  (`options.jira_write` empty) whatever the service's config says (D14);
- refuses `--stage`, `--jira`, `--story-task`, task text, `--inplace` with it.

`--output json` gains `failure_kind` (`auth` for `FailureAuth` and a rejected decision key, else
`""`): a compatible addition, `v` stays 1.

## 8. Outcomes

The child's exit code and JSON line, then `run.json` (loaded with the persistence validators) for
the details. Every comment starts `[orchestrator]`, is Go's wiki markup with model text escaped
(the §39.12 escaping), bounded to 8 KiB, and ends with `Run <run-id>`.

| child | comment | transition | attempt counted |
|---|---|---|---|
| 0, `mr.state: created` | merge request link, commit header, review, tests, cost (`MergeRequestComment`) | `done` | yes |
| 4, `task needs input` | the open points from `task.clarification`, numbered; "answer in a comment and move the ticket back to <status>" | `blocked` | yes |
| 4, any other reason (red tests, red pre-run suite, `max_steps`, budget, no progress, path rule) | the failure reason and the last test or check that failed | `failed` | yes |
| 6 (review fail, no MR, push refused, branch exists) | the verdict and its issues, or why there is no MR | `failed` | yes |
| 2 with `failure_kind: auth` | none | `retry` | no; the listener stops (§10) |
| 2 otherwise (the service's config, dependencies) | the error as printed (already redacted) | `failed` | yes |
| 1 | the error | `failed` | yes |
| 3 (a provider failed), a clone or fetch that failed on the network | none | `retry`, `next_try_at` with backoff from `poll_interval`, doubling, at most 1 h | no |
| 5 / killed (the listener is stopping) | none | none: stays `start`, resumed on restart (§9) | no |

- A ticket blocked before the run (service, project, size, Epic) counts no attempt.
- **Attempts**: `max_attempts` finished runs per ticket (state, §9). One more pick of a spent
  ticket: comment "N attempts spent; remove PAY-12 from .orchestrator/listener/state.json to
  try again" + `failed`, once.
- **Before the final transition** the status is read again (`jira.Client.Status`): no longer the
  `start` status (a person moved it during the run) → the comment is posted, the transition is
  not (D13).
- Writes follow §39.12: an intent in the state before each request, the id or `done` after it,
  never repeated once it may have reached Jira; a failed write is a log line, not a retry loop.

## 9. State and restart

`.orchestrator/listener/state.json` in the working directory, owned by one goroutine,
written atomically, flocked through `.orchestrator/listener/lock` (a second listener in the same
directory refuses at once). Per key: `attempts`, `service`, `phase`
(`claiming|claimed|running|finished`), `run_id`, `run_dir` (derived from the service and the id,
checked on load, never trusted as a path), `outcome`, `writes {comment, transition}`,
`next_try_at`. Strict validation on load; a corrupt file stops the listener (exit 2).

At start, for every key not `finished`:

- `claiming`: read the status; in `start` → treat as `claimed`; else drop the entry.
- `claimed`, no run yet: transition `retry`, no attempt counted.
- `running`: the run's `run.json` non-terminal → `orchestrator resume <id> --mode auto --output
  json` in the service directory (D15), then §8; terminal → §8 from `run.json` (exit code derived
  from `outcome`), the writes not yet done only.
- An intent without its result: not repeated, a log line.

## 10. Stopping

- SIGTERM/SIGINT: no new poll or claim; a running child gets SIGINT (`runChild`) and up to
  `childWaitDelay`; the state is saved; the ticket stays in `start` and is resumed on restart.
  The Docker stop timeout should exceed `childWaitDelay` (README).
- Stop with exit 2, after saving the state and putting a claimed-but-not-run ticket back to
  `retry`: Jira 401 on any call, `failure_kind: auth` from a child, a clone that git says was
  refused for authentication.

## 11. Security

- **Who can start a run**: only reporters in `query.reporters` (D19), re-checked in Go; the
  label alone is not enough.
- **What gets cloned**: only `gitlab.base_url` and `allowed_groups`; the ticket names the path,
  the config bounds it. A cloned repository's `orchestrator.yaml` and `test.command` are trusted
  because its group is: say so in the README. The default config applies to repositories
  without one.
- **Credentials**: Jira and GitLab tokens env only, never in argv, URLs, `.git/config`, logs,
  state, comments; the child gets no Jira variables; provider CLIs and the test command get no
  token (§6.2).
- **Comments**: Go's template; the only model text is the clarify points and review issues,
  escaped, bounded, secret-scanned (a finding → the point replaced by "(withheld: looked like a
  secret)").
- Ticket and comment text is task text (§38.5): no authority from it.

## 12. Changes to MVP.md and CLAUDE.md

- §30 / CLAUDE.md "Out of scope": Jira gains a JQL search, reading comments and `/myself`, and
  comments and transitions without a merge request — for the listener only. GitLab gains a
  token, used only by the git credential helper for clone, fetch and push to `gitlab.base_url`;
  still no API client (§50.13 amended).
- §39.10: comments as a section — for the listener's tasks only.
- New §51 (the listener) and an invariant paragraph in CLAUDE.md: "the listener owns Jira; the
  child never calls it".
- §50.10: `failure_kind`; §4 exit codes unchanged.

## 13. Not covered (later)

Several tickets at once; assigning the ticket; tickets for every reporter; story mode for Epics
and multi-service tickets; webhooks instead of polling; a command to reset attempts; a daily
budget (D21: not wanted).

## 14. Decisions (owner, 2026-10-29)

| | decision |
|---|---|
| D1 | A separate command, `orchestrator jira listen` |
| D2 | GitLab credentials from environment variables (`gitlab.token_env`) |
| D3 | The services directory defaults to the working directory (Docker) |
| D4 | Service and project from labels and components only, not guessed from the description |
| D5 | An allowlist of GitLab groups and Jira projects |
| D6 | A service without `orchestrator.yaml` runs with a default config |
| D7 | The claim moves the ticket to In Progress |
| D8 | A failed run → a `failed` status from the config, with a comment |
| D9 | Exit 6 → `failed` too, with a comment |
| D10 | Infrastructure failures retry later; an auth failure stops the listener |
| D11 | The comments after the bot's questions are part of the next run's task |
| D12 | A limit of attempts per ticket |
| D13 | A run is finished even if the ticket changes; the status is checked before the transition |
| D14 | The listener moves the ticket after the MR, not the child's `jira.write` |
| D15 | A restart resumes the interrupted run |
| D16 | Fetch and base on `origin/<default>`; the services directory is the listener's alone |
| D17 | One ticket at a time |
| D18 | Epics and multi-service tickets → blocked; story mode later (TODO) |
| D19 | A reporter filter for now; everyone later |
| D20 | Poll only while idle |
| D21 | No daily budget |

## 15. Author's defaults (confirmed by the owner, 2026-10-29)

| | default |
|---|---|
| A1 | The child gets the ticket through a task file (`--listener-task`), not `--jira`: the service configs need no `jira` section and the child makes no Jira call |
| A2 | The GitLab token reaches git only through a `git-credential` helper scoped to `gitlab.base_url` |
| A3 | Attempts live in the local state file; the working directory must be a volume |
| A4 | Blocks before a run count no attempt; finished runs do |
| A5 | The bot's comments start with `[orchestrator]`; the bot is the `/myself` account |
| A6 | Comment authors are not named in the task text |
| A7 | No assignment of the ticket to the bot |
| A8 | Cloud uses `/search/jql` with `nextPageToken`, Data Center `/search` with `startAt`, chosen by `jira.auth` |
