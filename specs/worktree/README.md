# Run isolation in a git worktree

Status: **done** (specified 2026-10-17; 2026-10-18): WT01–WT10 done. `scripts/check.sh` (root and
`bench/`) passed outside the sandbox on 2026-10-18 over all of them, and the live suite
(`go test -tags security ./test/security/...`, V-wt-1…3 included) passed on claude 2.1.302. The
owner decided O1–O5 on 2026-10-17 (`SPEC.md` §18): `worktree` by default, a commit on the run's
branch without the question, workflows without `implement` in place, setup sandboxed with the
network, `run --inplace` for one run. TODO.md Q4 is ticked.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 10 implementation tasks |

## The problem in one paragraph

A run with `implement` needs the operator's working copy clean and keeps it busy until it ends;
its result is uncommitted changes in that copy. The story spec that follows needs a branch per
task, based on another task's branch, in several repositories, which no single checkout can give.
This spec runs every workflow with `implement` in its own git worktree, outside the repository,
on the branch `orchestrator/<run-id>` from the operator's HEAD. The guards apply there unchanged,
and the worktree's `.git` pointer becomes guarded state. A trusted setup command installs
dependencies with the network before any model step, never after. A completed run commits on its
own branch, warns about conflicts with the operator's branch through `git merge-tree`, and
removes the worktree. `resume` finds the worktree by the run ID; `runs clean` removes old ones.
The operator's working tree, index and branch are never touched.

## Tasks

| task | title | size | depends on |
|---|---|---|---|
| [WT01](tasks/TASK-WT01-config.md) | config: `execution.isolation`, `worktree.setup`, `run --inplace`, clean-start rule per mode ✓ | S | — |
| [WT02](tasks/TASK-WT02-repository.md) | repository: add/remove/list worktrees, `.git` pointer, fingerprint, `merge-tree` ✓ | M | — |
| [WT03](tasks/TASK-WT03-record.md) | `run.json: worktree`, path derivation, strict validation ✓ | S | — |
| [WT04](tasks/TASK-WT04-run-in-worktree.md) | `run` in a worktree: creation, session switch, guard pointer, sandbox bind, output ✓ | L | WT01–WT03 |
| [WT05](tasks/TASK-WT05-setup.md) | setup: networked sandbox, never after a step, guard, logs ✓ | M | WT04 |
| [WT06](tasks/TASK-WT06-end-of-run.md) | end of run: commit on the run branch, conflicts, removal, `summary.md` ✓ | M | WT04 |
| [WT07](tasks/TASK-WT07-resume.md) | resume in the worktree ✓ | M | WT04 |
| [WT08](tasks/TASK-WT08-runs-clean.md) | `runs clean` ✓ | S | WT03 |
| [WT09](tasks/TASK-WT09-live-checks.md) | live checks V-wt-1…3 ✓ | S | WT04 |
| [WT10](tasks/TASK-WT10-integration-docs.md) | scenarios, bench configs, MVP.md §47, CLAUDE.md, README, example, TODO Q4 ✓ | M | all |

```
WT01 ─┐
WT02 ─┼─ WT04 ─┬─ WT05 ─┐
WT03 ─┤        ├─ WT06 ─┤
      │        ├─ WT07 ─┼─ WT10
      │        └─ WT09 ─┤
      └─ WT08 ──────────┘
```

Estimate: 13–16 engineering days. WT01, WT02 and WT03 can start together; WT05–WT09 are
independent after WT04.

## Ground rules for every task

- **The operator's tree is never touched in worktree mode**: not its files, its index or its
  branch. A test asserts it byte for byte wherever a scenario runs in a worktree.
- **`inplace` is today's behaviour in every detail.** No existing scenario changes its
  expectations except by pinning `inplace`.
- **The same guards on the worktree, nothing weaker.** The `.git` pointer is read in Go before
  any git command that trusts it.
- **Paths are derived, never read.** Everything read back from `run.json` is validated strictly.
- **Setup never runs on a tree a model has touched.**
- `internal/workflow` and `internal/policy` import only `domain`; nothing here touches them.
- Every new config key is declared in the strict schema; every new `run.json` field is validated
  on load.
- Tests need no network and no real CLI, except WT09 (`-tags security`).
- Each task ends green on `scripts/check.sh` (root and `bench/`). In an orchestrator run that is
  the run's `test` step; `scripts/check.sh` runs once outside the sandbox before the branch merges.
- When a task is done: tick it here and in TODO.md (Q4), update the `Status:` line above, and add
  an `## As built` section to the task file with the date and anything built differently, and
  why. In an orchestrator run, do this in the same change as the code; the run commits only
  after its test step passes. WT09's live suite is left to the operator.
