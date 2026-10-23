# DL06 — Jira write-back: comment and transition

**Size** M (2 d) · **Depends on** DL04 · **Blocks** DL11

## Goal

After an MR, comment on the task's Jira issue and move it by a configured transition
(`SPEC.md` §10).

## Files

- `internal/config/jira.go`: `jira.write {comment, transition}`; `DependencyOptions{Jira: true}`
  also when `--mr` and `jira.write` are set and the task comes from Jira.
- `internal/jira/write.go`: `Comment(ctx, key, wiki)`, `Transitions(ctx, key)`,
  `Transition(ctx, key, id)`; the create rules (base_url only, no redirect, strict answers, retry
  only 429/503, `MayExist`). The comment text via the existing wiki escaping.
- `internal/domain/run.go`: `options.jira_write`, `jira_write` and their validation.
- `internal/app/deliver.go`: after the MR is recorded: comment (intent → id), transition (by
  `name` or `to.name`, case-insensitive; `unchanged` when the issue is already in that status;
  `missing` with the available names in a warning). Failures are warnings only.
- `resume` uses `options.jira_write`; it never fetches the issue.

## Acceptance

- Fake Jira: the comment body; a transition found by name and by target status; none found → a
  warning listing names; already in status → nothing posted; a 500 after sending → intent kept, no
  retry; credentials never in output.
- No MR (skipped) → nothing written to Jira; a typed task (no key) → nothing written.

## Gates

`scripts/check.sh`
