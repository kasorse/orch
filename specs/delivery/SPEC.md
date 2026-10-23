# Spec: Delivery — from a finished run to a merge request, in a terminal or in CI

Status: **specified** (2026-10-23), nothing built. The owner's decisions are in §16 (taken
2026-10-23 while scoping; D12–D15 the same day, on the branch name), the author's defaults in §17. TODO.md phase 3: W1 (the rest), W2, W3,
W4, W5. Builds on `specs/worktree/` (§47) and `specs/story/` (§48), both done.

The spec touches these MVP.md sections:
- §4: CLI (`--mr`, `--output json`, `orchestrator review`, the exit code table);
- §30: out of scope (GitLab: one push, one merge request, one note per run);
- §39: Jira (a comment and a transition after a merge request, §39.12);
- §42: the commit message (a Conventional Commit type, the `Orchestrator-Decision` trailer);
- §43: `report.md` next to `summary.md`;
- §47.2: the branch name (instructions, a template, then `orchestrator/<run-id>`);
- §47.7 and §48.10: pushing and merge requests are no longer "not covered";
- new §49: delivery.

---

## 1. Problem

A run ends with a commit on `orchestrator/<run-id>` (§47) and a `summary.md` full of metrics
(§43). From there the operator still does everything by hand: pushes the branch, opens a merge
request, writes its description, links it in Jira and moves the ticket. A CI job cannot run the
orchestrator at all without guessing which output is the result and which exit code means what.
And the cheapest way into a team, a review of a change somebody else wrote, needs uncommitted
changes in the current tree (§40.4), so it cannot review a merge request.

1. **No reviewer-facing report.** `summary.md` answers "what did it cost and how long did it
   take". A reviewer asks "what was the task, why these stages, what changed, did the tests and
   the review pass".
2. **No push, no merge request** (TODO W2). GitLab is out of scope (§30).
3. **No write-back to the ticket** (TODO W3) beyond a story's child issues (§39.11).
4. **No contract for a machine caller** (TODO W4): output is for a person, exit codes are not
   documented as an interface, and a run without a terminal and without `--mode` waits on a
   prompt that reads EOF.
5. **No review of two refs** (TODO W5): `run --stage review` reviews the working tree against
   `HEAD` only (§40.6 lists `--base` as not covered).

## 2. What already exists

- **The commit**, by Go with no model call (§42.2): `summary_short` cut to 72 runes, `KEY: ` for a
  Jira task, `Orchestrator-Run` and `Orchestrator-Story` trailers. Automatic in a worktree run under
  the §42.6 rules (no commit on a warning, without `summary_short`, or on `approval: manual`).
- **The branch** `orchestrator/<run-id>`, or `story/<slug>/<ID>` for a story task, its
  `base_branch` and a merge-tree conflict check (§47.5). A story already prints, per task, "the
  branch its merge request targets" (`story_run.go`, `finish`).
- **Everything a report needs is in `run.json` and `decision.json`**: the decision and every
  stage's reason, the last test result with checks and failing test names, the review's verdict,
  issues and criteria, path rules, secret findings (never a value), cost, time, net diff counts.
  `internal/report` renders `summary.md` from them (imports only `domain`).
- **A strict HTTP client pattern**: `internal/jira` - unexported wire types, env-only credentials,
  redaction, no redirects, retry only on 429/503 with `Retry-After`, `MayExist` for a write that
  may have reached the server, an intent recorded before each write (§48.7).
- **Unattended runs**: `--mode auto` asks nothing; `auto_commit`; `on_failure: ask` fails without
  a terminal; `internal/exitcode` maps errors to 0–5 in one place.
- **A detached worktree is cheap**: §47 creates, guards, sets up and removes worktrees; the review
  diff (`Repository.ReviewDiff`) is bounded, redacted and runs no diff drivers.

## 3. Principles

1. **Go writes everything a person reads**: the commit message, the report, the MR title and
   description, the Jira comment. A model proposes at most one closed-list word (the commit type)
   and the `summary_short` it already writes.
2. **No delivery without the commit.** Push and merge request happen only after the §42.6 /
   §47.5 rules committed; they never relax those rules (D7).
3. **Writes outside the machine happen once, after an intent, never retried blindly** - the
   §48.7 discipline for every GitLab and Jira write.
4. **Requests go only to the configured host.** The GitLab project comes from the git remote
   (D3), but the token is sent only to `gitlab.base_url`, and the remote must point at that host.
5. **Only the run's own branch is pushed**, never with force, never the operator's branch.
6. **The exit code and one JSON line are the machine interface**; everything else is for a
   person and goes to stderr under `--output json`.
7. **Review-only is a run**: same store, decision record, guard, report and resume; it just has no
   `implement` and reviews `base...head`.

---

## 4. The commit message and the branch name (W1)

