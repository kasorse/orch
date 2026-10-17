# WT02 — repository: worktrees, `.git` pointer, fingerprint, `merge-tree`

**Size** M (2 d) · **Depends on** — · **Blocks** WT04

## Goal

`internal/repository` can create, list and remove a linked worktree, read its `.git` pointer
without git, fingerprint what a linked worktree adds, and ask git for merge conflicts
(`SPEC.md` §7, §8.3, §10.3, §10.4). The package comment's list of what the orchestrator writes
gains the run's worktree and branch.

## Files

- `internal/repository/worktree.go`:
  - `AddWorktree(ctx, path, branch, base string) (adminDir string, err)` — `git worktree add
    --quiet -b <branch> <path> <base>`; `base` must be a full SHA and `branch` a valid name
    (`git check-ref-format --branch`), refused before git otherwise; returns the admin
    directory (`git rev-parse --path-format=absolute --git-dir` in the new worktree)
  - `ListWorktrees(ctx) ([]Worktree{Path, Branch, Head}, error)` — parses `git worktree list
    --porcelain -z`
  - `RemoveWorktree(ctx, path) error` — `git worktree remove <path>` without `--force`; a refusal
    is a distinct error the caller reports as "kept"
  - `PruneWorktrees(ctx) error`
  - `ReadGitPointer(worktreeDir string) (string, error)` — reads `<dir>/.git` in Go: a regular
    file (not a symlink, not a directory), at most 4 KiB, exactly `gitdir: <abs path>\n`
  - `MergeConflicts(ctx, ours, theirs string) (files []string, supported bool, err)` —
    `git merge-tree --write-tree --name-only --no-messages`; exit 1 → the conflicting files;
    `supported` false on git < 2.38 (`git version` parsed once)
- `internal/repository/git.go`: `GitFingerprint` also hashes the absolute `--git-dir` path and,
  when it differs from the common directory, `<git-dir>/config.worktree`
- tests with real git (like `git_real_test.go`)

## Acceptance

- Real git: add a worktree from a SHA on a new branch; list it; the pointer reads back as the
  admin dir; remove it; removing one with an untracked file is refused and the directory stays.
- `ReadGitPointer` refuses a symlink, a directory, trailing data, a relative path.
- Fingerprint: writing `config.worktree` in a worktree's admin dir changes it; the main tree's
  fingerprint is unchanged by this task (same value as before for a non-linked checkout).
- `MergeConflicts`: a conflicting pair lists the file; a clean pair lists none.
- `AddWorktree` with `base` = `"-x"` or a branch name with `..` runs no git.

## Gates

`scripts/check.sh`
