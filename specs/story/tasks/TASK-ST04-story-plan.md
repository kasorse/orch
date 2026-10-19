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

## As built

2026-10-19. `scripts/check.sh` is the run's test step; it was not run while writing the change.

- **Exit code of an invalid answer: 3, not 1.** ST03 fixed `*apperr.InvalidResponseError` at
  exit 3 (like an invalid decision answer; `exitcode_test.go` covers it, wrapped too), and
  `story plan` returns that error as it is, so the field stays named (`specify: invalid
  response: tasks[0].repo: …`). The scenario checks a non-zero code and the field.
- **Jira.** `systemFields` is `summary,description,issuetype,parent,components`. `fields.components`
  is absent, `null` or an array of at most 50 objects whose `name` is a non-empty one-line string
  of at most 255 bytes (`fields.components[<i>].name: missing or empty`, `…: not one line`,
  `fields.components[<i>]: not an object`, `fields.components: not an array`); only `name` is
  read, exact duplicates are dropped. `Ticket.Components` is in `ticket.json`; `jira.Render`
  does not read it, so its goldens are unchanged. `specs/jira/SPEC.md` §8 and MVP.md §39 say so.
- **Command line.** `story plan` takes `--jira` and the repeatable `--repo` (empty refused,
  duplicates dropped); `--jira` elsewhere is now `--jira is only valid with run and story plan`,
  and `--repo` elsewhere `--repo is only valid with story plan`. `story` without `plan`, or
  without a task and `--jira`, is the usage. `--claude-auth` is not taken: the home's config and
  `auto` decide the claude credential mode.
- **Order.** The home's whole fail-fast chain (with the Jira variables for `--jira`), then a
  missing `story` section is refused, then the one fetch, `ResolveRoots`, `candidates`,
  `findServices` (its refusals, `--repo unknown` among them, become `ConfigError`, exit 2), the
  `Services:` line, and every service's own config - all before `.orchestrator/stories` exists
  or anything is called.
- **A service's config** (`loadStoryService`): `<service>/orchestrator.yaml` through
  `config.LoadLayered` (with the org layer), `Validate`, `ValidateAuthorities` and the CLI
  version check; not the dependency checks, the claude auth resolution or the sandbox probe,
  which concern runs in that repository. The survey takes that config's `actions.analyze` route
  and timeout and `context.extra_deny`, and `.orchestrator/runs` as the runs directory. A
  service without the file is a `ConfigError` naming it; the home, when it is a service, uses
  its already validated config.
- **The story directory** is `internal/persistence/story.go`: `NewStoryPlan` makes
  `.orchestrator/stories/.plan-<uuid>/` with `artifacts/` and `logs/` (0o755);
  `SaveTicket`; `SaveArtifact` writes `artifacts/survey-<repo>.json` and `artifacts/specify.json`
  as `{answer, error, usage, output, prompt}` - `answer` is `null` and `error` set when the call
  failed or its answer was refused, `output` is the raw answer either way; `AppendCallLog` /
  `CallLogPath` (`logs/<call>.<stream>.log`) make it the `action.PlanningLogger`; `Rename(slug)`
  never replaces an existing directory. A plan that does not reach a spec keeps `.plan-<id>`
  and prints where it is.
- **`story.json`** (`StoryRecord`) holds what plan knows of SPEC §14: `slug`, `spec_dir` (relative
  to the home, slash-separated), `jira_parent` (omitted without `--jira`), `created_at`,
  `services` (name → absolute checkout), `status` and `calls` (usage by `survey-<repo>` and
  `specify`, `null` when the CLI reported none). `LoadStoryRecord` refuses unknown keys and
  trailing data and checks every field, so ST06 extends the shape on purpose. It is written
  only with a spec: the one-task case keeps its `.plan-<id>` (artifacts and logs) without one.
- **An existing `<spec_dir>/<slug>`** is refused by specify's `SpecDirExists` (an invalid answer
  on `slug`), and again by `os.Mkdir` when the spec is written; an existing
  `.orchestrator/stories/<slug>` is refused before writing. A failed write removes only the
  directory this plan created.
- **Files.** `story.RenderSpec(title, SpecText)` writes SPEC.md (the five sections, `—` when
  empty). `story.TaskFileName(id, title)` builds `TASK-<ID>-<slug>.md` in Go: ASCII letters and
  digits, other runs as `-`, at most 40 bytes cut at a `-`, and `task` when nothing is left (a
  title in another script). The one-task command quotes the path and the model's title for the
  shell in Go.
- Tests: `internal/app/story_plan_test.go` (`writeSpec`, a service's config), `args_test.go`,
  `internal/persistence/story_test.go`, `internal/logging/story_test.go`,
  `internal/story/spec_test.go`, the Jira client cases, and `test/integration/story_plan_test.go`
  with every Acceptance item; the spec goldens are in `test/integration/testdata/story_plan/`.