### 4.1 Conventional Commit type

The implement answer gains one field:

```json
{ "summary": "...", "summary_short": "...", "commit_type": "fix", ... }
```

The prompt rule (next to `implementSummaryShortRule`):
`"commit_type" is one of feat, fix, refactor, perf, test, docs, build, ci, chore: the kind of the
whole change.`

Go reads it from the same implementation `summary_short` comes from (§42.2: the first that has
one), trims and lower-cases it, and accepts it only from that closed list. Anything else, or no
field, means no type: the header stays as today. The artifact is never failed for it.

Header, before the 72-rune cut:

| type | Jira key | header |
|---|---|---|
| `fix` | — | `fix: Use + in Add` |
| `fix` | `PAY-12` | `fix: PAY-12 Use + in Add` |
| none | `PAY-12` | `PAY-12: Use + in Add` (unchanged) |
| none | — | `Use + in Add` (unchanged) |

The key moves after the type so the header stays a valid Conventional Commit and the GitLab–Jira
integration still finds the key. `e` on the question (§42.2) replaces the whole header as typed.

### 4.2 `Orchestrator-Decision` trailer

After `Orchestrator-Run` (and before `Orchestrator-Story`):

```
Orchestrator-Run: 20261023-101500-ab12cd
Orchestrator-Decision: medium/analysis,planning,development,testing,review
```

`<complexity>/<enabled stages in stage order>`; `manual/<stages>` for `run --stage` and a review
run. Go builds it from `run.Decision`.

### 4.3 The branch name (D12–D15)

The branch of a worktree run is named **when the worktree is created** (§47.2: after the
approval, before the restore point and any step) and never changes. Today it is always
`orchestrator/<run-id>`. Now the first of these that gives a valid name wins:

1. **The repository's instructions** (D13, D14): what CLAUDE.md, AGENTS.md or CONTRIBUTING.md
   say about branch names, read by a model;
2. **the config template** `worktree.branch.template`;
3. `orchestrator/<run-id>`, as today.

```yaml
worktree:
  branch:
    template: "feature/{key}-{slug}"   # optional
    from_instructions:                 # optional; absent = instructions are not read
      provider: claude
      model: haiku
      timeout: 60s
```

**The instructions call.** It is made only when `from_instructions` is set, the run is a worktree
run, the workflow has `implement`, and at least one of `CLAUDE.md`, `AGENTS.md` and
`CONTRIBUTING.md` at the repository root of `base_ref` (read with `git show <base_ref>:<file>`,
not from the working tree, which the run does not use) contains `branch` or `ветк`
(case-insensitive). Otherwise nothing is called. One cold, tool-less call:

- authority `none` (like the translator and an LLM decision provider: never chosen by config,
  §38), the prompt on stdin, `limits.prompt`-style bounds: the three files together at most
  32 KiB (each cut at a line), fenced as repository data with `action.fence`;
- input: those files, the task's first line (≤ 200 runes, through `action.taskBlock`), the Jira
  key when there is one, the run ID, today's date;
- the asked answer: `{"branch": "<name>"}`, or `{"branch": ""}` when the instructions set no rule
  for branch names;
- strict JSON, anything else is `InvalidResponse`. Any failure, an empty answer or a name that
  fails the checks below prints `Warning: branch name from the instructions not used: <why>`
  and falls through to the template. `FailureAuth` stays fatal, as in every other call (§46.5).
- Recorded: `run.json: branch_naming {source, proposed?, rejected?, usage, call}`;
  `logs/branch-name.{stdout,stderr}.log`; its cost counts in `outcome.usage` and in
  `decision_overhead` (it is spent before the workflow runs).

**The template** is rendered by Go, with no model:

| placeholder | value |
|---|---|
| `{id}` | the run ID |
| `{key}` | the Jira key of a `run --jira` task; empty otherwise |
| `{slug}` | the task's first line: Cyrillic transliterated (a fixed table in Go), lower case, every run of characters outside `[a-z0-9]` → `-`, trimmed, at most 40 bytes cut at a `-` |
| `{date}` | `YYYYMMDD`, local time |

An empty placeholder takes the separator after it (`-`, `_`, `/`) with it:
`feature/{key}-{slug}` gives `feature/PAY-12-use-plus-in-add` or `feature/use-plus-in-add`. An
unknown placeholder is a config error. `{type}` is not offered: the commit type exists only after
implement, and the name is fixed before it (D14).

**The checks**, for both sources, in Go:

- `git check-ref-format --branch`; only `[A-Za-z0-9._/-]`; at most 100 bytes; no leading `-`
  or `/`, no `//`, no `..`, no `.lock` suffix;
- not `HEAD`, not the base branch, not `gitlab.target_branch`, not under `story/` (reserved for
  §48), and not an existing local branch (`refs/heads/<name>`);
