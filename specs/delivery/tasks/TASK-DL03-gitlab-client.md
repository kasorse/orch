# DL03 — `gitlab` config, the project from the remote, `internal/gitlab`

**Size** M (3 d) · **Depends on** — · **Blocks** DL04, DL09

## Goal

The configuration, the remote → project rule and a strict HTTP client for the five calls of
`SPEC.md` §6.

## Files

- `internal/config/gitlab.go`: `GitLabConfig` (`base_url`, `token_env`, `remote` default `origin`,
  `target_branch`, `draft` default true, `labels`, `timeout` default 30s, `max_retries` default 2);
  the literal `token` key declared and refused by name; `base_url` scheme rule shared with
  `jira.base_url`; `token_env` into `CredentialEnvNames`; `DependencyOptions.GitLab` checks the
  section and that the variable is set.
- `internal/gitlab/remote.go`: `ParseRemote(url, baseURL string) (project string, err error)` — the
  table of §6.2: scp-like ssh, `ssh://` with a port, https with userinfo and a path prefix; host
  match; `.git` strip; the path pattern; userinfo never in an error.
- `internal/gitlab/client.go`: `New(baseURL, token string, timeout, retries)`; `Project`,
  `OpenMR`, `CreateMR`, `MR`, `CreateNote`; unexported wire types, `PRIVATE-TOKEN`, no redirect
  (`CheckRedirect` returns an error), 1 MiB body limit, strict answers, the token redacted, retry
  only 429/503 with `Retry-After`; `MayExist(err)` as `jira.MayExist`.
- `internal/apperr`: `GitLabError{Op, Status, NotSent, …}` (or a `Kind` on an existing type,
  whichever `jira` uses) and its exit code 2 for fail-fast, 6 at delivery (DL04 maps).
- `internal/app/validate.go`: `validate` checks the section, the remote and `GET project` when the
  section is present (`--mr` makes it required, DL04).
- Boundary test: `internal/gitlab` imports only `domain`, `apperr` and the standard library.

## Acceptance

- `ParseRemote` table, including `insteadOf`-rewritten values given as input, IPv6 hosts, upper
  case hosts, a `base_url` with a path, `file://` and a local path refused.
- httptest server: each call's request (method, path with the encoded project, header, body);
  each answer's validation failures named by JSON path; a 302 not followed; a 429 retried once; a
  timeout after sending → `MayExist` true; 401/403/404 → named errors without the token.
- Config: strict keys; literal `token` refused naming the key; defaults applied.

## Gates

`scripts/check.sh`
