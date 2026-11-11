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