- an existing branch with the template's name gets `-<last 6 of the run ID>` appended once, then
  is checked again; a model's name that exists is rejected (to the template).

**Where it applies**: worktree runs only. A story task keeps `story/<slug>/<ID>` (D15): its name is
computed before the run and stacking and `--retry` rely on it; `--story-task` ignores
`worktree.branch`. A review run (§12) is detached and has no branch.

**Recording and resume**: the name is in `run.json: worktree.branch`, as today. It is no longer
derived from the run ID, so a resume checks it instead: the §4.3 character and format rules, and
`git worktree list` showing the derived path on exactly that branch (the check §47's resume
already makes). The path stays derived from the run ID.

**Push** (§8): before pushing, `git ls-remote --heads <remote> <name>` must show nothing. A branch
of that name on the remote is never pushed to, even as a fast-forward: `mr.error: the branch
<name> already exists on <remote>`, exit 6. A name from a model, or a template without `{id}`,
could otherwise move somebody else's branch.

## 5. `report.md`

Written next to `summary.md` whenever a run reaches a terminal status, by Go from `run.json` and
`decision.json`, never read back, never a resume input; failing to write it fails nothing
(§43.2's rules). It is the MR description (§8) and the MR note of a review run (§12.5).
`summary.md` keeps the metrics; `report.md` is for the reviewer.

```markdown
## fix: PAY-12 Use + in Add

**Status:** completed · **Review:** pass (2 low) · **Tests:** passed · **Checks:** vet ✓ lint ✓

### Task
> PAY-12: Add returns the difference
> ...                                   (first 30 lines, then "…(N more lines)")
Source: https://jira.example.com/browse/PAY-12

### Why these stages
complexity **medium** (size 0.84; cuts 0.60 / 1.30) · decided by llm (haiku)
| stage | on | why |
|---|---|---|
| analysis | ✓ | 0.91 ≥ 0.81 |
| planning | ✓ | template for medium |
| development | ✓ | 0.97 ≥ 0.81 |
| testing | ✓ | with development |
| review | ✓ | template for medium |

### What changed
3 files, +42 −7 against `main@1a2b3c4`
| file | + | − |
|---|---|---|
| calc/add.go | 3 | 1 |
...                                   (at most 50 rows, then "and N more")

### Tests and checks
last test step: passed (`go test ./...`), checks vet, lint passed.
Fix iterations: 1 (the first test step failed: TestAdd).

### Review
verdict **pass** · criteria 3/3 met
- low · calc/add.go:12 · Comment is stale
...                                   (at most 30 issues, most severe first)

### Needs attention
- policy.paths requires manual approval: migrations/** (2 files)
- a likely secret was added at cfg.go:3 (aws-access-key)

---
Run `20261023-101500-ab12cd` · branch `orchestrator/20261023-101500-ab12cd` · restore point
`refs/orchestrator/runs/20261023-101500-ab12cd` · cost $0.84 · 6 min active · orchestrator v1.4.0
```

Rules:
- Sections without content are left out ("Needs attention" usually is).
- The file list needs data `run.json` does not hold today: `executions[].diff.run_files` (§13), up
  to 200 `{path, insertions, deletions, binary}` of the run's net change, from the same
  `git diff --numstat` that already gives `diff.run` (an implement the guard passed); the report
  reads the last implement's list, as `outcome.diff` is the last implement's `diff.run`.
- **Untrusted text is neutralized.** Task text, ticket text and every model-written string are
  escaped before they reach Markdown: raw HTML (`<`, `>`, `&`), link and image brackets, and the
  GitLab references that notify or link (`@user`, `#123`, `!123`, `~label`, `%milestone`)
  are backslash-escaped. A model cannot put a link, an image, a mention or a quick action
  (`/merge` at the start of a line) into an MR. Lines that start with `/` are indented by one space.
- Bounded at 60 000 bytes (lists are cut first, then the task quote); GitLab's limit is 1 MiB.
- A review run (§12) has "Reviewed" (`base...head`, the MR) in place of "What changed", and no
  "Why these stages".

## 6. GitLab: configuration, project, client

### 6.1 Configuration

```yaml
gitlab:
  base_url: https://gitlab.example.com   # https; http only for localhost (jira.base_url's rule)
  token_env: GITLAB_TOKEN                # required; a literal `token` is a config error
  remote: origin                         # the remote that is pushed to and names the project
  target_branch: main                    # only when the run has no base branch (detached HEAD)
  draft: true                            # default true: the title gets "Draft: "
  labels: [ai-generated]                 # optional
  timeout: 30s                           # one HTTP attempt
  max_retries: 2                         # only 429/503 with Retry-After
```

Strict schema, defaults for absent keys, `token_env` registered in `Config.CredentialEnvNames` (so
it is scrubbed from every provider CLI and test process; git keeps the full environment).
Checked only when something needs GitLab (`DependencyOptions{GitLab: true}`: `run --mr`,
`story run --mr`, `review --mr`, and `validate` when the section is present).

### 6.2 The project comes from the remote (D3)

`git remote get-url <remote>` (which applies `insteadOf`) is parsed in Go:

| remote URL | host | project |
|---|---|---|
| `git@gitlab.example.com:group/sub/app.git` | gitlab.example.com | `group/sub/app` |
| `ssh://git@gitlab.example.com:2222/group/app.git` | gitlab.example.com | `group/app` |
| `https://gitlab.example.com/group/app.git` | gitlab.example.com | `group/app` |
| `https://oauth2:TOKEN@gitlab.example.com/group/app` | gitlab.example.com | `group/app` |
| `https://gitlab.example.com/gitlab/group/app.git` with `base_url` `…/gitlab` | gitlab.example.com | `group/app` |

- The host must equal `base_url`'s host (case-insensitive; the ssh port is ignored). Otherwise:
  config error `gitlab.remote origin points at github.com, not gitlab.example.com (gitlab.base_url)`.
  The token is never sent anywhere else.
- Userinfo is dropped before anything is printed or recorded.
- `.git` is stripped; for http(s), `base_url`'s path prefix is stripped; the rest must match
  `^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`. `file://`, a local path and anything else are config
  errors.
- The project path is URL-encoded as the `:id` of every API call.

### 6.3 The client (`internal/gitlab`)

Same rules as `internal/jira`: imports only `domain`, `apperr` and the standard library;
unexported wire types; `PRIVATE-TOKEN` header from the constructor only; the token redacted from
every error; no redirect followed; response bodies read up to 1 MiB; every answer validated
strictly (`InvalidResponse` naming the JSON path); retries only on 429/503 with a `Retry-After`
that fits; `gitlab.MayExist(err)` for a write.

| call | request | answer validated |
|---|---|---|
| `Project` | `GET /api/v4/projects/:id` | `id` > 0, `path_with_namespace` equal to the one asked (case-insensitive) |
| `OpenMR` | `GET /projects/:id/merge_requests?state=opened&source_branch=B` | array of MRs (below); more than one is an error |
| `CreateMR` | `POST /projects/:id/merge_requests` | 201, MR |
| `MR` | `GET /projects/:id/merge_requests/:iid` | MR with `diff_refs` |
| `CreateNote` | `POST /projects/:id/merge_requests/:iid/notes` | 201, `id` > 0 |

MR: `iid` > 0, `web_url` https on `base_url`'s host, `source_branch` / `target_branch` equal to the
ones sent (on create), `state`, `source_project_id`, `target_project_id`, and for `MR` also
`title`, `description` (string or null) and `diff_refs.{base_sha,head_sha,start_sha}` (40 hex).

Minimum GitLab: API v4, 15.0 (`Draft:` title prefix, `refs/merge-requests/<iid>/head`). The live
check is on gitlab.com (D10, §15).

## 7. Fail-fast for GitLab

With `--mr` (any command), after the whole §7 chain (`ValidateDependencies` with `GitLab: true`
checks the section, the token variable, the remote and its host) and before any decision call or
run directory:

1. `GET /projects/:id`: a wrong token (401), no access (403/404) or an unreachable host is a
   `ConfigError` (exit 2) before anything is spent. Like `jev.CheckModel`.
2. `run --mr` also needs `security.secret_scan.mode` other than `off`: a push publishes the tree,
   so the scan that stops a secret before the commit must be on (A3).

## 8. `run --mr` (W2)

```bash
orchestrator run --mr "fix the rounding in Add"
orchestrator run --mr --jira PAY-12 --mode auto --output json
```

- `--mr` makes the run a worktree run (`--worktree` implied, D2); with `--inplace` it is a usage
  error. Valid with `run` and `story run` (and `review`, §12.4); `resume` takes the choice from the
  run (`options.mr`), never from a flag.
- A workflow without `implement` prints `No merge request: the workflow has no implement` and
  does nothing else.
- **After the commit and the conflict check, before the worktree is removed** (§47.5):
  1. **No commit, no MR.** When §42.6 / §47.5 did not commit (a review that failed, a secret
     finding, no `summary_short`, `approval: manual`) or the operator answered no, the run records
     `mr.skipped: <why>` and prints `No merge request: <why>`. Exit code 6 (§11.3).
  2. **Push.** The operator repository's git fingerprint is compared with the one taken when the
     worktree was created (as before merge-tree; a pre-push hook runs), the remote must not have
     the branch yet (`git ls-remote`, §4.3), then
     `git push --no-follow-tags <remote> refs/heads/<branch>:refs/heads/<branch>` from the
     operator's repository, with the full environment (the operator's own credentials), no
     `--force`, bounded by a fixed 5 minutes. A rejected or failed push: `mr.error`, the message,
     and `To push it yourself: git push <remote> <branch>`. Exit code 6.
  3. **The intent.** `run.json: mr {state: intent, project, source, target}` is saved.
  4. **One MR per branch.** `OpenMR(source)`: one found is recorded (`state: found`) and reused
     as it is (no update). Otherwise `CreateMR`: title = the commit header, prefixed `Draft: ` with
     `gitlab.draft`; description = `report.md` rendered for this run after the commit; `labels`;
     `remove_source_branch` false. 201 → `state: created`, `iid`, `url`.
  5. A create that may have reached GitLab (`MayExist`) keeps the intent with `mr.error` and is
     never repeated by this run. Exit code 6.
