# JL02 — Config: `listener` and `gitlab`, validation, scrubbing

**Size** S

## Goal

SPEC.md §2 in the strict schema and the fail-fast chain.

## Files

- `internal/config`: `Listener` and `GitLab` sections with defaults (`poll_interval: 1m`,
  `services_dir: .`, `max_attempts: 3`, `clone_timeout: 10m`, `query.label: ai-run`, the label
  prefixes, `gitlab.username: oauth2`); `Validate`: ranges, required `query.projects`,
  `query.statuses`, `query.reporters`, `gitlab.allowed_groups`, the five transitions; JQL values
  one printable line without `"` or `\`; `gitlab.base_url` https (http on loopback), no userinfo;
  a literal token refused; `listener` without `jira` or `gitlab` refused.
- `Config.CredentialEnvNames` includes `gitlab.token_env`.
- `ValidateDependencies`: a `DependencyOptions{Listener: true}` that checks the GitLab variable
  and `git`, plus the Jira checks.
- `orchestrator.example.yaml`: the two sections, commented. MVP.md §5/§6: the keys.

## Acceptance

- Each rule has a table test; an unknown key under either section is an error (strict schema).
- `orchestrator validate` reports the sections when present.
- `scripts/check.sh` passes.
