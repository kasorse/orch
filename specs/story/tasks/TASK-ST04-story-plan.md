# ST04 — `story plan`

**Size** M (1.5 d) · **Depends on** ST01, ST03 · **Blocks** ST10

## Goal

`orchestrator story plan "<request>" [--jira KEY] [--repo NAME]...` writes a spec or explains why
it did not (`SPEC.md` §6, §7.4, §8).

## Files

- `internal/jira`: the GET also asks for `components` (`systemFields`; its request test
  changes); `domain.Ticket.Components` (names), strictly validated, recorded in `ticket.json`;
  the rendered task text is unchanged (its golden files stay)
- `internal/app/story_plan.go`, in this order: parse args (`--repo` repeatable); the home's
  fail-fast chain (with Jira dependencies for `--jira`); with `--jira` one `jira.Fetch` (§39)
  rendered as the request; `findServices` (ST02) over the request, the components and `--repo`,
  and the `Services:` line - a refusal here makes no call and creates no story directory; the
  story directory `.orchestrator/stories/.plan-<id>/` with `ticket.json`; a survey per service,
  then specify with the services as the closed list; `story.Validate`; one task → print the run
  command, write nothing under `spec_dir`; otherwise render `<spec_dir>/<slug>/` (refuse an
  existing directory), rename the story directory to `<slug>`, write `story.json`
  (`status: planned`, `jira_parent`, `services`, the calls' usage)
- `internal/app/app.go`: the `story` command and its usage line

## Acceptance

- Integration (fake executables): two repositories, a valid answer → the spec on disk, README
  and task files as golden; an invalid answer → nothing under `spec_dir`, the answer in
  `artifacts/specify.json`, exit 1 with the field named; a one-task answer → the run command
  printed; a task that names no service → the §7.1 message, no call, no story directory; a
  ticket whose component names a service → that service surveyed; three services under the
  roots and a task naming two → exactly two survey calls; `--repo unknown` → config error before
  any call; an existing `specs/<slug>` → refused before writing.

## Gates

`scripts/check.sh`