- Target branch: the run's `worktree.base_branch`; when it is empty (detached HEAD, a CI checkout),
  `gitlab.target_branch`; with neither, `--mr` is a config error before any call.
- Output: `Merge request !12: https://gitlab.example.com/group/app/-/merge_requests/12`.
- `summary.md` gains a "Merge request" row; `report.md` is the description and is not changed
  afterwards.
- A resume that completes the run does the same with `options.mr` (it builds a GitLab client
  only then). A completed run is not resumable (§37), so a failed push or MR is finished by hand.

## 9. `story run --mr`

- `story run --mr` checks every service with `orchestrator validate --mr` (the check `story run`
  already makes per service before the first dispatch, §48.6) - a service without a `gitlab`
  section stops the story before anything runs.
- Every child gets `run --story-task <file> --mr`; its branch is `story/<slug>/<ID>`, its target
  the branch its base came from: the dependency's branch, else the story's base branch - exactly
  the "target" `story run` prints today. The dependency's branch is on the remote by then (its own
  child pushed it); GitLab retargets the stacked MR when the parent MR merges and deletes its
  branch.
- `story.json` records each task's `mr` (url, iid) read from the child's `run.json`; `story status`
  and the end table print it. A child that ends with exit 6 (no MR) is `completed` without an MR:
  the story **stops** before the next task that depends on it (its branch is not on the remote),
  and continues independent tasks. `story run --retry <ID>` works as today.

