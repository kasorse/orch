---
id: RV01
depends_on: []
---
# RV01 — `internal/gitlab`: the read-only client, `gitlab.api_token_env`, the scope check

**Size** M

## Goal

SPEC §2, §3: the config key, the dependency check, the client with its three GETs and the scope
check. Nothing calls it yet (RV03).

## Files

- `internal/config/listener.go`: `GitLabConfig.APITokenEnv`, strict schema, a literal
  `api_token` rejected, must differ from `token_env`; registered in `CredentialEnvNames`;
  `DependencyOptions{GitLab: true}` checks the section and the variable.
- `internal/gitlab/` (new): `NewHTTPClient(Options{BaseURL, AllowedGroups, Timeout}, token)`,
  `CheckReadOnly`, `MergeRequest(project, iid)`, `LatestMergeRequest(project, branch)`; the
  domain type `domain.MergeRequest`; `apperr.GitLabError`; redaction; no redirect; strict decode.
- Tests: an `httptest` server per call and per error kind; a boundary test (imports: `domain`,
  `apperr`, stdlib); a test that no request method but GET is ever built; a scope table
  (`read_api` ok, `api` refused, empty refused, revoked refused).
- Docs: MVP.md §54.1–§54.2 (config, client), §30 (a read-only GitLab API client exists, for
  `revise` only); CLAUDE.md Architecture and Out of scope lines; `orchestrator.example.yaml`.

## Acceptance

- `gitlab.api_token_env` absent: every existing test passes unchanged.
- A token with any scope outside `read_api`, `read_repository`, `read_user` is a config error that names the scopes and not the token.
- The token appears in no error text, log or argv (a test with a token-shaped value assembled at run time).
- A project outside `allowed_groups` is refused before any request.
- `scripts/check.sh` passes.

## As built (2026-11-11)

- `internal/gitlab/client.go`: `NewHTTPClient`, `NewToken` (prints `[REDACTED]`), `CheckReadOnly`,
  `MergeRequest`, `LatestMergeRequest`, and the pure `Allowed`, `ParseMergeRequestURL`,
  `MergeRequestURL`. No `Options.Timeout`/retry keys in the config: fixed 30 s and two retries
  (`DefaultTimeout`, `DefaultRetries`), as nothing asked for them.
- `domain.MergeRequest` (`internal/domain/mergerequest.go`) and `apperr.GitLabError` with its kinds;
  `exitcode` maps auth, not found, redirect and scope to 2, cancelled to 5, the rest to 3.
- The section now needs `token_env` **or** `api_token_env` (SPEC §2 did not say): an operator who
  only runs `revise` has no git token to name. A listener without `token_env` is refused instead.
- Retries cover every transient kind (timeout, connection, 429, 5xx), not only 429/503 with
  `Retry-After` as SPEC §3 said: every request is an idempotent GET.
- MVP.md §54 starts here with §54.2 and §54.3; §30 and CLAUDE.md name the read-only client.
