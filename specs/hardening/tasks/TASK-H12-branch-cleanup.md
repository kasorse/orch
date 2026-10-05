# H12 — Delete merged branches

**Size** S (15 min) · **TODO** S6 · **Depends on** —

## Goal

Remove the branches already merged into master, locally and on origin (answer Q10).

## Steps

1. `git fetch --prune origin`
2. Verify each is merged — the command must list all five:
   ```
   git branch --merged master
   ```
   Expected: `feature/permissions`, `feature/resume`, `jev-integration`,
   `mvp-review-followups`, `review-severity-rubric`. Any branch **not** listed is skipped and
   reported, never force-deleted.
3. Local: `git branch -d <branch>` for each listed one (`-d`, never `-D`).
4. Remote (only those that exist on origin; on 2026-10-05: `feature/permissions`,
   `feature/resume`, `jev-integration`):
   ```
   git branch -r --merged origin/master
   git push origin --delete <branch>
   ```
5. `git branch -a` shows only `master` and `origin/master` (plus anything skipped).

## Acceptance

No merged branch left locally or on origin; nothing unmerged deleted.

## As built

Done by the owner before 2026-10-06; `git fetch --prune` and `git branch -a` then showed only
`master` and the phase's own `feature/hardening`.
