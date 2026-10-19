# Spec: Run isolation in a git worktree

Status: **specified** (2026-10-17), not started. The owner decided O1–O5 the same day (§18);
the author's defaults are in §19. Once WT10 is done, MVP.md §47 becomes the normative home and this
file stays the design record.

The spec touches these MVP.md sections:
- §5: configuration (`execution.isolation`, `worktree.setup`);
- §37: resume (§37.4 loading, §37.8 safety);
- §38.3: guards around implement (the worktree's `.git` pointer, the clean-start rule);
- §41.3 and §41.5: the test sandbox and the pre-run test, which now run in the worktree;
- §42: the commit at the end of a run;
- §43: `summary.md`;
- new §47: the worktree run.

It is TODO.md Q4, with the owner's decision of 2026-10-08 (a run starts from HEAD and does not
see the operator's uncommitted changes; conflicts appear only when the run's branch is merged,
are reported deterministically with `git merge-tree`, and are resolved by a person with git,
never by an LLM). It is also the foundation the story spec (`specs/story/`, next) builds on:
a branch per task, based on another task's branch, in several repositories.

---

## 1. Problem

1. **A run occupies the operator's working copy.** A workflow with `implement` refuses a dirty
   tree (§38.3), and while it runs the operator cannot touch the tree: any edit becomes part of
   the run's diff, its review, its secret scan and the commit offered at the end.
2. **The result is uncommitted changes in the operator's tree.** §42 offers to commit them on
   the current branch. There is no branch that holds a run's result and nothing else.
3. **Story needs branches the operator's checkout cannot give.** A story task runs on a branch
   based on another task's branch, in each of several repositories. Switching branches in the
   operator's working copies for that is out of the question.
4. **Parallel runs (later, §30 today) need separate trees.**

## 2. What already exists

- `Repository.GitFingerprint` reads the config from `--git-common-dir` and the hooks from
  `--git-path hooks`. In a linked worktree both resolve to the shared config and hooks, so the
  fingerprint is already the right one there (TODO Q4 item 3 is mostly done).
- `Repository.GitDirs` returns the git directory and, when it differs, the common directory; the
  test sandbox binds both read-only.
- The restore point ref `refs/orchestrator/runs/<id>` is shared by every worktree of a
  repository (`repository.RestorePointRef`).
- `worktreeRel` gives `""` for a path outside the repository, and `Dispatcher.RunsDir` is then
  `""`. The claude adapter confines the file tools to the working directory (`buildArgs`; a Read
  of `/etc/hosts` is denied, verified on claude 2.1.287).
- `CommitAll`, `CheckBaseline`, `recordBaseline`, `Store.Lock`, `IsDirty` (which leaves the
  runs directory out) and the shared tail `executeWorkflow` that `run` and `resume` both use.

## 3. Gaps

