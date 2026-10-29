# Delivery without a push: `story.push: false` (first `delivery.push`)

## Problem

A worktree run with `implement` always ends with commit → push → merge request (MVP.md §50.1,
§50.5; decisions D16–D20 of `specs/delivery`). Nothing turns the push off: outside `--mode auto`
a person can say no at the question, but `--mode auto` and every story task push. On a remote
that is not GitLab (this repository's `origin` is GitHub) the push with the `merge_request.*`
options is rejected, the run exits 6, and `story run` blocks every task that depends on it
(`withoutMR`): run f7e76b55 (JL01 of `specs/jira-listener`, 2026-10-29) completed with tests and
review passing and still stopped the story.

The owner (2026-10-29): add `delivery.push: false`, so that a run, and a story, end at the commit
on the run's branch and stay local. Later the same day (D2) the key moved to `story.push`: only a
story turns the push off. The text below is the first design; the As built of the tasks and
MVP.md §50.5, §50.7, §50.8 say where it is now.

## Approach

### Ground rules for the tasks of this story

- `story run` writes `specs/delivery-push/README.md` and each task's `## As built`: do not edit
  anything under `specs/delivery-push/`. Only DP03 edits the shared documents (MVP.md, CLAUDE.md,
  TODO.md, README.md, `orchestrator.example.yaml`); the other tasks say in their commit what DP03
  must document.
- The tasks are one chain (DP01 → DP02 → DP03).
- With `delivery.push` absent or `true` nothing changes: every existing test keeps passing
  unchanged, the output is byte-for-byte the same, and a `run.json` without the new option loads
  and resumes as before.
- CLAUDE.md's invariants hold: Go decides from config and the persisted run; resume reads the
  recorded option, never the current config. The gate is `scripts/check.sh`.

### 1. The key

```yaml
delivery:
  push: false     # default true: a completed worktree run pushes its branch and opens the MR
```

- Strict schema, optional, a boolean. Copied at run start into `run.json: options.push` (`false`
  recorded, `true` left out, like the other options) and read from there by a resume.
- No CLI flag (out of scope).

### 2. The end of a worktree run with `delivery.push: false`

The chain of §50.5 stops after the commit:

1. The commit, exactly as today: every mode but `--mode auto` asks `Commit to <branch>?`;
   `--mode auto` commits under the §42.6 rules. A no, a refusal, a warning: as today (no commit,
   the worktree stays; a refusal is exit 6 as today).
2. The conflict check against `base_branch` (`merge-tree`), as today.
3. **No push, no merge-request question, no `git ls-remote`**, no Jira write-back (it needs a
   merge request, §39.12). `run.json: mr = {skipped: "push disabled (delivery.push: false)"}`.
4. The worktree is removed as after any commit; the branch stays.
5. The console, after `Committed …` and `Based on …`: `Push disabled (delivery.push: false); the
   branch <branch> stays in this repository. To publish it: git push <remote> <branch>`.
6. **Exit code 0**: nothing was left undone that the config asked for. `--output json`:
   `status: completed`, `exit_code: 0`, `mr: null`, `mr_skipped: "push disabled
   (delivery.push: false)"`, `branch` and `commit` set.
7. `summary.md`: the Merge request row reads `skipped: push disabled (delivery.push: false)`.
   `report.md` is written as today (it is the reviewer's report too).

### 3. The checks before the worktree (§50.7)

With `push: false` the remote is not needed: the `git remote get-url <remote>` check is skipped
(in `run`, in `orchestrator validate` and in `story run`'s service check), and so is the target
check of a detached HEAD (there is no merge request to target). The rule that
`security.secret_scan.mode` must not be `off` stays: the branch is still meant to be published
later, by a person.

### 4. `story run`

The parent reads each service's `delivery.push` in its service check (`checkDeliverable`, which
already loads the service's config) and keeps it per service.

- **Service check**: a service with `push: false` needs no remote; the secret-scan rule stays.
- **The spec branch** (`story/<slug>/spec`) is created and merged into locally as today, and is
  **not pushed** for a service with `push: false` (`bases.<repo>.spec_push` stays absent). If a
  later `story run` finds the service's `push` turned on, the existing continued-story path pushes
  it before the first merge request targets it.
- **Stacking**: a task stacked on a done parent uses the parent's recorded commit, which exists
  locally: nothing changes. `withoutMR` holds nothing in a service with `push: false`: the dependent
  opens no merge request, so it needs no parent's branch on the remote, whatever the parent's run
  did (a parent whose push failed before the service turned the push off is stacked on too).
- **The child** is the ordinary run of §2: exit 0 with a commit is `done`; `tasks.<ID>.mr` stays
  absent.
- **The end**: the end table shows `—` in the merge-request column for these tasks, and the story
  prints one line per service with unpushed branches:
  `Not pushed (delivery.push: false): git push <remote> <branch> <branch> …` in the story's order,
  the spec branch first when it carries a merge.
- Several services in one story may differ: each task follows the option its own run recorded.

### 5. Resume

`options.push` from `run.json` decides; a run recorded without it pushes, as before. A run
interrupted after its commit and before the push with `push: true` keeps today's behaviour.

## Contracts

- `config.Delivery.Push *bool` (absent = true), `Delivery.PushEnabled() bool`.
- `domain.RunOptions.Push *bool` (`json:"push,omitempty"`), validated on load (a boolean or
  absent); a helper `RunOptions.Pushes() bool`.
- `domain.MRSkippedPushDisabled = "push disabled (delivery.push: false)"`.
- The exit code of a completed worktree run with a commit and `options.push: false` is 0;
  `exitcode` and the §4 table are unchanged otherwise.

## Out of scope

A `--push`/`--no-push` flag; GitHub pull requests; pushing without opening a merge request
(`push: true` without the `merge_request.*` options); an org rule forcing the push.

## Risks

- An operator turns the push off and forgets the branches: the console line and `summary.md` say
  what to push, and the story's end prints the command.
- A story continued after the option changed from `false` to `true`: the parents done without a
  merge request are held for as today (`withoutMR`), since a dependent's merge request would target
  a branch that is not on the remote; the operator pushes them and records their merge requests,
  as the hint says.

## Decisions

### Owner (2026-10-29)

| | decision |
|---|---|
| D1 | A config key `delivery.push: false` turns the push (and so the merge request) off |

| D2 | (later the same day) The key is the story's: `story.push` in the story home's config, for every service of the story; `delivery` has no `push` key and a plain `run` always pushes |

### Author's defaults (accepted with "сделай сразу", 2026-10-29)

| | default |
|---|---|
| A1 | Exit 0, not 6: the config declined the push, as a person's no does today |
| A2 | The worktree is removed after the commit as today; the branch is the result |
| A3 | `secret_scan.mode: off` stays refused for a worktree run: the branch is meant to be published later |
| A4 | No CLI flag; the key is per repository (each service's own config in a story) |
| A5 | The story's spec branch is not pushed for a service with `push: false` |
| A6 | Recorded in `run.json: options.push`; resume never reads the current config for it |
