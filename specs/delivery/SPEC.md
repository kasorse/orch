# Spec: Delivery — from a finished run to a merge request, in a terminal or in CI

Status: **specified** (2026-10-23), nothing built. The owner's decisions are in §16 (taken
2026-10-23 while scoping; D12–D15 the same day, on the branch name; D16–D20 the same day, on the
worktree default and the end of a run, which replace D2, D3 and D5; D21 the same day, on a story
task's branch name, which replaces D15; D22 the same day, on `specs/parallel-story`'s spec
branch), the author's defaults in §17. TODO.md phase 3: W1 (the rest), W2, W3,
W4, W5. Builds on `specs/worktree/` (§47) and `specs/story/` (§48), both done.

The spec touches these MVP.md sections:
- §4: CLI (`--output json`, `orchestrator review`, the exit code table);
- §30: out of scope (GitLab: one push of the run's branch, which opens one merge request);
- §39: Jira (a comment and a transition after a merge request, §39.12);
- §42: the commit message (a Conventional Commit type, the `Orchestrator-Decision` trailer);
  §42.1/§42.6: a worktree run asks, or under `--mode auto` does not ask (§7.2);
- §43: `report.md` next to `summary.md`;
- §47.1: `worktree` is the default isolation again (§6);
- §47.2: the branch name (instructions, a template, then `orchestrator/<run-id>`);
- §47.5: commit, push and merge request at the end of a worktree run (§7);
- §47.7 and §48.10: pushing and merge requests are no longer "not covered";
- §48.4/§48.5: a story task's branch is named by its repository's rules, `story/<slug>/<ID>`
  only the fallback (§9.1);
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
2. **No delivery without the commit.** Push and merge request happen only after the run
   committed; they never relax the §42.6 rules (D7).
3. **Only `--mode auto` delivers without asking** (D18). Every other mode asks before the
   commit, before the push and before the merge request, and a no stops there.
4. **Writes outside the machine happen once, after an intent, never retried blindly** - the
   §48.7 discipline for the push and every Jira write.
5. **The orchestrator holds no GitLab credentials.** The merge request is opened by the push
   itself (GitLab push options, D19), with the operator's own git credentials; there is no
   GitLab API client.
6. **Only the run's own branch is pushed**, never with force, never the operator's branch.
7. **The exit code and one JSON line are the machine interface**; everything else is for a
   person and goes to stderr under `--output json`.
8. **Review-only is a run**: same store, decision record, guard, report and resume; it just has no
   `implement` and reviews `base...head`.

### Ground rules for the tasks of this story

- These are story tasks (`story run`). `story run` writes `specs/delivery/README.md` and each
  task's `## As built`: **do not edit anything under `specs/delivery/`**. CLAUDE.md's bookkeeping
  for `specs/*/tasks` (ticking, the `Status:` line, As built) is done by `story run` for these
  tasks.
- Only DL12 edits the shared documents (MVP.md, CLAUDE.md, TODO.md, README.md,
  orchestrator.example.yaml), so the task branches do not conflict on them.
- A gate the test step cannot run (the live check on gitlab.com, `docker build`) is left to the
  operator: the task says so in its summary and does not claim it was done.
- CLAUDE.md's invariants hold: Go orders and decides, no model picks what runs; git is argv only.
- The gate is `scripts/check.sh` (root and `bench/`).

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

### 4.3 The branch name (D12–D14, D21)

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
| `{key}` | the Jira key of a `run --jira` task, or a story task's child issue (§9.1); empty otherwise |
| `{slug}` | the task's first line (a story task: its title, §9.1): Cyrillic transliterated (a fixed table in Go), lower case, every run of characters outside `[a-z0-9]` → `-`, trimmed, at most 40 bytes cut at a `-` |
| `{date}` | `YYYYMMDD`, local time |

An empty placeholder takes the separator after it (`-`, `_`, `/`) with it:
`feature/{key}-{slug}` gives `feature/PAY-12-use-plus-in-add` or `feature/use-plus-in-add`. An
unknown placeholder is a config error. `{type}` is not offered: the commit type exists only after
implement, and the name is fixed before it (D14).

**The checks**, for both sources, in Go:

- `git check-ref-format --branch`; only `[A-Za-z0-9._/-]`; at most 100 bytes; no leading `-`
  or `/`, no `//`, no `..`, no `.lock` suffix;
- not `HEAD`, not the base branch, not `delivery.target_branch`, not under `story/` (the
  fallback namespace of story tasks, §9.1), and not an existing local branch (`refs/heads/<name>`);
- an existing branch with the template's name gets `-<last 6 of the run ID>` appended once, then
  is checked again; a model's name that exists is rejected (to the template).

**Where it applies**: worktree runs only, story tasks included (D21): a story task falls back to
`story/<slug>/<ID>` instead of `orchestrator/<run-id>` (§9.1). A review run (§12) is detached and
has no branch.

**Recording and resume**: the name is in `run.json: worktree.branch`, as today. It is no longer
derived from the run ID, so a resume checks it instead: the §4.3 character and format rules, and
`git worktree list` showing the derived path on exactly that branch (the check §47's resume
already makes). The path stays derived from the run ID.

**Push** (§7.3): before pushing, `git ls-remote --heads <remote> <name>` must show nothing. A branch
of that name on the remote is never pushed to, even as a fast-forward: `mr.error: the branch
<name> already exists on <remote>`, exit 6. A name from a model, or a template without `{id}`,
could otherwise move somebody else's branch.

## 5. `report.md`

Written next to `summary.md` whenever a run reaches a terminal status, by Go from `run.json` and
`decision.json`, never read back, never a resume input; failing to write it fails nothing
(§43.2's rules). It is the MR description (§7.3) and, in the CI example, the note of a review
(§11.4). `summary.md` keeps the metrics; `report.md` is for the reviewer.

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
- Bounded at 60 000 bytes (lists are cut first, then the task quote); GitLab's note limit is 1 MiB.
- A review run (§12) has "Reviewed" (`base...head`, the MR) in place of "What changed", and no
  "Why these stages".

## 6. Worktree by default (D16)

- `execution.isolation` defaults to `worktree` again (it was `inplace` since 2026-10-18; MVP.md
  §47.1 changes back). `run --inplace`, or `isolation: inplace` written out, opts out;
  `run --worktree` stays and overrides an `inplace` config. Given together they remain a usage
  error.
- What becomes a worktree run is unchanged (§47.1): a workflow with `implement`. A workflow
  without it runs in place, so `run --stage review` still reviews the operator's uncommitted work.
- `worktree.setup` with `isolation` absent now runs in every worktree run (it ran only under
  `run --worktree`).
- An unborn HEAD stays a config error; its message names `--inplace`.
- Existing tests and scenarios that read a run's result in the working tree pin `isolation:
  inplace` (the rule CLAUDE.md already sets for new ones); DL03 does that.

The owner's table for the worktree feature (D20) is §47 as built, plus this spec:

| capability | phase | where |
|---|---|---|
| a worktree per task | now | §47.2 |
| a branch of its own per task | now | §47.2, §4.3 |
| every CLI step runs in the worktree | now | §47.2 |
| git status and diff from the worktree | now | `diff.run`, the review diff, `report.md` |
| removed after the run | now | §47.5: after a successful commit, whatever the push did (§7.1) |
| kept on an error | now | §47.5: no commit (a failed run, a refused commit, a no) keeps it |
| parallel agents, a separate worktree for review, merge conflicts resolved, changes integrated automatically, a lifecycle manager over persistent state | next version | §18 |

## 7. The end of a worktree run: commit, push, merge request (D17–D19)

### 7.1 The chain

A worktree run that completes with `implement` in its workflow always ends with this chain.
There is no flag; `--inplace` is the only way out (an `inplace` run keeps §42: the commit
question or `auto_commit`, no push, no MR). Each step runs only after the one before it
succeeded:

1. **commit** on the run's branch, in the worktree (§42.2 message, §42.3 commit);
2. the conflict check (`merge-tree`, §47.5), unchanged;
3. **push** of the branch to `delivery.remote`;
4. **merge request** `branch → target`, opened by that same push (§7.3);
5. the worktree removal (§47.5), unchanged: after a successful commit, whatever steps 3–4 did.
   The branch holds the result.

### 7.2 Who says yes (D18)

| mode | commit | push | merge request |
|---|---|---|---|
| `--mode auto` | no question, under the §42.6 rules | no question | no question |
| any other mode (`manual`, `accept-edits`, the interactive default) | `Commit to <branch>? [y]es / [N]o / [e]dit message` (§42.2) | `Push <branch> to <remote>? [y]es / [N]o` | `Open a merge request <branch> → <target>? [y]es / [N]o` |

- The questions come in that order; the default (an empty answer, a closed stdin) is no, and a no
  stops the chain: nothing after it is asked or done. Both the push and the MR answer are taken
  before the one `git push` (§7.3).
- The commit question is §42.2's as it is: its warnings (a failing review, a secret finding, the
  `policy.paths` approvals), a typed line when there is no `summary_short`. The §42.6 refusals
  apply only under `--mode auto`, where nobody can read a warning.
- `auto_commit` has no effect in a worktree run (it behaved as if true, §42.6): `--mode auto` is
  the yes given in advance for all three, every other mode asks.
- `--mode plan` never completes a run, so it never reaches the chain. A non-auto mode without a
  terminal is a usage error at the start (§11.1).
- A story child runs under `--mode auto` (§9).

### 7.3 The push, which opens the merge request

From the operator's repository (the branch is shared with the worktree), after the operator
repository's git fingerprint matched the one taken when the worktree was created (as before
`merge-tree`; a pre-push hook runs) and `git ls-remote --heads <remote> <branch>` showed nothing
(§4.3):

```
git push -u --no-follow-tags <remote> refs/heads/<branch>:refs/heads/<branch> \
    -o merge_request.create \
    -o merge_request.target=<target> \
    -o "merge_request.title=<title>" \
    -o "merge_request.description=<description>"
```

Without the MR (a no to the third question) the same command without the `-o` options.

- argv, never `sh -c`; the full environment (the operator's credential helper, ssh agent), as
  every git call; never `--force`; bounded by a fixed 5 minutes (A10). `-u` records the upstream
  in the shared git config: the push is the last step that compares the fingerprint, so nothing
  after it can take that for tampering. **A story child pushes without `-u`** (D22): a sibling
  task of the same repository may be running (`specs/parallel-story`), and its guard compares
  that same config.
- `run.json: mr {state: intent, remote, source, target}` is saved before the push. The push is
  never repeated by this run: one that timed out may have reached the remote.
- **target**: the run's `worktree.base_branch`; when it is empty (detached HEAD, a CI checkout),
  `delivery.target_branch`; with neither, an error before the worktree is created (§8).
- **title**: the commit header (§4.1), e.g. `PMC-123: Implement new feature` or
  `fix: PMC-123 Implement new feature`.
- **description**: `report.md` rendered for this run after the commit, with every line break
  written as the two characters `\n` (CR dropped). git refuses a push option with LF or NUL
  (`push options must not have new line characters`); GitLab turns a literal `\n` back into a
  line break (A17). A `\n` that was text in the report also becomes a break: harmless.
- **Bounds**: the title 255 bytes, cut at a rune. A push option travels as one pkt-line (at most
  65 516 bytes), so the whole `merge_request.description=…` option is at most 64 000 bytes after
  encoding: when it would be longer, the report is rendered again with smaller lists, then a
  shorter task quote (the §5 cut order), and as a last resort cut at a line with `…(cut)`.
  `report.md` on disk stays the 60 000-byte version. Title and report text are already escaped as
  untrusted text (§5).
- **The result.** GitLab answers on the push's stderr (`remote: View merge request for
  <branch>:` and the URL on the next line). Go takes the first line after that marker that is
  exactly `https://<host>/<path>/-/merge_requests/<n>`, `<host>` being the remote's host
  (`git remote get-url`, userinfo dropped). The "To create a merge request … visit" link
  (`…/merge_requests/new?…`) never matches. The URL is only recorded and printed, never requested.

| what happened | `run.json: mr` | printed | exit |
|---|---|---|---|
| pushed, MR URL found | `state: created`, `iid`, `url` | `Merge request !12: <url>` | 0 |
| pushed, MR not wanted (a no) | `state: pushed`, `skipped: declined at merge request` | `Pushed <branch> to <remote>.` | 0 |
| pushed, MR asked, no URL in the answer | `state: pushed`, `error: the remote reported no merge request` | the error and the GitLab link to create one, if printed | 6 |
| push failed: rejected, a hook, the network, `the receiving end does not support push options` (not GitLab) | `state: intent`, `error: <git's last line>` | `To push it yourself: git push -u <remote> <branch>` | 6 |

Minimum: git 2.10 (push options), GitLab 12.2 (the `title` and `description` options). Neither is
checked in advance; the push says so. A GitLab that does not decode `\n` (older versions, see
[gitlab#241710](https://gitlab.com/gitlab-org/gitlab/-/issues/241710)) shows the description
with literal `\n`; the live check (§15) confirms the decoding on gitlab.com, and the version
that first does it goes into DL04's As built.

### 7.4 No commit, no delivery (D7)

- Under `--mode auto`, when §42.6 does not commit (a warning, no `summary_short`, `approval:
  manual`) or the commit fails: `mr.skipped: <why>`, `No push and no merge request: <why>`, the
  changes stay in the worktree and its path is printed. Exit code 6.
- In any other mode, a no: `mr.skipped: declined at <commit|push|merge request>`. Exit code 0:
  the person decided.
- A workflow without `implement` runs in place and has no chain.

### 7.5 Output, resume

- `summary.md` gains a "Merge request" row (the URL, or the skip or error); `report.md`'s
  footer names the MR when there is one.
- A resume that completes the run runs the same chain: the mode is the resume's own `--mode`,
  the isolation `options.isolation`. A completed run is not resumable (§37), so a failed push or
  MR is finished by hand with the printed command.

## 8. Configuration and the checks before the worktree

```yaml
delivery:
  remote: origin          # default origin: pushed to
  target_branch: main     # optional: the target when HEAD is detached
```

Strict schema, both keys optional, the section optional. No token, no URL: the push uses the
operator's git credentials.

Checked once the workflow is known and approved (with `--stage`, before any call), before the
worktree is created and before any step, for a run that will be a worktree run (A20):

1. `git remote get-url <remote>` succeeds;
2. a target is known: the current branch, else `delivery.target_branch`;
3. `security.secret_scan.mode` is not `off`: a push publishes the tree (A3).

A failure is a config error (exit 2) whose message names `--inplace`. Whether the remote is GitLab
is not checked: only the push can tell (§7.3). `orchestrator validate` makes checks 1 and 3 when
the configured isolation is `worktree`.

## 9. `story run`

- Children already run as `run --mode auto --story-task <file>` (§48.5), so every child runs the
  §7 chain without questions; `story run`'s confirmation is the operator's yes. The branch is
  named by the task's repository's rules (§9.1); the target is the branch its base came from (the
  dependency's branch as `story.json` recorded it, else the story's base branch), exactly the
  "target" `story run` prints today. The dependency's branch is on the remote by then (its own
  child pushed it); GitLab retargets the stacked MR when the parent MR merges and deletes its
  branch.
- The per-service `orchestrator validate` that `story run` makes before the first dispatch
  (§48.6) includes §8's checks: a service without its remote stops the story before anything runs.
- `story.json` records each task's `mr` (url, iid) read from the child's `run.json`; `story status`
  and the end table print it. A child that ends with exit 6 (no MR) is `completed` without an MR:
  the story **stops** before the next task that depends on it (its branch may not be on the
  remote), and continues independent tasks. `story run --retry <ID>` works as today.
- **The spec branch** (`specs/parallel-story` §1, D22). A task with no parent, or with several,
  targets `story/<slug>/spec`; a task with one parent targets that parent's branch. The parent
  `story run` pushes the spec branch, so that it is on the remote before any merge request
  targets it:
  - when: once after the approval at the first start, before the first dispatch; then after
    every merge Go makes on it, before the task that needs the merge is dispatched;
  - how: from the repository's checkout, after its git fingerprint matched the one the pre-run
    checks took, `git push --no-follow-tags <remote> refs/heads/story/<slug>/spec:refs/heads/story/<slug>/spec`:
    argv, the full environment, no `-u`, no push options (no merge request for the spec
    branch), never `--force`, the 5-minute bound;
  - the remote: at the first push, `git ls-remote --heads` must show no such branch (§4.3);
    later pushes are fast-forwards of the commit pushed before, and a rejected one (the remote
    branch moved) stops the story;
  - recorded: `story.json: bases.<repo>.spec_push {state: intent|pushed, sha, error?}`, the
    intent saved before the push and never repeated blindly (§3, principle 4). A failed push stops the
    story before the next dispatch, with the command to push it by hand.
  - None of this happens before `specs/parallel-story` is built: until then a task without a
    parent targets the story's base branch, as above.

### 9.1 A story task's branch name (D21)

A story task's branch is named as any worktree run's (§4.3): from the instructions of the task's
repository, else from that repository's `worktree.branch.template`, else `story/<slug>/<ID>` (a
retry `story/<slug>/<ID>.<n>`), which takes the place of `orchestrator/<run-id>` as the fallback.

- **Where**: in the child, when its worktree is created, by the same code as any run, with the
  child repository's own config (its `worktree.branch`, as every other key of a service, §48.1)
  and its instruction files read from the task's `base_ref` (A22). `story run` names no branch.
- **Inputs**: `{key}` and the call's Jira key are the task's child issue (the task input's
  `jira`, created before the first dispatch, §48.7), empty without one; `{slug}` and the call's
  task line are the task's title, from a new task-input field `title` (the task file's heading,
  one line, at most 200 runes, validated like the other fields); `{id}` is the child's run ID.
- **The task input's `branch`** is the fallback: still `story/<slug>/<ID>[.<n>]` and validated by
  the story pattern; that it does not exist is checked only when it is the name used.
- **Checks**: §4.3's, for both sources. A name from the instructions or the template is never
  under `story/`, so the pre-run check that refuses an unknown `story/<slug>/…` branch still
  covers every fallback name, and a rule name cannot collide with one.
- **Recorded**: the child's `run.json: worktree.branch` and `branch_naming`, as any run;
  `task.source.branch` keeps the fallback it was given. `story run` copies `worktree.branch` into
  `story.json: tasks.<ID>.branch` when it reads the child's run - after the child ends, or when it
  settles the run of a `story run` that died (§48.6). Before that the field is absent.
- **Readers of the recorded name**: a dependent's MR target (§9 above), the check that counts a
  blocked task done once its branch has a commit beyond its base (§48.6), the known-branch set of
  the pre-run checks, the README, the As built and the end table.
- **The plan** before `[Y/n]` prints the fallback with `or by <repo>'s rules` (A23): the name is
  fixed only when the worktree is created, and the Jira key a template may use exists only after
  the approval.
- **`--retry`**: the attempt's fallback gains `.<n>` as today; a template name the abandoned
  attempt already holds gets §4.3's `-<6>` suffix; an instructions name that exists is rejected to
  the template (A24).
- **Cost**: the naming call is the child's and counts in its `outcome.usage`, so
  `story.max_cost_usd` counts it.

## 10. Jira write-back (W3)

```yaml
jira:
  write:
    comment: true             # default false
    transition: "In Review"   # optional; the transition's name or its target status name
```

- Only for a task with a Jira key (`run --jira`: the issue; a story task: its child issue, §48.7)
  and only after this run's push opened an MR (`mr.state: created`). Same `jira.base_url`, credentials,
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
 "mr":{"iid":12,"url":"https://…/-/merge_requests/12"},"mr_skipped":"","mr_error":"",
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
| 2 | config, dependency, usage, credentials (`FailureAuth`), Jira fail-fast, the §8 checks |
| 3 | a provider failed |
| 4 | the workflow failed or a limit stopped it (a red test, `max_steps`, budget, no progress) |
| 5 | cancelled by the operator |
| **6** | **completed, but needs a person**: a worktree run with `implement` ended without an MR that nobody declined (`--mode auto` did not commit, or the push or MR failed in any mode, §7.3–§7.4), or `orchestrator review` gave a failing verdict (A6) |

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
- `examples/ci/gitlab-ci.yml` (no leading dot: `.gitlab-ci.yml` is a protected path at any depth, and an example is copied to the root as `.gitlab-ci.yml` anyway):
  - `ai-implement`: manual, or a pipeline variable `JIRA_KEY` →
    `orchestrator run --jira "$JIRA_KEY" --mode auto --output json > result.json` (a worktree
    run by default: it pushes and opens the MR, §7); `result.json` and the run directory as
    artifacts. The checkout is detached, so the example sets `delivery.target_branch`.
  - `ai-review`: on `merge_request_event` →
    `orchestrator review --base "$CI_MERGE_REQUEST_DIFF_BASE_SHA" --head "$CI_COMMIT_SHA"
    --mode auto --output json`, then the job itself posts `report.md` as one note with `curl` to
    the notes API and `$GITLAB_TOKEN` (job script, not orchestrator code; A21).
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
```

- `--base` is required; `--head` defaults to `HEAD`. `--mode`, `--output json`, `--config`,
  `--claude-auth`, `--codex-auth` as for `run`. Not `--stage`, `--inplace`/`--worktree`,
  `--allow-red-tests`. A merge request is reviewed by its refs (in CI: §11.4); there is no
  `--mr` (A21).
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
- The task: `--task`, else the `--jira` ticket, else Go writes one from the commits:
  `Review the change from <base> to <head>. Its commits:\n- <subject>…` (at most 50 subjects,
  8 KiB). Through `action.taskBlock` as every task.
- `run.json: review_target {base, head, merge_base, base_ref_name, head_ref_name}`.
  `worktree.branch` is empty for a review run only.
- A verdict that fails completes the run with it, as today, and the command exits 6.
- `resume` works: same worktree (verified detached at `head`), same `review_target`.

### 12.3 Output

`report.md` (§5, the review form), `summary.md`, the console verdict and issues; with
`--output json` the one line (`review_verdict`).

## 13. Persistence

All optional on load, strictly validated when present.

| field | rule |
|---|---|
| `options.isolation` | unchanged; a run started without `--inplace` and without `isolation: inplace` now records `worktree` |
| `branch_naming` | `{source: instructions\|template\|default, proposed?, rejected?, usage?, call?}`; `proposed` ≤ 255 printable bytes, `rejected` one line ≤ 500 bytes; absent for an inplace or review run |
| `worktree.branch` | no longer `orchestrator/<run-id>` only: any name that passes §4.3's character and format rules (a story task: those, or its fallback's story pattern) |
| story task input `title` | one line, 1–200 runes, printable; required |
| `options.jira_write` | `{comment: bool, transition: string}`, copied from `jira.write` at run start |
| `mr` | `{state: intent\|pushed\|created, remote, source, target, iid?, url?, skipped?, error?}`; `iid`/`url` only with `created`; `url` https on the remote's host, ending `/-/merge_requests/<iid>`; `skipped` and `error` one line, ≤ 500 bytes, redacted; `skipped` alone (no `state`) when the chain stopped before the push |
| `jira_write` | `{comment: {state, id?}, transition: {name, state: done\|missing\|failed\|unchanged}}` |
| `review_target` | `{base, head, merge_base}` 40 hex; `base_ref_name`, `head_ref_name` printable ≤ 255 |
| `executions[].diff.run_files[]` | ≤ 200 `{path (clean, repository-relative), insertions, deletions, binary}`, on an implement with `diff.run` only |
| `story.json: tasks.<ID>.mr` | `{iid, url}` |
| `story.json: bases.<repo>.spec_push` | `{state: intent\|pushed, sha (40 hex), error?}`; `error` one line ≤ 500 bytes, redacted; only with the spec branch (§9) |
| `story.json: tasks.<ID>.branch` | no longer `story/<slug>/<ID>[.<n>]` only: §4.3's rules too; set from the child's `worktree.branch`, absent before the child's run is read |

`worktree.branch` may be empty only on a run with `review_target`.

## 14. Security

- No GitLab credentials in the orchestrator: no token variable, no API client, no request to
  GitLab but the push. The push uses the operator's git credentials, like every git call; the CI
  example keeps the token in a credential helper, never in the remote URL (§11.4).
- Push: only the run's own branch (`worktree.branch`, created by this run), explicit refspec, no
  `--force`, after the operator fingerprint check, and never to a branch that already exists on the
  remote (§4.3): a model-proposed or template name cannot move somebody else's branch. The `-o`
  values are built by Go, escaped and bounded, with no LF or NUL (`\n` encoded, §7.3). The MR URL is read from the
  remote's answer, checked against the remote's host and pattern, and only recorded and printed.
- Only `--mode auto` publishes without a question (§7.2); a story's children run under it after
  `story run`'s confirmation.
- The branch-name call has authority `none`; the instruction files are read from `base_ref` with
  `git show`, bounded and fenced; whatever it answers only passes Go's name checks or is dropped. A secret finding never reaches a push: under `--mode auto` it blocks the commit
  (§42.6), in another mode the commit question shows it, and the scan cannot be `off` in a worktree run (§8).
- Everything model- or ticket-written that reaches GitLab or Jira is escaped (§5).
- A delivering run is never `inplace`, so `.git/config` (with a URL that may carry a token) is
  outside the worktree's file tools (§47.3).

## 15. Live check (gitlab.com, D10)

An operator gate, not a test step (it reaches a real remote). A test project on gitlab.com and a
repository whose `origin` is that project, pushable with the operator's credentials:

1. `orchestrator run --mode auto --output json "<small fix>"` → a worktree run, a pushed branch,
   an MR with the commit header as title and `report.md` as a multi-line description (the
   `\n` decoded, not shown literally), `run.json: mr.state: created`, exit 0, the worktree
   removed.
2. The same task with a test that cannot pass → the fix loop runs out and the run fails (exit
   4): no commit, no push, the worktree kept. (A failed run never reaches the chain of §7, so it
   has no `mr` record; exit 6 is a completed run that §42.6 does not commit, scenario 65.)
3. Without `--mode auto`: `y` to the commit, `y` to the push, `n` to the MR → the branch on the
   remote, no MR, `mr.state: pushed`, exit 0.
4. A two-task story in that repository, with `worktree.branch.template` set → two MRs on
   templated branches, the second targeting the first's branch.
5. A run whose `origin` is a GitHub repository → `the receiving end does not support push
   options`, `mr.error`, the manual command, exit 6.

The steps and their results go into the As built of DL04 and DL05 and the README's `Status:`.

---

## 16. Owner's decisions (2026-10-23)

| # | decision |
|---|---|
| D1 | Phase 3 in one spec: W1 (rest), W2, W3, W4, W5. GitHub stays out. |
| ~~D2~~ | ~~`--mr` implies a worktree run; `--mr --inplace` is a usage error.~~ Replaced by D16–D17: there is no `--mr`. |
| ~~D3~~ | ~~The GitLab project comes from the git remote; the token only to `gitlab.base_url`.~~ Replaced by D19: no API client, the push opens the MR. |
| D4 | One MR per story task, stacked on the dependency's branch (now without a flag, §9). |
| ~~D5~~ | ~~W5: local `--base/--head`, plus `--mr` with an optional `--comment`.~~ Narrowed by D19 to `--base/--head` (A21). |
| D6 | A separate `report.md` for the reviewer; `summary.md` stays the metrics. |
| D7 | No commit → no MR. Delivery does not relax §42.6 (review failed, secret, no `summary_short`, `approval: manual`). |
| D8 | A `Dockerfile` and `examples/ci/`; no published image; `--claude-auth api` in CI. |
| D9 | W3 in: a comment and a configured transition, only after an MR, with the same Jira credentials as the read. |
| D10 | The live check runs on gitlab.com. |
| D11 | The Conventional Commit type: the model proposes `commit_type`, Go accepts it from a closed list or drops it. |
| D12 | The branch name format is configurable, from the config or from the repository's instructions; `orchestrator/<id>` is only the fallback. |
| D13 | "The repository's instructions" are the prose of CLAUDE.md / AGENTS.md (and CONTRIBUTING.md): a model reads them and proposes a name, Go checks it. |
| D14 | The name is fixed when the worktree is created and never renamed; the instructions win over the config template. |
| ~~D15~~ | ~~Story tasks keep `story/<slug>/<ID>`.~~ Replaced by D21. |
| D16 | Every task runs in its own worktree by default; a flag (`--inplace`) cancels it. Reverses the 2026-10-18 default. |
| D17 | After all the stages of a worktree run: commit, push, merge request. Always: no opt-in flag. |
| D18 | Commit, push and MR are automatic only under `--mode auto`; in a manual mode each of them is asked. |
| D19 | The MR is opened with `glab mr create` or with GitLab's push options (`git push -u origin <branch> -o merge_request.create -o merge_request.target=main -o merge_request.title=…`); the owner accepted either (the author chose push options, A17). |
| D20 | The worktree scope table (§6): worktree per task, own branch, CLI in the worktree, git status and diff, cleanup, kept on error now; parallel agents, review worktrees, merge conflict resolution, automatic integration, lifecycle over persistent state in the next version. |
| D21 | A story task's branch is named by the same rules as any run (the instructions, then the config); `story/<slug>/<ID>` is only the fallback. Its merge request is pushed and opened like any run's under `--mode auto` (§7, §9). |
| D22 | Delivery follows `specs/parallel-story`: tasks with no parent or several target the spec branch, which the parent `story run` pushes; a story child pushes without `-u` (§7.3, §9). |

## 17. Author's defaults (the owner may override any)

| # | default | why |
|---|---|---|
| A1 | With a type and a key: `fix: PAY-12 summary` | stays a valid Conventional Commit; GitLab–Jira finds the key anywhere |
| A2 | `Orchestrator-Decision: <complexity>/<stages>`, `manual/…` for `--stage` and review | TODO W1's format; complexity is absent for manual |
| A3 | A worktree run with `secret_scan.mode: off` is a config error | a push publishes the tree |
| A6 | Exit code 6 "completed, needs a person"; an operator's no is 0 | CI needs to tell "no MR" and "review failed" from success without parsing; a no is a decision, not a failure |
| A7 | In a review run a red test fails the run before the review | the engine's transitions stay unchanged; the report says why |
| A8 | Report bounds: 30 task lines, 50 files, 30 issues, 60 000 bytes | readable in an MR note |
| A10 | Push timeout is a fixed 5 minutes, not a key | one less setting |
| A11 | `--output json` is not offered for `story run` and `story plan` | a story is long and interactive; `story status` exists |
| A12 | A Jira write failure is only a warning | the MR is the delivered result |
| A13 | Fixed at creation + instructions first ⇒ a separate tool-less call before the worktree (D14 leaves no step that could answer earlier); made only when the files mention branches | implement runs after the branch exists |
| A14 | The instruction files are read from `base_ref`, not the operator's tree | the run is built from `base_ref`; an uncommitted edit is not the repository's rule |
| A15 | Template placeholders `{id}`, `{key}`, `{slug}`, `{date}`; Cyrillic transliterated | `{type}` is unknown at creation (D14) |
| A16 | Never push to a branch that already exists on the remote | a proposed name must not move somebody else's branch |
| A17 | Push options, not `glab`; the description is `report.md` with line breaks encoded as `\n` | no new executable, no token, no API client, one command for push and MR; git refuses LF in a push option, GitLab decodes `\n` (owner, 2026-10-23) |
| A18 | "Manual" is every mode but `auto`: `manual`, `accept-edits`, the interactive default | D18 names auto as the only automatic mode |
| A19 | `auto_commit` has no effect in a worktree run | `--mode auto` is the yes in advance for all three steps |
| A20 | The remote, target and scan checks run after the workflow is approved, before the worktree, not before the decision | a workflow without `implement` runs in place and needs no remote |
| A21 | No `review --mr`: CI reviews the MR by `CI_MERGE_REQUEST_DIFF_BASE_SHA`/`CI_COMMIT_SHA` and posts the note in its own job script | without an API client the orchestrator cannot read an MR or post a note |
| A22 | A story task's branch is named in the child at worktree creation, with the service's config and instructions, not by `story run` at the start | one implementation (§4.3); each service's own rules apply (§48.1); the child issue's key exists only after the approval; the name stays fixed at creation (D14) |
| A23 | The plan before `[Y/n]` shows the fallback and `or by <repo>'s rules`, not the final name | the final name does not exist yet (A22); `story status` shows it once known |
| A24 | Names from the rules are never under `story/`; a retry under a template gets the `-<6>` suffix, an existing instructions name falls to the template | the unknown-branch check keeps scanning one prefix; §4.3's collision rules unchanged |

A4, A5 and A9 (the project check, reusing an open MR, refusing forks in `review --mr`) went with
D3 and D5.

## 18. Not covered

- GitHub (TODO W2.5, after GitLab is used in production); a GitHub remote fails at the push (§7.3).
- `glab`, the GitLab API; draft, labels, assignees, reviewers,
  milestones, squash and auto-merge; waiting for the MR pipeline.
- A flag that keeps a worktree run but skips the push (an operator says no; CI uses `--inplace`).
- Updating an existing MR's description or pushing again to it; force-push after a story rebase.
- `review --mr`, MR notes from the orchestrator, inline review comments; reviewing a fork's MR.
- Jira comments on a failed run or without an MR; links between issues.
- Publishing a Docker image; macOS images; a non-GitLab CI example.
- A retry command for a push or MR that failed at the end of a completed run (it is a printed
  `git push` and the GitLab UI).
- An org-level floor that forbids or forces delivery or `inplace`.
- Renaming a branch after implement (`{type}`, a name from `summary_short`); a branch name for an
  `inplace` run (it commits on the current branch, §42); naming a story's branches up front, in
  the plan or in the spec (A22, A23).
- The next version of the worktree feature (D20): parallel agents, a separate worktree for
  review, resolving merge conflicts, integrating changes automatically, a lifecycle manager over
  persistent state (beyond `run.json`, resume and `runs clean`).