| # | gap |
|---|---|
| G1 | Every step uses the repository opened at the process's working directory (`session.repo`). There is no other place a step can run. |
| G2 | In a linked worktree `.git` is a **file** inside the writable tree, and it decides which git directory every later git call reads. `git status` never reports it and no protected pattern covers it; the sandbox binds the git directories read-only but leaves this file writable. A rewrite that points at a planted git directory changes the fingerprint only indirectly (through the common directory's path). |
| G3 | A fresh worktree has none of the ignored build state the operator's tree has: `node_modules`, `.venv`, generated code. Tests that pass in the operator's tree fail in the worktree. |
| G4 | A resume must find the worktree again, and everything a resume reads from the run directory is untrusted (§37.4): the path has to be derived, not read. |
| G5 | §42 asks before committing on the operator's branch. The run's own branch is a different object, and story needs a commit per task without a question per task. |
| G6 | Worktrees accumulate: every run with `implement` leaves a checkout on disk. |
| G7 | A conflict with the operator's branch shows up only when someone merges. |
| G8 | The benchmark and the integration scenarios check the result in the working directory. |
| G9 | The decision's project context is read from the operator's tree (possibly dirty), while the steps see HEAD. |

## 4. How comparable tools do it (summary)

From the author's knowledge, not a fresh survey:

- **Desktop tools that run several agents on one repository** (Conductor and similar) give each
  agent its own git worktree and branch, and run a per-repository setup script there before the
  agent starts (install dependencies, copy environment files).
- **OpenAI Codex (cloud)** runs a setup script with network access, then the agent phase with
  the network off by default.
- **Cursor background agents** run an install command from the environment's definition before
  the agent.
- **Claude Code's documentation** recommends separate git worktrees for parallel sessions.

What they share, and what this spec takes: one worktree and one branch per unit of work; a
trusted setup step that has the network and runs **before** the model, never after; the result
is a branch, and merging it is a person's job.

## 5. Principles

1. **The operator's tree is never touched.** In worktree mode the orchestrator writes git
   objects, the run's branch, the run's worktree (and its administrative directory under
   `.git/worktrees/`) and `refs/orchestrator/` — never the operator's working tree, index or
   branch.
2. **The same guards, on the worktree, and nothing weaker.** The worktree's `.git` pointer
   becomes guarded state (§8.3).
3. **Paths are derived, never read** (§37.4): the worktree's path comes from the run ID.
4. **Setup is trusted operator config that runs only on a tree no model has touched.** It has
   the network; code a model wrote never does.
5. **The result is a branch.** Conflicts are reported by git and resolved by a person with git.
6. **Story reuses this as is.** The base commit and the branch name are recorded per run, so a
   story task differs only in what it passes (§15).

## 6. Modes and configuration

```yaml
execution:
  isolation: worktree   # inplace (default since 2026-10-18, O1) | worktree
worktree:
  setup: "npm ci"       # optional; trusted operator config, like test.command
```

- **`inplace`** is today's behaviour, unchanged in every detail.
- **`worktree`** applies to workflows that contain `implement`. A workflow without it changes
  nothing and is usually about the operator's uncommitted work - `run --stage review` reviews the
  working tree against HEAD (§40.4), `--stage testing --stage review` checks a hand edit - so it
  runs in place (O3). In a worktree, a clean checkout of HEAD, that review would find nothing.
  The rule is decided on the workflow the operator approved, after the review screen (an edit
  that adds `implement` makes the run a worktree run).
- **`run --inplace`** (O5) makes one run `inplace` whatever the config says, with today's
  clean-start rule. It is how the operator lets `implement` continue their uncommitted work
  (with `security.require_clean_worktree: false`, as today). **`run --worktree`** makes one run
  `worktree` the same way (added 2026-10-18 with the default change, O1); the two together are
  a usage error.
- **The choice is recorded** in `run.json: options.isolation` at run start (with the other
  options a resume reads instead of the config, §44): a resume continues in the mode the run
  started in, whatever the config or a flag says then.
- **`security.require_clean_worktree` does not apply in worktree mode.** The tree the model
  writes is a fresh checkout of a commit, clean by construction; the org floor on that key
  (§45.2) therefore holds without a check. A dirty operator tree gets one warning instead:
  `The run starts from HEAD <short>; <n> uncommitted changes in your tree are not part of it
  (commit them first, or run with --inplace).`
- **`worktree.setup`** with `isolation: inplace` written out is a config error (it would never
  run); with the key absent it runs only in a `run --worktree` run.
  `worktree` is declared in the strict schema; `isolation` accepts the two values only.

## 7. Lifecycle of a worktree run

```
fail-fast chain (unchanged; + isolation/setup validation)
decision in the operator's tree (unchanged; G9: see below)
review / approval (checkWorktree: in worktree mode a warning when dirty, never a refusal)
── after approval, before the restore point ──
create the worktree                        §8; run.json: worktree, saved at once
switch the session to the worktree         every step, guard, sandbox, scan, commit
setup                                      §9, once, before any step
restore point = base_ref, pinned           §38.3 as today; no snapshot is needed
head_ref + resume_baseline                 taken in the worktree, after setup
pre-run test, secret baseline, engine      unchanged, in the worktree
commit on the run's branch, conflict check, removal   §10
```

- **Creation.** `base_ref` is the commit HEAD names in the operator's tree; `base_branch` the
  branch it is on (empty when detached). An unborn HEAD (no commits) is a config error naming
  `isolation: inplace`. The branch `orchestrator/<run-id>` and the derived path must not exist;
  the orchestrator refuses rather than reusing either. `git worktree add --quiet -b
  orchestrator/<run-id> <path> <base_ref>` runs the operator's `post-checkout` hook and filters
  (LFS); they are the operator's own, and the fingerprint is compared before and after it.
  `run.json: worktree` is saved right after, so a crash leaves a worktree the run can find.
- **The session switches.** From here on the dispatcher (the provider's working directory), the
  step guard, the test sandbox, the verify server, the secret scan, the review diff and the
  commit all use the worktree's `Repository`. The store, the run directory and its lock stay in
  the operator's repository: `.orchestrator/runs/` is outside the worktree, `RunsDir` is `""`,
  and the provider's file tools cannot reach it because they are confined to the working
  directory (§12 makes that a live check).
- **The config file.** When it lies inside the operator's repository, its repository-relative
  path is protected in the worktree too: the worktree's copy is what the branch carries back at
  merge.
- **G9.** The decision still reads the operator's tree. With a dirty tree the project context may
  show content the worktree does not have; the warning above says so. Reading HEAD instead would
  need a second Context Manager path for a difference that only affects the decision.

## 8. Paths and identity

### 8.1 Where

`<state>/orchestrator/worktrees/<repo-id>/<run-id>`, where `<state>` is `$XDG_STATE_HOME` or
`~/.local/state`, and `<repo-id>` the first 16 hex digits of the sha256 of the common git
directory's absolute, symlink-resolved path.

It is outside the repository on purpose:
- not in the operator's `ls-files`, Read, Grep, build or test discovery;
- not under `.git/`: claude treats every write under `.git/` as a prompt (denied under
  `dontAsk`), and the sandbox binds `.git` read-only after the repository, which would make a
  worktree inside it read-only.

Not configurable in v1 (§19).

### 8.2 Record

`run.json` gains:

```json
"worktree": {
  "path": "/home/u/.local/state/orchestrator/worktrees/4f1c…/<run-id>",
  "branch": "orchestrator/<run-id>",
  "base_ref": "<full sha>",
  "base_branch": "master",
  "admin_dir": "/repo/.git/worktrees/<name>",
  "setup_done": true,
  "conflicts": ["internal/app/app.go"],
  "removed": false
}
```

Validated strictly on load: `path` equals the derived path (a record for people, never used as
read), `branch` equals `orchestrator/<run-id>`, `base_ref` is a full SHA, `base_branch` a valid
branch name or empty, `admin_dir` lies directly under `<common dir>/worktrees/`. `conflicts`
only on a completed run, `removed` only on a run that is not running (the end of a run or
`runs clean`, §14).

### 8.3 The `.git` pointer

The worktree's `.git` file must read exactly `gitdir: <admin_dir>\n`. It is read **in Go**, never
through git, and compared:
- by the guard, before the fingerprint, in every comparison (around implement, every check and
  test command, setup, every verify call, before the commit);
- by a resume, before any git command in the worktree.

A mismatch is a guard violation named `.git (worktree pointer)`. The fingerprint also hashes
`<admin_dir>/config.worktree` (read by git when `extensions.worktreeConfig` is on) and the
`admin_dir` path itself.

## 9. Setup

- **What.** `worktree.setup` is a shell string, run with `sh -c` in the worktree — trusted
  operator config, like `test.command`, with the same documented exception to argv-only.
- **When.** Once, right after creation, before the restore point, the baseline and the pre-run
  test. **Never after a step has run**: a resume of a run with executions never runs it; a resume
  of a run with no executions and `setup_done: false` runs it again. Setup has the network;
  running it over files a model wrote would hand that code the network.
- **Where.** Under the test sandbox's mounts (the worktree writable; its git directories and
  `.git` file read-only; `$HOME` hidden; `test.ro_binds` / `test.rw_binds`), but **with the
  network**: installing dependencies needs it. Environment: `execution.TestEnv()` plus
  `test.env_pass`. With `test.sandbox: none` it runs unsandboxed, like the tests.
- **Guard.** The fingerprint and the `.git` pointer are compared after setup. The git config and
  hooks are shared with the operator's repository, so setup must not change them (husky's
  `prepare` does: set `HUSKY=0` through `test.env_pass`). A change fails the run.
