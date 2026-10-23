# DL12 — Documentation

**Size** S (1 d) · **Depends on** DL11 · **Blocks** —

## Goal

MVP.md becomes the normative home; CLAUDE.md, README, the example config and TODO.md agree.

## Files

- MVP.md: new §49 "Delivery" (normative, from `SPEC.md` §4–§14); §4 the exit code table and the new
  flags and command; §30 the GitLab line ("one push of the run's branch, one merge request, one
  note") and the Jira line (comment and transition after an MR); §39.12 the write-back; §42.2 the
  type and the trailer; §42.5, §47.7, §48.10 updated; §43 the `report.md` pointer and the "Merge
  request" row; §47.2 the branch name (instructions, template, fallback) and the resume check.
- CLAUDE.md: a GitLab invariant (as the Jira one), the Jira invariant's write list, `review` in the
  architecture summary, run artifacts (`report.md`, `mr`, `jira_write`, `review_target`,
  `mr_note`, `options.mr`, `options.jira_write`, `diff.run_files`, `branch_naming`,
  `logs/branch-name.*`), the worktree invariant (the branch is no longer derived from the run ID;
  the path still is), the spec pointer.
- README.md: `--mr`, `orchestrator review`, `--output json`, exit codes, CI.
- `orchestrator.example.yaml`: `gitlab:`, `jira.write` and `worktree.branch` commented out.
- TODO.md: W1–W5 ticked with a pointer here; this README's `Status:`.

## Gates

`scripts/check.sh`
