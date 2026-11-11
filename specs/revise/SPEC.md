# Revise: a new requirement for a run that already opened a merge request

Status: approved, 2026-11-11. Owner's answers R1–R9 (§0); the author's defaults A1–A12 (§13), confirmed by the owner 2026-11-11.

## Problem

A run delivered a ticket: commit, push, merge request (MVP.md §50.5). The operator then wants
changes. Today the only way is a new run, which opens a second branch and a second merge request,
because §50.6 never pushes to a branch that exists on the remote and §50.14 lists "updating an
existing merge request or pushing again to it" as not covered. The operator wants to name the
work (a Jira ticket, a merge request, an earlier run, a branch) and the new requirement; the
orchestrator finds the merge request and its branch, makes the change on that branch, pushes it
back, and adds the requirement to the ticket.

Seen on PAY-31 (Jira Cloud, 2026-11-11): the Jira development panel is empty, and the merge
requests are web links (`GET /rest/api/2/issue/{key}/remotelink`, `relationship: "merge
request"`, `object.url` `https://gitlab.example.com/acme/payments/billing/-/merge_requests/1294`). Three
of them for one ticket (!1290, !1293, !1294). A web link carries the URL and the title, not the
branch or the state: those need the GitLab API.

## 0. The owner's answers (2026-11-11)

