# T05 — Clean worktree, restore point, protected-path guard after `implement`

**Phase** 2 · **Size** M · **Depends on** T03 · **Blocks** T08

## Goal

A run that writes starts from a named restore point, and a write that could turn into later
execution stops the run before `test` (`SPEC.md` §7.2–7.3, D8, D9).

## Files

- `internal/config/config.go`, `validator.go` — `SecurityConfig{RequireCleanWorktree, ProtectedPaths}`
- `internal/repository/git.go` — `HeadRef`, `StashCreate`, `GitFingerprint`
- `internal/action/implement.go` — the guard
- `internal/interaction/gate.go` — change summary before the step after `implement`
- `internal/domain/run.go` — `Run.PreRunRef`
- `internal/app/app.go` — worktree check, `pre_run_ref`
- tests (unit + one integration scenario)

## Steps

1. `security:` block. `require_clean_worktree` via `*bool` in the YAML shape (absent → true,
   explicit false → false; test both). `protected_paths` entries must be valid globs.
2. After `workflow.Build`, if the workflow contains `implement`: dirty tree + key true →
   `*apperr.ConfigError` (exit 2; `RepositoryError` maps to 1) naming both remedies, before `ReviewAndSelectMode`. Otherwise
   keep `DirtyRepoWarning`. Record `PreRunRef` (`HeadRef` if clean, else `StashCreate`, falling
   back to `HeadRef` when stash prints nothing); failure to record is logged, not fatal.
   Print `Restore point: <sha>`.
3. Guard in `executeImplement`, around the provider call:
   - before: `GitFingerprint` = sha256 of `.git/config` + sorted names and contents of
     non-`.sample` files in `.git/hooks/` (resolve the git dir with `git rev-parse --git-dir`,
     so worktrees work);
   - after: fingerprint unchanged, and no `ChangedFiles` entry matches the protected set
     (`.claude/**`, `.mcp.json`, `.github/workflows/**`, `.gitlab-ci.yml`, `.envrc`,
     `.vscode/**`, the config file path when inside the worktree, `security.protected_paths`).
     An untracked directory reported as a whole matches if the pattern could match inside it.
   - violation → `*apperr.WorkflowError` listing paths and the restore point; the step fails,
     so the engine stops the run (no transition to `test`).
4. `manual` gate: when the previous step was `implement`, print `git diff --stat` and
   `protected paths: ok` before asking. Pass the data in; the gate does not run git itself.

## Acceptance

- Dirty tree + workflow with `implement` → exit 2, no step ran, no
  approval prompt shown. Dirty tree + read-only workflow → runs with the warning.
- `run.json` has `pre_run_ref` whenever steps execute.
- Integration: a fake `implement` that writes `.claude/settings.json` → run fails at
  `implement`, `test` never executes (fake test command not invoked). Same for appending to
  `.git/config` and for adding `.git/hooks/pre-commit`. A normal source edit passes.
- `security.protected_paths: ["go.mod"]` protects `go.mod`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