## 10. Jira write-back (W3)

```yaml
jira:
  write:
    comment: true             # default false
    transition: "In Review"   # optional; the transition's name or its target status name
```

- Only for a task with a Jira key (`run --jira`: the issue; a story task: its child issue, §48.7)
  and only after an MR was created or found in this run. Same `jira.base_url`, credentials,
  redaction, no-redirect and strict rules as §39 and §39.11 (D9: the same token).
- **Comment**: `POST /rest/api/2/issue/{key}/comment`, wiki markup rendered by Go:
  ```
  Merge request [!12|https://gitlab.example.com/group/app/-/merge_requests/12]: fix: PAY-12 Use + in Add
  Review: pass · Tests: passed · Cost: $0.84 · Run 20261023-101500-ab12cd
  ```
  An intent before, the id after the 201, never repeated once it may have reached Jira.
- **Transition**: `GET /rest/api/2/issue/{key}/transitions`, the one whose `name` or `to.name`
  equals the configured value (case-insensitive), `POST …/transitions {"transition":{"id":…}}`
  (204). None matches: `Warning: PAY-12 has no transition "In Review" (available: Start, Done)`.
  The issue already in that status: nothing is sent.
- A failure is a warning; the run's status and exit code do not change (the MR is the result).
- `jira.write` is copied into `options.jira_write` at run start; a resume uses that copy. Resume
  still never *reads* Jira (§39): it builds a Jira client only for this write.
- `run.json: jira_write {comment: {state, id}, transition: {name, state}}`.

## 11. Headless and CI (W4)

### 11.1 No terminal

`run`, `resume`, `review`: stdin not a terminal and no `--mode` → usage error at once, before the
fail-fast chain's calls:
`stdin is not a terminal: pass --mode auto (or plan, accept-edits) to run without questions`.
`--mode manual` without a terminal is the same error. Today such a run reads EOF at the approval
prompt.

### 11.2 `--output json`

Valid with `run`, `resume`, `review`. Every human-readable line goes to stderr (plain reporter,
never the pretty one); stdout gets exactly one line when the process exits:

```json
{"v":1,"run_id":"20261023-101500-ab12cd","status":"completed","exit_code":0,
 "failure_reason":"","workflow":["analyze","plan","implement","test","review"],
 "commit":"<sha>","branch":"orchestrator/20261023-101500-ab12cd",
 "mr":{"iid":12,"url":"https://…/-/merge_requests/12"},"mr_skipped":"",
 "review_verdict":"pass","tests_passed_final":true,"approval_required":false,
 "cost_usd":0.84,"cost_partial":false,
 "run_dir":".orchestrator/runs/20261023-101500-ab12cd","report":"…/report.md"}
```

