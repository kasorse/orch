# DL12 — Documentation

**Size** S (1 d) · **Depends on** DL11 · **Blocks** —

## Goal

MVP.md becomes the normative home; CLAUDE.md, README, the example config and TODO.md agree.

## Files

- MVP.md: new §49 "Delivery" (normative, from `SPEC.md` §4–§14); §4 the exit code table and the new
  flags and command; §30 the GitLab line ("one push of the run's branch, which opens one merge request") and the Jira line (comment and transition after an MR); §39.12 the write-back; §42.2 the
  type and the trailer; §42.5, §47.7, §48.10 updated; §43 the `report.md` pointer and the "Merge
  request" row; §47.2 the branch name (instructions, template, fallback) and the resume check;
  §48.4/§48.5 a story task's branch by its repository's rules, `story/<slug>/<ID>` the fallback,
  the task input's `title`, `story.json: tasks.<ID>.branch` from the child's run.
- MVP.md §47.1: `worktree` is the default, `--inplace` opts out; §47.5 and §42.1/§42.6: the chain, asked
  in every mode but `auto`, `auto_commit` not for worktree runs.
- CLAUDE.md: a delivery invariant (worktree by default, commit/push/MR asked unless `--mode auto`,
  the push opens the MR, no GitLab credentials), the worktree invariant's default, the Jira invariant's write list, `review` in the
  architecture summary, run artifacts (`report.md`, `mr`, `jira_write`, `review_target`,
  `options.jira_write`, `diff.run_files`, `branch_naming`,
  `logs/branch-name.*`), the worktree invariant (the branch is no longer derived from the run ID;
  the path still is), the spec pointer.
- README.md: the worktree default and the chain, `orchestrator review`, `--output json`, exit codes, CI.
- `orchestrator.example.yaml`: `delivery:`, `jira.write` and `worktree.branch` commented out.
- TODO.md: W1–W5 ticked with a pointer here; this README's `Status:`.

## Gates

`scripts/check.sh`
