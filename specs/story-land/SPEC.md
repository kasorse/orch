# Landing a story that does not push: the base branch moves to the result

## Problem

With `delivery.push: false` (`specs/delivery-push`) a story ends with every task committed on its
own local branch and prints `Not pushed (delivery.push: false): git push …`. Nothing brings the
result into the branch the story was started from: with a push, the merge requests do it in
GitLab; without one, the operator does it by hand. The `jira-listener` story (2026-10-29, six
chained tasks) ended that way: the operator fast-forwarded `spec/jira-listener` to JL06's branch
and committed the README and the `## As built` sections `story run` had left uncommitted in the
tree.

The owner (2026-10-29): "после завершения всех тасок я ожидаю что это будет автоматически" - once
every task is done, the result is merged into the branch automatically.

## Approach

### 1. When

At the end of a completed story (`finish`), for every service whose config says
`delivery.push: false`, that has at least one done task with a commit, and whose base was a branch
(`story.json: bases.<repo>.branch`; a story started on a detached HEAD lands nothing and says so).
A later `story run` of the completed story lands again: the land is derived from git, so a land
that is already done changes nothing, and one that was refused is tried again.

A service that pushes is unchanged: its merge requests carry the result.

### 2. The result commit

Go starts at the base branch's current tip `B` (the branch may have moved since the story started;
gone: not landed) and takes the service's **leaves**: its done tasks that no other task of the
service starts from (`story.Parents`), in the story's order. Each in turn, as the merge of a task's
parents on the spec branch (MVP.md §48.4):

- already in the tip: nothing;
- a descendant of the tip: it becomes the tip (a fast-forward);
- otherwise `merge-tree` and `commit-tree` (objects only), the message
  `Merge <task branch> into <base>` and the `Orchestrator-Story: <slug>` trailer. A conflict: not
  landed, the files listed.

The git fingerprint of the service is compared with the one the story's checks took before the
first `merge-tree`, the merge or the commit below (each can run the repository's code).

### 3. Moving the base branch

With the result `T` different from `B`:

- **The base branch is checked out in the service's checkout** (the directory in
  `story.json: services`): `git merge --ff-only --quiet <T>` there. Git refuses rather than
  overwrite a local change or an untracked file; its refusal is "not landed", and nothing moved.
- **It is checked out in another worktree** (`git worktree list`, under the worktrees lock): not
  landed, the hint says to fast-forward that worktree.
- **It is not checked out**: `git update-ref refs/heads/<base> <T> <B>`, so a branch that moved
  meanwhile is not overwritten.

This is the one place where Go writes the operator's branch and checkout: the `story run`
confirmation of a story whose service does not push is the operator's yes to it.

### 4. The story's own files

When the story home (the repository of the spec directory) is that service's checkout and is on the
base branch after the land, the files `story run` writes there and that git reports as changed -
`<spec dir>/README.md` and the task files - are committed with `git commit -- <paths>` (the
repository's hooks run, as for `CommitAll`; nothing else the operator staged goes in), the message
`Record the <slug> story: README and As built` with the trailer. Nothing changed: no commit.

### 5. The console and the exit code

- Landed: `Landed on <base>: <short sha> (<branches>)`, then, when the story files were committed,
  `Recorded the story on <base>: <short sha>`, then
  `Not pushed (delivery.push: false): git push <remote> <base>`.
- Nothing to land (`T == B`): `<base> already has the story's result`.
- Not landed: `Not landed on <base>: <why>`, the hints (the manual merge, `story run <dir>` to try
  again), the previous `Not pushed …` line with the tasks' branches, and the story ends with exit
  code 6 (completed, needs a person). `story.json` keeps `status: completed`.

### 6. Records

Nothing new in `story.json`: git is the truth, and a later `story run` finds the leaves already in
the base branch.

## Contracts

- `repository.Repository.MergeFastForward(ctx, commit) error`: `git merge --ff-only --quiet`;
  a refusal is a `RepositoryError` with git's message.
- `repository.Repository.CommitPaths(ctx, message, paths) (string, error)`: `git add --` then
  `git commit --quiet --file=- --` on those paths only.
- `story run` of a completed story with a service that does not push and a land that was refused
  returns `apperr.NeedsPersonError`.

## Out of scope

A key or flag that turns the land off; landing for a service that pushes; a rebase or a squash;
pushing the base branch; landing a story that is not completed.

## Risks

- The operator's checkout changes under them at the end of a story: only by a fast-forward of the
  branch the story was started from, and git refuses what would lose a change.
- Hooks of the operator's repository run in the merge and the commit: the fingerprint is compared
  first, as before the story's other merges.

## Decisions

### Owner (2026-10-29)

| | decision |
|---|---|
| D1 | When every task of a story is done, the result is merged into the branch automatically |

### Author's defaults (accepted with "оформи спекой и сделай сразу", 2026-10-29)

| | default |
|---|---|
| A1 | Only for a service with `delivery.push: false`; a service that pushes lands through its merge requests |
| A2 | A fast-forward when the base branch did not move and the tasks are one chain; a merge commit made by Go otherwise |
| A3 | The checked-out base branch moves by `git merge --ff-only` in the checkout; git's refusal is "not landed" |
| A4 | The README and the As built sections are committed on the base branch when the story home is the service |
| A5 | Not landed is exit 6 and a later `story run` tries again; no record in `story.json` |
| A6 | No config key to turn it off |