- An error before a run exists: `{"v":1,"run_id":"","status":"error","exit_code":2,"error":"…"}`
  (the message as printed, already redacted).
- Fields that do not apply are empty or null, never absent. `v` changes only on an incompatible
  change.

### 11.3 Exit codes (documented as an interface in MVP.md §4)

| code | meaning |
|---|---|
| 0 | completed, and what was asked was delivered |
| 1 | a run that cannot be used, an unexpected error |
| 2 | config, dependency, usage, credentials (`FailureAuth`), GitLab/Jira fail-fast |
| 3 | a provider failed |
| 4 | the workflow failed or a limit stopped it (a red test, `max_steps`, budget, no progress) |
| 5 | cancelled by the operator |
| **6** | **completed, but needs a person**: `--mr` produced no MR (skipped or failed), or `orchestrator review` gave a failing verdict (A6) |

### 11.4 Docker image and CI example (D8)

- `Dockerfile` (repository root): build stage `golang` → runtime `debian:bookworm-slim` with git,
  bubblewrap, ca-certificates, Node.js and `@anthropic-ai/claude-code@<version>`, the version an
  `ARG` defaulting to the newest one inside the claude adapter's verified range (§41.4); a
  non-root user; `ENTRYPOINT ["orchestrator"]`. No image is published; teams build it.
