---
id: DL03
repo: orche
depends_on: []
jira: none
---
# DL03 — Worktree by default, the `delivery` config, the checks before the worktree

**Size** S–M

## Goal

`execution.isolation` defaults to `worktree` again, `--inplace` opts out, and a run that will be a
worktree run is refused before the worktree when it could not deliver (`SPEC.md` §6, §8).

## Files

- `internal/config/config.go`: `IsolationMode()` returns `worktree` when the key is absent; the
  comment and `worktree.setup`'s rule follow (setup runs in every worktree run).
- `internal/config/delivery.go`: `DeliveryConfig` (`remote` default `origin`, `target_branch`),
  strict schema.
- `internal/app`: after the workflow is approved (with `--stage`, before any call) and before the
  worktree is created, for a worktree run: `git remote get-url <remote>`, a target (the current
  branch or `delivery.target_branch`), `secret_scan.mode` not `off`; each failure a `ConfigError`
  naming `--inplace`. The unborn-HEAD message names `--inplace`.
- `internal/app/validate.go`: checks 1 and 3 when the configured isolation is `worktree`.
- `internal/repository`: `RemoteURL` (`git remote get-url`).
- Every existing unit test, integration scenario and bench fixture that reads a run's result in
  the operator's working tree pins `isolation: inplace` (or passes `--inplace`); scenarios about
  worktrees drop their `--worktree`/`isolation: worktree` where it is now the default.

## Acceptance

- No `isolation` key, workflow with `implement` → worktree run; `--inplace` → in place; a workflow
  without `implement` → in place.
- No remote / detached HEAD without `delivery.target_branch` / `secret_scan.mode: off` → exit 2
  naming `--inplace`, no worktree created, no step run; with `--stage development` no provider call.
- A workflow without `implement` in a repository without a remote runs.
- Config: strict keys, defaults applied.

## Gates

`scripts/check.sh`

## Notes

Do not edit anything under `specs/delivery/` (story run writes its README and As built). Leave
MVP.md, CLAUDE.md, TODO.md and README.md to DL12.

## As built (2026-10-27)

Run `848d8366-2a58-4ff1-aeca-9b18f01e873c` in orche: completed, 1 fix iteration, tests passed, review pass.
Branch `story/delivery/DL03` on `spec/delivery` @ 6d3f006, commit 51c64b4.
Cost $3.17. Implementation summary: Default to worktree runs and refuse undeliverable ones before any call.
Conflicts with spec/delivery: none.
