# WT09 — live checks V-wt-1…3

**Size** S (1 d) · **Depends on** WT04 · **Blocks** WT10

## Goal

The confinement a worktree run relies on is verified against the real claude CLI
(`SPEC.md` §12), with positive controls.

## Files

- `test/security/worktree_test.go` (`-tags security`), using the existing fixture helpers on a
  repository with a linked worktree:
  - **V-wt-1:** authority `read` in the worktree: quotes a worktree file; cannot quote a file in
    the operator's tree by absolute path, nor `<operator>/.orchestrator/runs/<id>/run.json`
  - **V-wt-2:** authority `edit` in the worktree: writes `notes.txt`; cannot write a file in the
    operator's tree; an attempt to rewrite the worktree's `.git` file either fails, or (recorded
    in the test's output) succeeds and `guard.RepoGuard.Changed` reports `.git (worktree
    pointer)`
  - **V-wt-3:** a hook in the operator's `.claude/settings.json` does not run for a call in the
    worktree
- `internal/provider/claude/client.go`: the comment on confinement names the version verified in
  a worktree

## Acceptance

`go test -tags security ./test/security/...` passes on the current claude CLI version; the
result and the version go into this file's As built and `specs/permissions/` if a behaviour
differs from the plan.

## Gates

`scripts/check.sh`; `go test -tags security ./test/security/...`
