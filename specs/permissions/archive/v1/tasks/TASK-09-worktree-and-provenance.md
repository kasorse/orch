# T09 — Revertible worktree, `pre_run_ref`, and config provenance

**Phase** 3 · **Size** M · **Depends on** T05, T06 · **Blocks** T12

## Goal

Two independent preconditions that make the rest of the model operationally true: a write
step always has a named point to return to (F10), and a `test.command` that came from inside
an untrusted repository does not get shell-executed (F7, Q6).

## Files

- `internal/app/app.go` — both checks
- `internal/repository/repository.go` — `StashCreate`/`HeadRef`, and the two provenance git
  calls
- `internal/logging/progress.go` — the new error/warning text
- tests

## What to do

### Clean worktree (§8.11)

1. When `security.require_clean_worktree` is true **and the built workflow contains a
   write-capable step**, a dirty worktree is an error, upgrading today's
   `logging.DirtyRepoWarning` at `app.go:155-159`. A `question`/`review`/`test`-only workflow
   has no write step and must keep running on a dirty tree — that is a common, legitimate use
   and blocking it would get the key set to `false` permanently, losing the control where it
   matters.
2. Ordering: the workflow is only known after classification, which is an AI call. Checking
   this late means the operator pays for a classification before being told to stash. Prefer
   checking **before** classification whenever the mode/config makes a write step possible at
   all, and re-checking after `workflow.Build` for the precise case. If you check only once,
   check early and accept a false positive on read-only workflows — but then say so in the
   message ("workflow may include an implement step"), because a misleading error is worse
   than a slightly over-eager one. Write down which you chose and why.
3. The error names both ways out: commit/stash, or `security.require_clean_worktree: false`.
4. Before the first `implement` step, record `git stash create`'s ref in `Run.PreRunRef`
   (T06 added the field), falling back to `git rev-parse HEAD` when the tree is clean and
   `stash create` outputs nothing. Print it in the run output — a ref nobody sees is not an
   undo path. Failing to record it is not fatal to the run; log it and continue.

### Config provenance (§8.12, Q6)

5. Print the resolved absolute config path at `validate` time and at run start, always.
6. When the config file resolves **inside** the repository worktree **and** `test.command` is
   non-empty, check its git state:
   - untracked (`git ls-files --error-unmatch <path>` fails) **or** modified
     (`git status --porcelain -- <path>` non-empty) → **refuse** with `*apperr.ConfigError`
     (exit 2). This is the planted/tampered shape.
   - tracked and clean → warn, stating that `test.command` is shell-executed and that a
     config inside a repository you do not control is untrusted input.
7. The check must not apply when the config is outside the worktree (the explicit-`--config`
   case) and must not fail the run when the path is outside any git repo. Do not let a git
   error here become a refusal — a broken provenance probe should degrade to the warning, not
   block a legitimate run.

## Acceptance

- Dirty tree + workflow with `implement` → exit 2, message naming both remedies; no AI call
  after the check.
- Dirty tree + `question` → runs (this is the regression most likely to be introduced).
- `require_clean_worktree: false` → warning only, today's behavior.
- `Run.PreRunRef` is set and appears in `run.json` and in the run output for any run with a
  write step.
- Untracked in-repo `orchestrator.yaml` with `test.command` → refused; tracked+clean → warning;
  `--config` from outside the worktree → neither.
- `test.command` empty → no provenance refusal regardless of location.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