- **Failure.** A non-zero exit or a timeout fails the run before any step:
  `failure_reason: worktree setup failed (exit <n>)`, output in `logs/setup.{stdout,stderr}.log`.
  It runs inside the engine's share of `execution.timeout`.
- Private registry credentials are not covered (`test.env_pass` refuses credential-like names):
  bind a package cache through `test.rw_binds`, or use `inplace`.

## 10. The end of a worktree run

### 10.1 The commit (O2)

A completed run whose workflow contains `implement` commits its changes **on its own branch,
without the question**: §42 asks because the commit writes the operator's index and branch,
which a worktree run never touches. Otherwise the §42 rules hold as if `auto_commit: true`
(§42.6): no commit when the question would show a warning (a failing last review, a secret
finding under `mode: warn`), when there is no `summary_short`, or when the last implement matched
a `policy.paths` rule with `approval: manual`. In those cases an operator at the terminal gets
the §42.2 question, `Commit to orchestrator/<run-id>? [y]es / [N]o / [e]dit message`, default no;
under `--mode auto` the changes stay in the worktree and its path is printed.

Before the commit: the `.git` pointer, then `CheckBaseline` (fingerprint first) in the worktree.
`CommitAll` runs in the worktree with the operator's identity and hooks; it writes only the
worktree's index and the run's branch. `run.json: commit` as in §42.4.

