---
id: RV03
depends_on: [RV01, RV02]
---
# RV03 — `orchestrator revise`: the target, its resolution, `task.source: revise`

**Size** M

## Goal

SPEC §1, §4, §5, §7 (the record): the command, the target's kind, the resolution into
`domain.Revision` with update/new, `task.source {kind: revise}`, the original task stored. The
run then stops before the worktree with "not built yet" until RV04 (or RV03 and RV04 merge
together).

## Files

- `internal/app/revise.go` (new): argument parsing (refusals of `--inplace`, `--stage`, `--jira`,
  `--story-task`), `targetKind`, `resolveRevision` for the four kinds (a Jira target via
  `jira.Fetch` + `RemoteLinks` + the API; a run target via the persistence loaders); the
  project from `delivery.remote`'s URL; the refusals of SPEC §4 (another project, a fork, no
  merge request, locked); the line printed before the approval.
- `internal/domain`: `TaskSourceRevise`, `Revision`, its strict validation on load.
- `internal/app/help.go`: the command's help.
- `artifacts/revise-original.txt` written and read back with its validation.
- Tests: `targetKind` table; resolution with a fake GitLab and a fake Jira for each kind and
  each refusal; "latest" among three links of which one is another project's.
- Docs: MVP.md §54.3–§54.5; README.md command list.

## Acceptance

- `revise PAY-31 "…"` in a checkout of `acme/payments/billing` picks the link with the latest `created_at` of that project.
- A merged latest merge request resolves to `mode: new` with the base `target_branch`.
- A resolution failure creates no run directory.
- A run target reads the run with the persistence validators and refuses a run with no branch.
- `scripts/check.sh` passes.
