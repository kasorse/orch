# ST02 — config: `story`, `jira.create`; repository resolution

**Size** S (1 d) · **Depends on** — · **Blocks** ST03, ST06, ST08

## Goal

The configuration of `SPEC.md` §5 exists, is strictly validated, and resolves repository names
to checkouts.

## Files

- `internal/config/story.go`: `StoryConfig{Repos map[string]StoryRepo{Path, Base}, SpecDir,
  MaxTasks, MaxCostUSD, JiraIssueTypes}`; defaults (`spec_dir: specs`, `max_tasks: 12`);
  `ResolveRepo(home, name) (abs string, err)` — absolute, `~/`, or relative to the story home,
  symlinks resolved
- `internal/config/jira.go`: `JiraConfig.Create *JiraCreateConfig{IssueType, Link, Field,
  Labels, Components}`
- `internal/config/validator.go`: repository names `^[a-z][a-z0-9-]{0,30}$`; `max_tasks`
  2..30; `max_cost_usd` > 0 and, when set, every provider a story can route to reports cost
  (as `limits.max_cost_usd`, §45.3); `jira.create` without `jira`; `link` `parent` | `field`;
  `field` required with `field` and matching `^customfield_[0-9]+$`; `components` keys are
  repository names
- `orchestrator.example.yaml`: the commented `story` and `jira.create` blocks

## Acceptance

- Unit: each validation error names the key; `ResolveRepo` for the three path forms; an unknown
  key under `story` or `jira.create` refused by the strict parse; a config without `story` loads
  exactly as before.

## Gates

`scripts/check.sh`
