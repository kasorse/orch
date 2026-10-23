# Delivery — from a finished run to a merge request (phase 3)

Status: **specified** (2026-10-23); nothing built. TODO.md W1 (the rest), W2, W3, W4, W5. Owner's
decisions D1–D15 and author's defaults A1–A16 in `SPEC.md` §16–§17. Builds on
[`specs/worktree/`](../worktree/README.md) and [`specs/story/`](../story/README.md), both done.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 13 implementation tasks |

## The problem in one paragraph

A run ends with a commit on its own branch and a `summary.md` of metrics; pushing, opening a merge
request, describing it and moving the ticket are still done by hand, a CI job has no contract to
read, and a change somebody else wrote cannot be reviewed. This phase: gives the commit a
Conventional Commit type and a decision trailer; names the run's branch from the repository's
instructions (CLAUDE.md, AGENTS.md: a tool-less call proposes, Go checks) or a config template; renders a reviewer-facing `report.md`; pushes the
run's branch and opens one draft MR in GitLab (`run --mr`, `story run --mr`), the project taken
from the git remote and the token sent only to `gitlab.base_url`; after the MR, comments on the
Jira issue and moves it; adds `--output json`, a documented exit code table (with a new 6,
"completed, needs a person"), a Dockerfile and a GitLab CI example; and adds `orchestrator review`
for `base...head` or a merge request, with an optional MR note.

## Tasks

| task | title | TODO | size | depends on |
|---|---|---|---|---|
| [DL01](tasks/TASK-DL01-commit-message.md) | `commit_type`, Conventional Commit header, `Orchestrator-Decision` | W1.1 | S | — |
| [DL02](tasks/TASK-DL02-report.md) | `report.md`, `diff.run_files`, Markdown escaping | W1.3–4 | M | — |
| [DL03](tasks/TASK-DL03-gitlab-client.md) | `gitlab` config, the project from the remote, `internal/gitlab` | W2.1 | M | — |
| [DL04](tasks/TASK-DL04-run-mr.md) | `run --mr`: fail-fast, push, MR, `run.json: mr`, resume, exit 6 | W2.2–4 | M | DL02, DL03 |
| [DL05](tasks/TASK-DL05-story-mr.md) | `story run --mr`: children, `story.json` MR, stop on a missing MR | W2 | S | DL04 |
| [DL06](tasks/TASK-DL06-jira-write.md) | `jira.write`: comment, transition, intents | W3 | M | DL04 |
| [DL07](tasks/TASK-DL07-headless.md) | no terminal, `--output json`, the exit code table | W4.1–2 | S–M | DL04 |
| [DL08](tasks/TASK-DL08-review-local.md) | `orchestrator review --base/--head`: detached worktree, `ReviewDiff` base | W5 | M | DL02 |
| [DL09](tasks/TASK-DL09-review-mr.md) | `review --mr`, fetch, `--comment` | W5 | M | DL03, DL08 |
| [DL10](tasks/TASK-DL10-docker-ci.md) | `Dockerfile`, `examples/ci/` | W4.3–4 | S | DL07, DL09 |
| [DL11](tasks/TASK-DL11-integration.md) | scenarios 64–78: fake GitLab, fake Jira write, branch names | — | M | DL01–DL09, DL13 |
| [DL13](tasks/TASK-DL13-branch-name.md) | branch name: the instructions call, `worktree.branch.template`, checks, resume | W1 | M | — |
| [DL12](tasks/TASK-DL12-documentation.md) | MVP.md §49 and others, CLAUDE.md, README, example config, TODO.md | — | S | DL11 |

```
commit  DL01 ─────────────────────────────────────────┐
branch  DL13 ─────────────────────────────────────────┤
report  DL02 ─┬──────────────┬─ DL08 ─┐               │
gitlab  DL03 ─┴─ DL04 ─┬─ DL05        ├─ DL09 ─ DL10  ├─ DL11 ─ DL12
                       ├─ DL06        │               │
                       └─ DL07 ───────┘───────────────┘
(DL09 also needs DL03)
```

Estimate: 25–28 engineering days, plus the live check on gitlab.com (`SPEC.md` §15). DL01, DL02,
DL03 and DL13 can start on day one. Recommended order for one person: DL01 → DL13 → DL02 → DL03 → DL04 → DL07
→ DL08 → DL09 → DL05 → DL06 → DL10 → DL11 → DL12. With DL04, DL07 and DL08 done, the main loop
(ticket → MR in CI, MR → review note) already works.

## Ground rules for every task

- **Go writes everything a person reads.** A model contributes `summary_short` and one word
  from a closed list; never a title, description or comment as is.
- **No commit, no delivery.** `--mr` never relaxes §42.6 / §47.5.
- **One write, once.** An intent in `run.json` before every GitLab or Jira write, the id after
  it, never repeated once it may have reached the server (`MayExist`).
- **The token goes only to the configured host.** The project comes from the remote, the host
  from `gitlab.base_url`, and they must agree.
- **Only the run's branch is pushed**, never forced, after the operator fingerprint check.
- `internal/gitlab` imports only `domain`, `apperr` and the standard library, like
  `internal/jira`. `internal/report` imports only `domain`. `internal/workflow` and
  `internal/policy` import only `domain`.
- Every new `run.json` / `story.json` field is validated strictly on load; every new config key
  is declared in the schema; a literal secret key is declared so it can be refused by name.
- Tests need no network, no real CLI, no GitLab or Jira: fake servers in `test/integration`.
- Each task ends green on `scripts/check.sh` (root and `bench/`).
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add an
  `## As built` section to the task file with the date and anything built differently, and why.
  A gate the test step cannot run (the live check, `docker build`) is left to the operator and
  named in the As built.
