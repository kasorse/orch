---
id: DP03
repo: orche
depends_on: [DP02]
jira: none
---
# DP03 — The documentation of delivery.push

**Size** S

## Goal

The shared documents DP01 and DP02 made out of date. Read their commits for what they left here.

## Files

- MVP.md: §50.1 and §50.5 (the chain ends at the commit with `push: false`), §50.7 (the key and
  the skipped remote check), §50.10 (`mr_skipped`), §50.12 (`options.push`), §47.5, §48.6 and
  §50.8 (story without a push), §4 row 6 (a disabled push is not exit 6), §43 (the summary row).
- CLAUDE.md: the delivery invariant ("ask unless `--mode auto`, the push opens the merge
  request") gains `delivery.push: false` as the second way out next to `--inplace`; the run
  artifacts line for `options`.
- `orchestrator.example.yaml`: `push` under `delivery`, commented, with when to use it (a remote
  that is not GitLab, a local-only story).
- README.md: one line where delivery is described.
- TODO.md: a done item for this spec under phase 3.

## Acceptance

- MVP.md §50 describes `delivery.push: false` and no longer says the push cannot be turned off.
- CLAUDE.md names `delivery.push: false` in the delivery invariant and `options.push` in the run artifacts.
- `orchestrator.example.yaml` loads with `orchestrator validate` with the new line uncommented.
- TODO.md has the done item.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/delivery-push/` (story run writes its README and As built).

## As built (2026-10-29)

MVP.md §4 (exit 6 row), §43 (the Merge request row), §47.5, §50.1, §50.5, §50.7 (the key, the
skipped checks), §50.8 (a service that does not push), §50.10 (`--output json`), §50.12
(`options.push`); CLAUDE.md (the opening pointer, the delivery invariant, the run artifacts'
`options`); `orchestrator.example.yaml`; README.md (the delivery paragraph); TODO.md (item DP).
