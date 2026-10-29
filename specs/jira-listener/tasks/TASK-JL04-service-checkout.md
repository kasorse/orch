# JL04 — Service, project, clone, the git credential helper, fresh base

**Size** M

## Goal

SPEC.md §5 and §6: from a ticket's labels and components to a clean, fresh checkout of the right
repository, and a git credential that never leaves git.

## Files

- `internal/listener` (new, pure; `imports_test.go`): `Service(labels, components, dirs []string,
  prefix) (string, error)`, `Project(labels, prefix) (string, error)`, `Allowed(path, groups)`,
  `SameProject(originURL, baseURL, path) bool` (normalised).
- `internal/repository`: `Clone(ctx, url, dir)` (`--no-recurse-submodules`, `--` before the URL),
  `DefaultBranch` (`origin/HEAD`), `FetchFastForward(ctx, branch)`; argv only.
- `internal/app/gitcredential.go`: the hidden `orchestrator git-credential get`: reads git's
  request on stdin, answers `username`/`password` from `gitlab.token_env` only for the exact
  protocol and host of `gitlab.base_url`, nothing otherwise; `listenerGitEnv(exe, base)` builds the
  `GIT_CONFIG_COUNT/KEY_n/VALUE_n` entries.
- `internal/app/listen_checkout.go`: clone into `.clone-<service>-<rand>` then rename; cleanup of
  stale `.clone-*`; the fingerprint, clean and on-default checks, then fast-forward.

## Acceptance

- `Service`: one label; one matching component; label + same component; two names → error naming
  both; none → error; an invalid name → error.
- `Allowed`: `backend` allows `backend/x`, not `backend-old/x` nor `backend`.
- A clone from a local bare repository through a fake `https` remote is not needed: the helper is
  tested by feeding git's protocol on stdin; the clone path by a `file://` remote with the helper
  env present and `.git/config` holding no token afterwards.
- A dirty checkout, one on another branch, one that diverged: each a distinct error.
- `scripts/check.sh` passes.
