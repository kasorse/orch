# Delivery — from a finished run to a merge request (phase 3)

Status: **specified** (2026-10-23; revised the same day: worktree by default, commit → push → MR
at the end of every worktree run, asked unless `--mode auto`; and: a story task's branch named by
its repository's rules, D21; the spec branch of [`specs/parallel-story`](../parallel-story/SPEC.md)
pushed by the parent and no `-u` in a story child, D22); nothing built. TODO.md W1 (the rest), W2,
W3, W4, W5. Owner's decisions D1–D22 (D2, D3, D5, D15 replaced) and author's defaults A1–A24 in
`SPEC.md` §16–§17.
Builds on
[`specs/worktree/`](../worktree/README.md) and [`specs/story/`](../story/README.md), both done.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 12 implementation tasks (DL09 `review --mr` dropped, A21) |

## The problem in one paragraph

A run ends with a commit on its own branch and a `summary.md` of metrics; pushing, opening a merge
request, describing it and moving the ticket are still done by hand, a CI job has no contract to
read, and a change somebody else wrote cannot be reviewed. This phase: gives the commit a
Conventional Commit type and a decision trailer; names the run's branch from the repository's
instructions (CLAUDE.md, AGENTS.md: a tool-less call proposes, Go checks) or a config template; renders a reviewer-facing `report.md`; makes
every task with `implement` a worktree run by default (`--inplace` opts out) and ends it with
commit → push → merge request: one `git push` with GitLab's `merge_request.*` push options opens the
MR (`report.md` as its description, line breaks sent as `\n`), with the operator's own git credentials and no GitLab API client; `--mode auto` does all three
without asking, every other mode asks before each; after the MR, comments on the Jira issue and
moves it; adds `--output json`, a documented exit code table (with a new 6, "completed, needs a
person"), a Dockerfile and a GitLab CI example; and adds `orchestrator review` for `base...head`.

## Tasks

| task | title | TODO | size | depends on |
|---|---|---|---|---|
| [DL01](tasks/TASK-DL01-commit-message.md) | `commit_type`, Conventional Commit header, `Orchestrator-Decision` | W1.1 | S | — |
| [DL02](tasks/TASK-DL02-report.md) | `report.md`, `diff.run_files`, Markdown escaping | W1.3–4 | M | — |
| [DL03](tasks/TASK-DL03-worktree-default.md) | worktree by default, `delivery` config, the checks before the worktree | W2.1 | S–M | — |
| [DL04](tasks/TASK-DL04-deliver.md) | commit → push → MR (push options), the questions, `run.json: mr`, resume, exit 6 | W2.2–4 | M | DL02, DL03 |
| [DL05](tasks/TASK-DL05-story-mr.md) | `story run`: an MR per task, the task's branch by its repository's rules, `story.json` MR and branch, stop on a missing MR | W2 | M | DL04, DL13 |
| [DL06](tasks/TASK-DL06-jira-write.md) | `jira.write`: comment, transition, intents | W3 | M | DL04 |
| [DL07](tasks/TASK-DL07-headless.md) | no terminal, `--output json`, the exit code table | W4.1–2 | S–M | DL04 |
| [DL08](tasks/TASK-DL08-review-local.md) | `orchestrator review --base/--head`: detached worktree, `ReviewDiff` base | W5 | M | DL02 |
| [DL10](tasks/TASK-DL10-docker-ci.md) | `Dockerfile`, `examples/ci/` | W4.3–4 | S | DL07, DL08 |
| [DL11](tasks/TASK-DL11-integration.md) | scenarios 64–77: fake GitLab remote, fake Jira write, branch names | — | M | DL01–DL08, DL13 |
| [DL13](tasks/TASK-DL13-branch-name.md) | branch name: the instructions call, `worktree.branch.template`, checks, resume | W1 | M | — |
| [DL12](tasks/TASK-DL12-documentation.md) | MVP.md §49 and others, CLAUDE.md, README, example config, TODO.md | — | S | DL11 |

```
commit    DL01 ─────────────────────────────┐
branch    DL13 ─────────────────────────────┤        (DL13 also before DL05)
report    DL02 ─┬─ DL08 ───────────┐        │
worktree  DL03 ─┴─ DL04 ─┬─ DL05   │        ├─ DL11 ─ DL12
                         ├─ DL06   │        │
                         └─ DL07 ──┴─ DL10 ─┘
```

Estimate: 22–25 engineering days, plus the live check on gitlab.com (`SPEC.md` §15). DL01, DL02,
DL03 and DL13 can start on day one. Recommended order for one person: DL03 → DL01 → DL13 → DL02 → DL04 →
DL07 → DL08 → DL05 → DL06 → DL10 → DL11 → DL12. With DL02, DL03, DL04 and DL07 done, the main loop
(task → worktree → MR, in a terminal or in CI) already works.

## Ground rules for every task

- **Go writes everything a person reads.** A model contributes `summary_short` and one word
  from a closed list; never a title, description or comment as is.
- **No commit, no delivery.** Delivery never relaxes §42.6.
- **Only `--mode auto` delivers without asking**; every other mode asks before the commit, the
  push and the MR, and a no stops there.
- **One write, once.** An intent in `run.json` before the push and every Jira write, the id after
  it, never repeated once it may have reached the server (`MayExist`).
- **No GitLab credentials in the orchestrator.** The push opens the MR (push options), with the
  operator's git credentials; there is no `internal/gitlab`.
- **Only the run's branch is pushed**, never forced, after the operator fingerprint check.
- `internal/report` imports only `domain`. `internal/workflow` and
  `internal/policy` import only `domain`.
- Every new `run.json` / `story.json` field is validated strictly on load; every new config key
  is declared in the schema; a literal secret key is declared so it can be refused by name.
- Tests need no network, no real CLI, no GitLab or Jira: a bare repository with a push-options
  hook as the remote, a fake Jira server in `test/integration`.
- Each task ends green on `scripts/check.sh` (root and `bench/`).
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add an
  `## As built` section to the task file with the date and anything built differently, and why.
  A gate the test step cannot run (the live check, `docker build`) is left to the operator and
  named in the As built.