- bwrap inside a container needs unprivileged user namespaces (Docker's default seccomp profile
  and Ubuntu's AppArmor userns restriction block them). The fail-fast sandbox probe already refuses
  with a clear message; `examples/ci/README.md` names the runner settings that allow it. The
  example never sets `test.sandbox: none`.
- In CI only `--claude-auth api` (`ANTHROPIC_API_KEY`, §46; TODO P6).
- `examples/ci/.gitlab-ci.yml`:
  - `ai-implement`: manual, or a pipeline variable `JIRA_KEY` →
    `orchestrator run --jira "$JIRA_KEY" --mr --mode auto --output json > result.json`;
    `result.json` and the run directory as artifacts.
  - `ai-review`: on `merge_request_event` →
    `orchestrator review --mr "$CI_MERGE_REQUEST_IID" --comment --mode auto --output json`.
  - Push credentials through a credential helper in the job's global git config that reads
    `$GITLAB_TOKEN` from the environment, never a token in the remote URL inside the repository
    (a URL in `.git/config` is readable to an `inplace` implement's file tools).

## 12. `orchestrator review` (W5)

### 12.1 CLI

```bash
orchestrator review --base main                       # main...HEAD
orchestrator review --base v1.3.0 --head feature/x
orchestrator review --base main --task "the new cache must not change the API"
orchestrator review --base main --jira PAY-12         # the ticket is the task
orchestrator review --mr 12 --comment                 # §12.4
```

- Exactly one of `--base` and `--mr`; `--head` only with `--base` (default `HEAD`); `--comment`
  only with `--mr`. `--mode`, `--output json`, `--config`, `--claude-auth`, `--codex-auth` as for
  `run`. Not `--stage`, `--mr` with `--base`, `--inplace`/`--worktree`, `--allow-red-tests`.
- Refs resolve with `git rev-parse --verify --end-of-options <ref>^{commit}`; the reviewed change
  is `merge-base(base, head)..head` - three dots, like a merge request. An empty change: usage
  error `nothing to review: <head> adds nothing to <base>`.

### 12.2 The run

- A run like any other in `.orchestrator/runs/`: `decision.json` `source: manual`, stages
  `testing` and `review` (`domain.ManualDecision`), workflow `test → review`. No decision provider,
  translator or Context Manager.
- It runs in a **detached worktree at `head`** (`git worktree add --detach`, the §47.2 path for
  its run ID): the operator's tree may be dirty and is never touched; `worktree.setup` runs as in
  §47.4. The worktree is removed at the end whatever the status (nothing in it is a result); a
  removal git refuses keeps it and prints the path.
- `ReviewDiffOptions.Base` (new, default `HEAD`): the diff is `merge_base` against the worktree,
  i.e. the change under review plus anything the tests wrote. The bounds, deny list and redaction
  are unchanged.
- The `test` step runs the checks and `test.command` on `head`, sandboxed and guarded as any test
  step. A red test fails the run before the review (§11's transitions; A7). The pre-run test does
  not apply (no `implement`).
- The task: `--task`, else the `--jira` ticket, else the MR's title and description (§12.4), else
  Go writes one from the commits:
  `Review the change from <base> to <head>. Its commits:\n- <subject>…` (at most 50 subjects,
  8 KiB). Through `action.taskBlock` as every task.
- `run.json: review_target {base, head, merge_base, base_ref_name, head_ref_name, mr_iid?}`.
  `worktree.branch` is empty for a review run only.
- A verdict that fails completes the run with it, as today, and the command exits 6.
- `resume` works: same worktree (verified detached at `head`), same `review_target`.

### 12.3 Output

`report.md` (§5, the review form), `summary.md`, the console verdict and issues; with
`--output json` the one line (`review_verdict`, `mr_note`).

### 12.4 `--mr` and `--comment`

- `--mr 12` or `--mr https://gitlab.example.com/group/app/-/merge_requests/12`: the URL only
  supplies the iid; its host and project must equal `base_url`'s host and the remote's project,
  otherwise a usage error. Nothing is ever requested from a pasted URL (the `jira.ParseRef` rule).
- `MR(iid)`: an MR from a fork (`source_project_id ≠ target_project_id`) or one not `opened` is
  refused (A9).
- `git fetch --no-tags <remote> refs/merge-requests/<iid>/head:refs/orchestrator/review/<run-id>/head
  refs/heads/<target>:refs/orchestrator/review/<run-id>/base` in the operator's repository (full
  environment, the operator's credentials; the fingerprint compared before, as for push). The
  fetched head must equal `diff_refs.head_sha` (else `the merge request changed while it was being
  fetched; run again`); `base` is `diff_refs.base_sha`. The refs stay, like restore points, so a
  resume finds them.
- The task is the MR's title and description (untrusted text, fenced, bounded to 32 KiB).
- `--comment`: when the run reaches a terminal status after at least one step, `report.md` is
  posted as one note, an intent first (`run.json: mr_note {state, id}`), never twice. A failure is
  a warning.

## 13. Persistence

All optional on load, strictly validated when present.

| field | rule |
|---|---|
| `options.mr` | bool; set at run start from `--mr` |
| `branch_naming` | `{source: instructions\|template\|default, proposed?, rejected?, usage?, call?}`; `proposed` ≤ 255 printable bytes, `rejected` one line ≤ 500 bytes; absent for an inplace, story or review run |
| `worktree.branch` | no longer `orchestrator/<run-id>` only: any name that passes §4.3's character and format rules (a story task: its story pattern, as today) |
| `options.jira_write` | `{comment: bool, transition: string}`, copied from `jira.write` at run start |
| `mr` | `{state: intent\|created\|found, project, source, target, iid?, url?, skipped?, error?}`; `iid`/`url` only with created/found; `url` https; `skipped` and `error` one line, ≤ 500 bytes, redacted |
| `jira_write` | `{comment: {state, id?}, transition: {name, state: done\|missing\|failed\|unchanged}}` |
| `review_target` | `{base, head, merge_base}` 40 hex; `base_ref_name`, `head_ref_name` printable ≤ 255; `mr_iid` > 0 |
| `mr_note` | `{state: intent\|created, id?}` |
| `executions[].diff.run_files[]` | ≤ 200 `{path (clean, repository-relative), insertions, deletions, binary}`, on an implement with `diff.run` only |
| `story.json: tasks.<ID>.mr` | `{iid, url}` |

`worktree.branch` may be empty only on a run with `review_target`.

## 14. Security

- GitLab token: env-only (`gitlab.token_env`), read in `app` only, passed only to the client's
  constructor, redacted from every error, scrubbed from every provider CLI and test process; a
  literal `gitlab.token` is a config error naming the key. Never in argv, config, `run.json`,
  logs, `report.md`.
- Requests go only to `gitlab.base_url`; the remote must point at its host; a pasted MR URL only
  yields an iid.
- Push: only the run's own branch (`worktree.branch`, created by this run), explicit refspec, no
  `--force`, after the operator fingerprint check, and never to a branch that already exists on the
  remote (§4.3): a model-proposed or template name cannot move somebody else's branch.
- The branch-name call has authority `none`; the instruction files are read from `base_ref` with
  `git show`, bounded and fenced; whatever it answers only passes Go's name checks or is dropped. A secret finding never reaches a push: it blocks the commit
  (§42.6), and the scan cannot be `off` under `--mr`.
- Fetch: explicit refspecs into `refs/orchestrator/review/<run-id>/` only, `--no-tags`.
- Everything model- or ticket-written that reaches GitLab or Jira is escaped (§5).
- `inplace` is never used for `--mr`, so `.git/config` (with a URL that may carry a token) is
  outside the worktree's file tools (§47.3).

## 15. Live check (gitlab.com, D10)

An operator gate, not a test step (it makes real requests). A test project on gitlab.com, a token
with `api` and `write_repository` in `GITLAB_TOKEN`, a repository whose `origin` is that project:

1. `orchestrator validate` with the `gitlab` section → the project check passes; a wrong token →
   exit 2 naming `GITLAB_TOKEN`, without its value.
2. `orchestrator run --mr --mode auto --output json "<small fix>"` → a draft MR with the
   `report.md` description, `run.json: mr.state: created`, exit 0.
3. The same task with a test that cannot pass → no MR, `mr.skipped`, exit 6.
4. `orchestrator review --mr <iid> --comment --mode auto` → one note on the MR.
5. A two-task story in that repository with `story run --mr` → two MRs, the second targeting the
   first's branch.

The steps and their results go into the As built of DL04 and DL09 and the README's `Status:`.

---

## 16. Owner's decisions (2026-10-23)

| # | decision |
|---|---|
| D1 | Phase 3 in one spec: W1 (rest), W2, W3, W4, W5. GitHub stays out. |
| D2 | `--mr` implies a worktree run; `--mr --inplace` is a usage error. |
| D3 | The GitLab project comes from the current repository's git remote, not from config. (Author's addition, §6.2: the API host stays `gitlab.base_url` and the remote must point at it, so the token goes only to the configured host.) |
| D4 | `story run --mr` is in: one MR per task, stacked on the dependency's branch. |
| D5 | W5: local `--base/--head`, plus `--mr` with an optional `--comment` (one note). No inline comments. |
| D6 | A separate `report.md` for the reviewer; `summary.md` stays the metrics. |
| D7 | No commit → no MR. `--mr` does not relax §42.6 (review failed, secret, no `summary_short`, `approval: manual`). |
| D8 | A `Dockerfile` and `examples/ci/`; no published image; `--claude-auth api` in CI. |
| D9 | W3 in: a comment and a configured transition, only after an MR, with the same Jira credentials as the read. |
| D10 | The live check runs on gitlab.com. |
| D11 | The Conventional Commit type: the model proposes `commit_type`, Go accepts it from a closed list or drops it. |
| D12 | The branch name format is configurable, from the config or from the repository's instructions; `orchestrator/<id>` is only the fallback. |
| D13 | "The repository's instructions" are the prose of CLAUDE.md / AGENTS.md (and CONTRIBUTING.md): a model reads them and proposes a name, Go checks it. |
| D14 | The name is fixed when the worktree is created and never renamed; the instructions win over the config template. |
| D15 | Story tasks keep `story/<slug>/<ID>`. |

## 17. Author's defaults (the owner may override any)

| # | default | why |
|---|---|---|
| A1 | With a type and a key: `fix: PAY-12 summary` | stays a valid Conventional Commit; GitLab–Jira finds the key anywhere |
| A2 | `Orchestrator-Decision: <complexity>/<stages>`, `manual/…` for `--stage` and review | TODO W1's format; complexity is absent for manual |
| A3 | `--mr` with `secret_scan.mode: off` is a config error | a push publishes the tree |
| A4 | `GET project` before the run, as the Jev model check | a wrong token costs nothing |
| A5 | An open MR for the branch is reused, not updated | one MR per branch; a story `--retry` reuses its branch |
| A6 | Exit code 6 "completed, needs a person" | CI needs to tell "no MR" and "review failed" from success without parsing |
| A7 | In a review run a red test fails the run before the review | the engine's transitions stay unchanged; the report says why |
| A8 | Report bounds: 30 task lines, 50 files, 30 issues, 60 000 bytes | readable in an MR |
| A9 | `review --mr`: forks and non-open MRs are refused | a fork's branch is another project's code and credentials |
| A10 | Push timeout is a fixed 5 minutes, not a key | one less setting |
| A11 | `--output json` is not offered for `story run` and `story plan` | a story is long and interactive; `story status` exists |
| A12 | A Jira write failure is only a warning | the MR is the delivered result |
| A13 | Fixed at creation + instructions first ⇒ a separate tool-less call before the worktree (D14 leaves no step that could answer earlier); made only when the files mention branches | implement runs after the branch exists |
| A14 | The instruction files are read from `base_ref`, not the operator's tree | the run is built from `base_ref`; an uncommitted edit is not the repository's rule |
| A15 | Template placeholders `{id}`, `{key}`, `{slug}`, `{date}`; Cyrillic transliterated | `{type}` is unknown at creation (D14) |
| A16 | Never push to a branch that already exists on the remote | a proposed name must not move somebody else's branch |

## 18. Not covered

- GitHub (TODO W2.5, after GitLab is used in production).
- MR assignees, reviewers, milestones, squash and auto-merge; waiting for the MR pipeline.
- Updating an existing MR's description or pushing again to it; force-push after a story rebase.
- Inline review comments; reviewing a fork's MR.
- Jira comments on a failed run or without an MR; links between issues.
- Publishing a Docker image; macOS images; a non-GitLab CI example.
- A retry command for a push or MR that failed at the end of a completed run (it is a printed
  `git push` and the GitLab UI).
- An org-level floor that forbids or forces `--mr`.
- Renaming a branch after implement (`{type}`, a name from `summary_short`); a template for story
  branches; a branch name for an `inplace` run (it commits on the current branch, §42).
