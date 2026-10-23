# DL13 — The branch name: instructions, template, fallback

**Size** M (2.5 d) · **Depends on** — · **Blocks** DL11

## Goal

A worktree run's branch is named, when the worktree is created, from the repository's
instructions (a tool-less call), else from `worktree.branch.template`, else
`orchestrator/<run-id>`; Go checks every name (`SPEC.md` §4.3).

## Files

- `internal/config`: `worktree.branch {template, from_instructions {provider, model, timeout}}`;
  the template's placeholders checked (`{id}`, `{key}`, `{slug}`, `{date}`; anything else a config
  error); `from_instructions` goes through the fail-fast chain like the translator (executable,
  `AuthorityNone` supported, CLI version, credentials).
- `internal/domain/branch.go`: `RenderBranchTemplate(tmpl, vars)` (empty placeholder takes its
  following separator), `Slug(text)` (Cyrillic table, `[a-z0-9-]`, ≤ 40 bytes at a `-`),
  `ValidBranchName(name)` (characters, length, no `..`/`//`/`.lock`, no leading `-`/`/`, not under
  `story/`). `RunWorktreeBranch` reads `worktree.branch` for a non-story run instead of deriving it;
  `Run` validation applies `ValidBranchName` (story pattern unchanged).
- `internal/action/branchname.go`: the prompt (instructions fenced, task through `taskBlock`, key,
  run ID, date; the `{"branch": ...}` answer) and the strict parse; a golden prompt.
- `internal/app/worktree.go`: before `git worktree add`: read the three files with
  `git show <base_ref>:<file>` (absent is fine, bound 32 KiB, keyword gate), the call, the checks
  that need git (`check-ref-format --branch`, not HEAD/base/target, not an existing local branch),
  the template fallback with the one `-<6>` suffix, the default; the warning; `run.json:
  branch_naming`; `logs/branch-name.*`; usage into `outcome.usage` and `decision_overhead`.
- `--story-task` and the review run skip all of it.
- Resume: `verifyRunWorktree` checks the recorded branch with `ValidBranchName` and against
  `git worktree list` (the path stays derived).
- `internal/report/summary.go`: the Worktree row says where the name came from.

## Acceptance

- Unit: template rendering (with and without a key, Cyrillic slug, long slug cut, unknown
  placeholder), name checks table, the suffix on a taken template name.
- Fake CLI: instructions mention branches → the call is made and its name used; no mention → no
  call; the answer `""` → template; an invalid or existing name → warning, template; a timeout →
  warning, template; `FailureAuth` → fatal; the file read from `base_ref`, an uncommitted edit of
  CLAUDE.md ignored.
- A resumed run with a templated branch continues; a `run.json` whose branch was edited to another
  worktree's branch is refused.
- A story task's branch is unchanged with `worktree.branch` set.

## Gates

`scripts/check.sh`
