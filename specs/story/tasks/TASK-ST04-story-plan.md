# ST04 — `story plan`

**Size** M (1.5 d) · **Depends on** ST01, ST03 · **Blocks** ST10

## Goal

`orchestrator story plan "<request>" [--jira KEY] [--repo NAME]...` writes a spec or explains why
it did not (`SPEC.md` §6, §7.3, §8).

## Files

- `internal/app/story_plan.go`: parse args (`--repo` repeatable, names in `story.repos`); the
  home's fail-fast chain (with Jira dependencies for `--jira`); each named repository resolved
  and checked as a git checkout; the story directory `.orchestrator/stories/.plan-<id>/`; with
  `--jira` one `jira.Fetch` (§39) rendered as the request, `ticket.json` saved; a survey per
  repository, then specify; `story.Validate`; one task → print the run command, write nothing
  under `spec_dir`; otherwise render `<spec_dir>/<slug>/` (refuse an existing directory), rename
  the story directory to `<slug>`, write `story.json` (`status: planned`, `jira_parent`, the
  calls' usage)
- `internal/app/app.go`: the `story` command and its usage line

## Acceptance

- Integration (fake executables): two repositories, a valid answer → the spec on disk, README
  and task files as golden; an invalid answer → nothing under `spec_dir`, the answer in
  `artifacts/specify.json`, exit 1 with the field named; a one-task answer → the run command
  printed; `--repo unknown` → config error before any call; an existing `specs/<slug>` →
  refused before writing.

## Gates

`scripts/check.sh`