### 10.2 Output

```
Committed 3f2a9c1 to orchestrator/<run-id>: Fix Add to use +
Based on master@8d0e4b2. To take it:
  git merge orchestrator/<run-id>
```

`summary.md` gains a worktree line: branch, base, commit, conflicts, and the path when the
worktree was kept.

### 10.3 Conflicts

After the commit, when `base_branch` is set: `git merge-tree --write-tree --name-only
--no-messages <base_branch> <commit>` in the operator's repository (it writes objects only;
git ≥ 2.38, older git skips the check with a note). Exit 1 means conflicts: a warning lists the
files and `worktree.conflicts` records them. It catches the operator's branch having moved since
`base_ref`. It never resolves anything. The fingerprint is compared before it: a configured merge
driver would run.

### 10.4 Removal

After a successful commit the worktree is removed with `git worktree remove` (without
`--force`: git refuses when something is left, and the worktree is then kept and its path
printed). The branch stays: it is the result. Without a commit (warnings, a no, a failed or
stopped run) the worktree stays; a failed or stopped run needs it for resume.

## 11. Resume

- The mode comes from `options.isolation`, never from the current config or a flag.
- A run with `worktree` set and `removed: false` resumes **in the worktree**. Its path is derived
  (§8.1) and compared with the record; a missing directory is a `RunStateError`: `the run's
  worktree <path> is gone; start a new run`.
- Before any git command in the worktree: the `.git` pointer (§8.3); then `git worktree list
  --porcelain` in the operator's repository must list the path on `refs/heads/orchestrator/<id>`;
  then §37.8 in the worktree: the baseline (fingerprint first), the protected paths, HEAD against
  `head_ref`.
- The operator's tree may be dirty and on any branch: it plays no part in a worktree run.
- A run with no executions and no worktree yet (refused, cancelled, crashed before creation)
  creates it, like a fresh run. Setup as in §9.

## 12. Security

- **Confinement is load-bearing.** In place, the runs directory is on the deny list. In a
  worktree it is outside the working directory, as is the operator's tree; what keeps a step out
  of both is the adapter's confinement of the file tools to the working directory. That gets live
  checks (`go test -tags security ./test/security/...`), each with a positive control:
  - **V-wt-1 (read):** from a linked worktree, a Read of a file in the operator's tree by
    absolute path, and of `<operator>/.orchestrator/runs/<id>/run.json`, is denied; a Read in the
    worktree works.
  - **V-wt-2 (edit):** an Edit or Write of the worktree's `.git` file, and of a file in the
    operator's tree, is denied. If the CLI allows the `.git` write, the test records it and the
    guard's pointer check (§8.3) is what stops the run; the test asserts that.
  - **V-wt-3 (settings):** a hook in the operator's `.claude/settings.json` does not run in a
    call made in the worktree.
- **Sandbox.** The `.git` file is bound read-only (a file bind after the worktree bind). The git
  directories are outside the worktree and read-only through the base mount. Tests can no longer
  write the operator's tree at all, which is stronger than in place.
- **Names.** The branch name and the path come from the run ID (hex and dashes); `base_ref` is a
  full SHA. Nothing from a model or a task reaches either.
- **Setup** has the network only before any model step (§9).

## 13. Persistence and output

- `run.json: worktree` (§8.2) and `options.isolation` (§6). Each `resumes[]` entry needs nothing
  new: the worktree is the run's.
- `logs/setup.{stdout,stderr}.log`.
- Console: after approval, `Worktree: <path> (orchestrator/<id> from <base_branch>@<short>)`;
  at the end, §10.2.
- `summary.md` (§43): the worktree line.
- `decision.json`: nothing. Isolation is not part of the decision.

## 14. Tests, benchmark, `runs clean`

- **Integration scenarios** (fake executables): a worktree run that implements, tests and commits
  on its branch while the operator's tree is dirty and stays byte-for-byte unchanged; setup that
  fails; setup that changes `.git/config` (fails the run); a planted `.git` pointer (fails the
  step); resume in the worktree after a failed test; resume with the worktree gone (refused);
  conflict reported by `merge-tree`; read-only workflow runs in place. `XDG_STATE_HOME` points at
  `t.TempDir()`.
