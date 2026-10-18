# ST02 — config: `story`, `jira.create`; finding the services the task names

**Size** S (1.5 d) · **Depends on** ST01 (`story.Named`) · **Blocks** ST03, ST04, ST06, ST08

## Goal

The configuration of `SPEC.md` §5 exists and is strictly validated, and the services a task
names are found on disk as §7.1 says, with no model call.

## Files

- `internal/config/story.go`: `StoryConfig{Roots, MaxServices, SpecDir, MaxTasks, MaxCostUSD,
  JiraIssueTypes}`; defaults (`spec_dir: specs`, `max_services: 5`, `max_tasks: 12`);
  `ResolveRoots(home)` — absolute, `~/`, or relative to the story home, symlinks resolved, each
  an existing directory
- `internal/config/jira.go`: `JiraConfig.Create *JiraCreateConfig{IssueType, Link, Field,
  Labels, Components}`
- `internal/config/validator.go`: `max_services` 1..20; `max_tasks` 2..30; `max_cost_usd` > 0
  and, when set, every provider a story can route to reports cost (as `limits.max_cost_usd`,
  §45.3); `jira.create` without `jira`; `link` `parent` | `field`; `field` required with `field`
  and matching `^customfield_[0-9]+$`
- `internal/app/story_services.go`: `candidates(roots, home)` — `os.ReadDir` of each root, a
  `.git` entry by `os.Lstat`, the name pattern of §7.1; `findServices(request, components,
  --repo, candidates, max)` — `story.Named` or the `--repo` names, then the checks of §7.1
  point 4 (none, too many, ambiguous, `git rev-parse --show-toplevel` only for the named ones),
  each message as in the spec
- `orchestrator.example.yaml`: the commented `story` and `jira.create` blocks

## Acceptance

- Unit: each validation error names the key; root resolution for the three forms; an unknown
  key under `story` or `jira.create` refused by the strict parse; a config without `story` loads
  exactly as before.
- `findServices` on a temporary tree of roots: found by mention; `--repo` overrides detection;
  none → the message with the roots; six named with `max_services: 5` → the list; the same name
  under two roots → both paths; a subdirectory of another repository (not its top level) →
  refused; a root with 200 non-git directories runs no git at all.

## Gates

`scripts/check.sh`

## As built

2026-10-18. Built; `scripts/check.sh` (root and `bench/`) has not been run - that gate is left to
the operator, and this task is not closed until it passes.

- ST01 was not yet in the repository, so `internal/story/services.go` holds `Named` alone (with
  its test); the rest of ST01 is unchanged.
- `StoryConfig` is `Config.Story *StoryConfig` (nil without the section); `ResolveRoots(home)` is
  a method on it, `~/` against the user's home directory.
- `jira.create` sits inside `jira`, so "`jira.create` without `jira`" cannot be written; a
  `create` block with the rest of the section missing is reported by `jira.base_url is
  required` and the other required keys. `link` defaults to `parent`; `field` with `link: parent`
  is refused; labels may not contain whitespace.
- `story.max_cost_usd` needing cost-reporting providers is checked with the other budget checks in
  `ValidateDependencies` (`storyBudgetProblems`, in `story.go`), because only there is the provider
  registry known. It covers `actions.analyze` (survey) and `actions.plan` (specify); the children
  use their own repositories' configs.
- `candidates(roots, home)` takes the resolved roots; `findServices` additionally takes the
  context, a `execution.ProcessExecutor` (the only place git runs, for the named services) and
  the roots for the messages. The story home is a candidate once, by resolved path, even when it
  is under a root.
- Not done: the commented `story` / `jira.create` blocks of `orchestrator.example.yaml` are
  added, but nothing yet calls `findServices` (ST03, ST04, ST06).
