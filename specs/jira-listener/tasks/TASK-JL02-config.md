---
id: JL02
repo: orche
depends_on: [JL01]
jira: none
---
# JL02 — Config: the listener and gitlab sections, validation, scrubbing

**Size** S

## Goal

SPEC.md §2 in the strict schema and the fail-fast chain. Nothing reads the sections yet.

## Files

- `internal/config`: `Listener` and `GitLab` sections with defaults (`poll_interval: 1m`,
  `services_dir: .`, `max_attempts: 3`, `clone_timeout: 10m`, `query.label: ai-run`,
  `labels.service_prefix: "service:"`, `labels.project_prefix: "gitlab:"`,
  `gitlab.username: oauth2`).
- `Validate`: `poll_interval` 30s..1h, `max_attempts` 1..10, `clone_timeout` 1m..1h; required
  `query.projects`, `query.statuses`, `query.reporters`, `gitlab.allowed_groups` and all five
  `transitions`; every JQL value one printable line without `"` or `\`; `allowed_groups` entries
  are project-path prefixes (SPEC §5's charset, no `.`/`..` segment); `gitlab.base_url` https
  (http only on loopback), no userinfo, query or fragment; a literal token key refused like
  `jira`'s; `listener` without `jira` or without `gitlab` refused; `default_config` a relative or
  absolute path, not checked for existence here.
- `Config.CredentialEnvNames` includes `gitlab.token_env`.
- `ValidateDependencies`: `DependencyOptions{Listener: true}` checks the Jira section and its
  variables (as `Jira: true`), the GitLab token variable and `git`.
- `orchestrator validate` reports the two sections when present.
- Table tests next to the existing config tests.

## Acceptance

- Each validation rule above has a table test case with its error message; an unknown key under `listener` or `gitlab` is a schema error.
- A config without the two sections loads, validates and behaves exactly as before.
- `gitlab.token_env` is in `CredentialEnvNames`, so `execution.ScrubbedEnv` drops it once registered.
- `DependencyOptions{Listener: true}` fails on a missing `GITLAB_TOKEN` naming only the variable, never a value.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira-listener/`, nor MVP.md, CLAUDE.md, TODO.md, README.md,
`orchestrator.example.yaml` (JL06 documents the keys).