- Existing scenarios that check the result in the working directory pin `isolation: inplace` in
  their config (they test other things).
- **Benchmark:** `bench/configs/*.yaml` pin `execution.isolation: inplace`. The bench's final
  check reads the workspace tree, and the bench is a black box: only its configs change.
- **`orchestrator runs clean [--older-than 7d] [--dry-run]`** removes, for every run older than
  the limit whose worktree is not removed and whose lock is free, the worktree with `git worktree
  remove` (no `--force`: a worktree with changes is kept and listed), then `git worktree prune`.
  It sets `worktree.removed` on that run. It never deletes a branch, a ref or a run directory.

## 15. What story needs from this (not built here)

- A base other than HEAD (a dependency's branch) and a branch name other than
  `orchestrator/<run-id>` (`story/<slug>/<task>`). Both are fields of `run.json: worktree`; the
  story spec adds the way to pass them and widens the `branch` validation.
- Several repositories: each has its own `<repo-id>` under the state directory, and its own run
  directory. Nothing else.

## 16. Not covered

- Parallel runs (§30 stands).
- Pushing and merge requests (TODO W2); Conventional Commits and `report.md` (the rest of W1).
- Copying ignored files (`.env`, `node_modules`) from the operator's tree: secrets would follow.
  Setup installs what it needs.
- Credentials for setup (private registries).
- Submodules: their git directories live under the shared `.git`, which the sandbox keeps
  read-only. A repository with submodules uses `inplace` in v1.
- A pool of warm worktrees, sparse checkouts for very large repositories.
- An org floor for `execution.isolation`.
- macOS (no bwrap), as for the test sandbox.

## 17. Plan and estimate

| task | title | size | depends on |
|---|---|---|---|
| WT01 | config: `execution.isolation`, `worktree.setup`, `run --inplace`, clean-start rule per mode | S | — |
| WT02 | repository: add/remove/list worktrees, `.git` pointer, fingerprint additions, `merge-tree` | M | — |
| WT03 | `run.json: worktree`, path derivation, strict validation | S | — |
| WT04 | `run` in a worktree: creation, session switch, guard pointer, sandbox bind, output | L | WT01–WT03 |
| WT05 | setup: networked sandbox, never after a step, guard, logs | M | WT04 |
| WT06 | end of run: commit on the run branch, conflicts, removal, `summary.md` | M | WT04 |
| WT07 | resume in the worktree | M | WT04 |
| WT08 | `runs clean` | S | WT03 |
| WT09 | live checks V-wt-1…3 | S | WT04 |
| WT10 | scenarios, bench configs, MVP.md §47 and the sections above, CLAUDE.md, README, example, TODO Q4 | M | all |

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

## 18. Owner's decisions (2026-10-17)

| # | question | decision |
|---|---|---|
| O1 | Default of `execution.isolation`. | **`worktree`** (TODO Q4 item 7). Existing configs change behaviour: the result lands on a branch, not in the tree. The bench and the scenarios that check the tree pin `inplace` (§14). **Changed 2026-10-18 to `inplace`**, with `run --worktree` for one run: a chain of task runs (ST01, ST02, ...) based every run on the same HEAD and needed a manual merge after each; under `inplace` with `auto_commit: true` each run commits on the current branch and the next starts from it. The pins stay. |
| O2 | Commit on the run's branch without the §42 question. | **Yes** (§10.1), under the §42.6 rules. Without it the result sits uncommitted under `~/.local/state`, and story needs a commit per task. |
| O3 | Workflows without `implement`: in place or in a worktree. | **In place** (§6): `run --stage review` keeps reviewing the operator's uncommitted changes. |
| O4 | Setup sandboxed with the network, or unsandboxed. | **Sandboxed with the network** (§9): the test sandbox's mounts, `$HOME` hidden, only before any model step. |
| O5 | How `implement` continues the operator's uncommitted work under the worktree default. | **`run --inplace`** for one run (§6). Rejected: carrying a snapshot of the dirty tree into the worktree (the work in progress would land on the run's branch and come back as duplicates at merge). |

## 19. Author's defaults (change before the task starts if wrong)

- The worktree path is not configurable (§8.1); `<repo-id>` is 16 hex digits of a sha256.
- The branch is `orchestrator/<run-id>` (the full ID: derivable and unique).
- The worktree is removed after a successful commit; branches and refs are never deleted by the
  orchestrator.
- The conflict check needs git ≥ 2.38 and is skipped with a note on older git; nothing else
  changes the minimum git version.
- `runs clean` defaults to `--older-than 7d`.