| # | answer |
|---|---|
| R1 | Console only: `orchestrator revise`. Not the listener (later). |
| R2 | The requirement goes into the ticket's **description**, appended as `Update <date>: <text>`. |
| R3 | Jira has the GitLab integration: a ticket's merge requests are its web links. |
| R4 | A merged or closed merge request: a **new** branch and a new merge request with the change. |
| R5 | The review sees the **whole** merge request's diff, not only the new commit. |
| R6 | A target is a Jira key, a merge request URL (anyone's), an earlier run's ID, or a branch. |
| R7 | A GitLab API client, **read-only**, with a read-only token. This changes MVP.md §30 and the "no GitLab client" rule of §50. |
| R8 | Several merge requests: the **latest**. |
| R9 | The description is written **after the run**, once the push succeeded. |

## 1. CLI

```
orchestrator revise <target> "<requirement>" [--mode M] [--config F] [--output json]
                    [--allow-red-tests] [--claude-auth M]
  <target>: PAY-12 | https://<gitlab>/<group>/<project>/-/merge_requests/<iid> | <run-id> | <branch>
```

* The target's kind is decided by Go from its form, in this order: a URL under `gitlab.base_url`
  matching `/-/merge_requests/<n>` → merge request; `jira.ParseRef` accepts it → Jira key; a
  UUID with a run directory `.orchestrator/runs/<id>` → run; else a branch name passing §50.3's
  format rules → branch. Anything else is a usage error (exit 2). A branch named like a Jira key
  is taken as the key; `--branch` is not added (A1).
* The requirement is one argument, non-empty, at most `limits.prompt` (the request's bound);
  `-` reads it from stdin, as `run` does.
* Always a worktree run: `--inplace`, `--stage`, `--jira` and `--story-task` are refused.
  `isolation: inplace` in the config is overridden (the change must land on the branch).
* `revise` is a run: it has a run ID, `run.json`, the whole fail-fast chain (§7 of MVP.md), the
  shared tail (`executeWorkflow`), resume (`orchestrator resume <id>`).

## 2. Configuration

```yaml
gitlab:
  base_url: https://gitlab.example.com  # existing (listener, fetch_repository)
  allowed_groups: [acme/payments]       # existing
  api_token_env: GITLAB_READ_TOKEN      # new: the read-only API token, env-only
```

* `api_token_env` is a new optional key of the existing `gitlab:` section (strict schema; a
  literal `api_token` is rejected like the other secrets). It is not the listener's `token_env`
  (that one is a git credential with write access) and the two must differ.
* Checked by `ValidateDependencies` only with `DependencyOptions{GitLab: true}` (`revise`, and
  `validate` when the key is present): the section, `base_url`, a non-empty `allowed_groups`, the
  variable set. `revise` with a Jira target needs the `jira:` section as `run --jira` does.
* The token is registered in `Config.CredentialEnvNames` (scrubbed from every child), read only
  in `app`, passed only to `gitlab.NewHTTPClient`, redacted from every error.

## 3. `internal/gitlab`: the read-only client

Like `internal/jira`: imports only `domain`, `apperr` and the standard library (enforced by a
test), unexported wire types, strict decoding, no redirect followed, requests only to
`gitlab.base_url`, `PRIVATE-TOKEN` header, a timeout per request, 429/503 retried with
`Retry-After` only. **Only GET**: a test asserts the client never builds another method.

| call | used for |
|---|---|
| `GET /api/v4/personal_access_tokens/self` | the scope check, once per run, before anything else of the API |
| `GET /api/v4/projects/:path/merge_requests/:iid` | a merge request: `iid`, `state`, `source_branch`, `target_branch`, `sha`, `created_at`, `title`, `description`, `web_url`, `project_id`, `source_project_id` |
| `GET /api/v4/projects/:path/merge_requests?source_branch=<b>&order_by=created_at&sort=desc&per_page=1` | a branch target |

* **The scope check (A2).** The token's `scopes` must be a non-empty subset of `read_api`,
  `read_repository`, `read_user`; `active: true`, not `revoked`. A token with `api`,
  `write_repository` or any other scope is a config error before any further call: "gitlab.api
  _token_env must name a read-only token (scopes: api)". A group or project access token answers
  the same endpoint.
* `:path` is the project path, URL-encoded, and must be under `gitlab.allowed_groups`
  (prefix match on path segments) before any request.
* Errors are `apperr.GitLabError{Kind: auth|not_found|rate_limited|unavailable|invalid}`; 401 is
  `FailureAuth`-like: exit 2, no retry.

## 4. Resolving the target

Go turns the target into a `domain.Revision` (no model call), before `store.NewRun`; a failure
creates no run (exit 2, or 3 for the network):

```
Revision {
  target   {kind: jira|mr|run|branch, value}
  jira     string            // the key, when there is one (§8)
  project  string            // group/project
  mr       {iid, url, state, source_branch, target_branch, sha, created_at, title}  // absent for a branch without one
  original string            // the original task text (below)
  from_run string            // a run target's ID
  mode     update | new       // §5
}
```

| target | merge request | original task |
|---|---|---|
| Jira key | `jira.Fetch` (one GET, as `run --jira`), then `GET …/remotelink` (new `jira.RemoteLinks`); every link whose `object.url` is a merge request URL on `gitlab.base_url` in **this repository's project** (A3), each read with the API; the one with the latest `created_at` (R8) | the rendered ticket (`jira.Render`) |
| MR URL | that merge request | its title and description |
| run ID | the run's `mr.url` (`state: created`), read with the API; a run with `mr.state: pushed` and no MR: its branch, as a branch target | the run's `task.description` (with its clarification rendered) |
| branch | the latest merge request with that `source_branch` in this project, if any | the latest merge request's title and description; without one, the branch's last commit message |

* **This repository's project**: the path of `delivery.remote`'s URL (`git remote get-url`),
  host equal to `gitlab.base_url`'s. A merge request of another project is refused, naming it:
  "!12 is in acme/payments/other; run revise in a checkout of it" (A3). A Jira key whose links name no
  merge request of this project: "PAY-12 has no merge request of acme/payments/billing (it has:
  acme/payments/other!3)".
* **A fork** (`source_project_id != project_id`): refused, nothing is pushed to another project
  (A4).
* A Jira key without any merge request link: refused ("PAY-12 has no merge request; use
  `orchestrator run --jira PAY-12`").
* The original text is task text (MVP.md §38.5), bounded by `jira.max_bytes` / `limits.prompt`,
  secret-scanned like the request.

## 5. Update or new

* `mr.state == opened` → **update** (§6).
* `merged`, `closed`, or a branch target with no merge request and no remote branch → **new**
  (R4): an ordinary worktree run whose base is the merge request's `target_branch` (fetched, §6.1),
  named by §50.3 as any run, delivered by the §50.5 chain unchanged (a new branch, a new merge
  request via push options). Its task is §7's.
* A branch target with a remote branch and no merge request → **update** with the merge request
  opened by the push (`-o merge_request.create`, §50.6's options) (A5).
* `locked` or a draft state other than these: refused.

Printed before the approval: `Revise !1294 (opened) on fix/PAY-31-round-totals in
acme/payments/billing` or `!1290 is merged: a new branch from main and a new merge request`.

## 6. The update run

### 6.1 The branch

1. Operator's fingerprint taken (fetch runs credential helpers).
2. `git fetch --no-tags <remote> +refs/heads/<source_branch>:refs/orchestrator/revise/<run-id>`
   (argv, the full environment, 5 minutes). The fetched commit must equal `mr.sha`; otherwise
   "the branch moved after the merge request was read; run revise again". Nothing under
   `refs/remotes/` or `refs/heads/` is written by the fetch.
3. The local branch `<source_branch>`:
   * absent → `git worktree add -b <source_branch> <path> <sha>`;
   * present at `sha` or an ancestor of it, and checked out nowhere → moved by `git update-ref
     refs/heads/<b> <sha> <old>`, then `git worktree add <path> <b>`;
   * present and diverged, or checked out in any worktree → refused, naming it ("your local
     fix/PAY-31 has commits that are not in !1294" / "fix/PAY-31 is checked out in <path>").
4. The fingerprint is compared again (post-checkout hooks), as `openWorktree` does.

`run.json: worktree` as today, with `base_ref = mr.sha`, `base_branch = mr.target_branch`,
`branch = source_branch`, and the new `existing_branch: true`. `branch_naming` is `{source:
existing}`: no naming call. The guard, the restore point, `pre_run_ref` and the secret-scan
baseline are taken at `base_ref` as in any worktree run.

### 6.2 The workflow

* The decision runs as usual (clarify, pre-check, classification) over the revision request (§7);
  **development is forced on** (reason `revise`), like a story task; the stage set is otherwise
  the policy's.
* The pre-run test runs on `base_ref` (the merge request's tip): a red suite is refused unless
  `--allow-red-tests`.
* **Review** sees the whole merge request (R5): the review diff is `merge-base(target, head)
  ...head`, with `target` = `refs/orchestrator/revise/<run-id>-target` fetched like the branch
  (`+refs/heads/<target_branch>:…`). The new field `run.json: review_base` records the merge base;
  `Repository.ReviewDiff` takes the base from it when present, else `base_ref` as today.
  The requirement's lines are the review criteria (as a story task's acceptance items).
* `limits`, the quality loop, path rules, the budget: unchanged.

### 6.3 Delivery

The §50.5 chain with these differences for `mode: update`:

1. The commit: a new commit on top, never amend, never squash; message as §50.2, header from the
   implementation's `summary_short`; trailer `Orchestrator-Run` plus `Orchestrator-Revises: !<iid>`.
2. The conflict check against `target_branch` as today.
3. The push question reads `Push <branch> to <remote> (updates !1294)? [y]es / [N]o`; no merge
   request question.
4. Before the push: `git ls-remote --heads <remote> <branch>` must show exactly `base_ref`
   (instead of §50.6's "shows nothing"). Anything else: `mr.error: <branch> on <remote> is at
   <short>, not at the merge request's <short>: somebody pushed`, exit 6.
5. `git push --no-follow-tags <remote> refs/heads/<b>:refs/heads/<b>`: no `-u`, no `-o`, never
   `--force` (git itself refuses a non-fast-forward). Intent in `run.json: mr` first, never
   repeated, as §50.6.
6. Success: `mr {state: updated, iid, url}` (new state `updated`), printed `Merge request !1294
   updated: <url>`, exit 0. The merge request's title and description are not changed (A6).
7. The worktree is removed as after any commit; the local branch stays at the new commit.

`--output json`: `mr` with `state: updated`; `revision {target, mode, iid}`.

## 7. The task the stages see

* The **request** is the requirement as the operator typed it: it goes through `action.taskBlock`
  like any request; translation, clarify and the decision provider see it followed by
  `Original task:` and the original text (the decision provider sees text only, as today).
* The stages that take the task (research, investigate, implement, review) get, after the
  request, the original task through `action.fence` under the heading `The task this merge request
  was opened for`, and for `mode: update` the merge request's title and URL. The clarification,
  if any, follows as today.
* `run.json: task.source {kind: revise, target: {kind, value}, jira?, project, mr?, from_run?,
  mode}`; the original text is stored in `artifacts/revise-original.txt` (a record, read back by a
  resume for the prompts, validated: size cap, UTF-8).

## 8. The ticket

* The Jira key: a Jira target's key; a run target's `task.source.key` (kind `jira`) or story
  `jira`. A merge request or branch target has none: no key is guessed from a title (A7).
* After the run's push succeeded (`mr.state` `updated` or `created`, R9), once:
  1. `GET /rest/api/2/issue/{key}?fields=description` (new `jira.Description`);
  2. the new description is the old one, an empty line, and `Update 2026-11-11: <requirement>`
     (the date in UTC, the requirement wiki-escaped minimally: `{`, `[`, `|` and line starts that
     would be markup; at most 8 KiB, cut at a rune with `…`);
  3. `PUT /rest/api/2/issue/{key}` `{"fields":{"description":…}}` (new `jira.SetDescription`),
     204.
* Intent in `run.json: jira_write.description {state: intent|done|failed}` before the PUT; never
  repeated once it may have reached Jira; a failure is a warning, the exit code does not change.
* Jira has no conditional update: an edit made between the GET and the PUT is lost. The window is
  one round trip (A8; `shortcut:` in the code).
* Not gated by `jira.write` (the operator asked for it by running `revise`); `jira.write.comment`
  and `transition` work as §39.12 after `updated` too, with the comment `Merge request [!1294|url]
  updated: <commit header>`.
* A resume that reaches a completed push writes the description if its intent is absent; it reads
  the description, never the ticket's other fields.

## 9. Resume

`orchestrator resume <id>` of a revise run: the persisted `task.source`, decision and workflow;
the worktree verified as any worktree run's (`existing_branch` changes only the branch-name check:
the recorded name must equal `task.source.mr.source_branch`). **No GitLab client** is built: the
check before the push is git's (`ls-remote` equals `base_ref`). A Jira client only for the
description write and §39.12's.

## 10. Persistence (new fields)

* `run.json`: `task.source` (kind `revise`, §7), `worktree.existing_branch`, `review_base`,
  `mr.state: updated`, `jira_write.description`, `revision {mode}`; `branch_naming.source:
  existing`.
* `artifacts/revise-original.txt`, `logs/revise-fetch.{stdout,stderr}.log`.
* `summary.md` and `report.md`: a "Revises !1294" line.
* Every new field strictly validated on load; an unknown `task.source.kind` stays an error.

## 11. Security

* The GitLab token: env-only, read-only by its scopes (§3), only in `internal/gitlab`'s request
  header; never argv, URL, log, `run.json`, prompt or a child process. Git keeps using the
  operator's own credentials for fetch and push.
* Only projects under `allowed_groups`; only `gitlab.base_url`; a merge request URL from a ticket
  or the operator is matched against it, never followed elsewhere.
* Merge request titles and descriptions, ticket text and web links are untrusted task text: no
  authority comes from them; fenced in prompts; secret-scanned.
* The push never forces, never targets a branch whose remote tip is not `base_ref`, never another
  project.
* The fetch writes only `refs/orchestrator/revise/<run-id>*` and git objects; the local branch is
  moved only by the fast-forward of §6.1.3, which refuses a diverged one.

## 12. Not covered

* The listener (R1): a ticket re-queued with a revision comment.
* Changing the merge request's title, description, labels, reviewers; a note on the merge
  request; reading merge request discussions as task text.
* Rebasing a stale branch on its target; resolving conflicts; force-push.
* Merge requests from forks; another GitLab than `gitlab.base_url`; GitHub.
* A key guessed from a merge request title; the Jira development panel (dev-status API).
* Several merge requests changed at once (several services of one ticket): one per `revise`.

## 13. The author's defaults (confirmed 2026-11-11)

| # | default |
|---|---|
| A1 | The target's kind from its form (§1); no `--branch`/`--mr` flags. |
| A2 | The token must prove itself read-only (`personal_access_tokens/self` scopes) or the run is refused. |
| A3 | Only merge requests of the current checkout's project are candidates; "latest" is among them. |
| A4 | Forks are refused. |
| A5 | A remote branch without a merge request: push to it and open one by push options. |
| A6 | The merge request's own title and description are never changed. |
| A7 | No Jira key is guessed for MR and branch targets. |
| A8 | The description's read-modify-write has a one-round-trip race; accepted. |
| A9 | The decision runs as usual with development forced on; clarify runs. |
| A10 | `new` mode bases on the fetched `target_branch` tip, not on the operator's HEAD. |
| A11 | `revise` always runs in a worktree, whatever `execution.isolation` says. |
| A12 | Resume builds no GitLab client. |
