---
id: JL04
repo: orche
depends_on: [JL03]
jira: none
---
# JL04 — Service, project, clone, the git credential helper, a fresh base

**Size** M

## Goal

SPEC.md §5 and §6: from a ticket's labels and components to a clean, fresh checkout of the right
repository, and a GitLab credential that only git ever sees.

## Files

- `internal/listener` (new, pure, with an `imports_test.go` allowing only `domain` and the
  standard library): `Service(labels, components, dirs []string, prefix string) (string, error)`,
  `Project(labels []string, prefix string) (string, error)`, `Allowed(path string, groups
  []string) bool` (whole segments), `SameProject(originURL, baseURL, path string) bool`
  (normalised scheme, host, default port, path, optional `.git`).
- `internal/repository`: `Clone(ctx, url, dir string, env []string)` (`git clone
  --no-recurse-submodules -- <url> <dir>`), `DefaultBranch` (from `origin/HEAD`),
  `FetchFastForward(ctx, branch)`; argv only.
- `internal/app/gitcredential.go`: the hidden `orchestrator git-credential get`: reads git's
  credential request on stdin, answers `username` and `password` (from `gitlab.token_env`) only
  for the exact protocol, host and port of `gitlab.base_url`, prints nothing otherwise; and
  `listenerGitEnv(exe, baseURL string) []string`, the `GIT_CONFIG_COUNT/KEY_0/VALUE_0` entries of
  SPEC §6.2.
- `internal/app/listen_checkout.go`: resolve the directory (SPEC §5), clone into
  `.clone-<service>-<rand>` then `os.Rename`, remove stale `.clone-*`, then the git fingerprint,
  the clean and on-default checks and the fast-forward (SPEC §6.3); each failure a distinct error
  kind the loop (JL05) maps to SPEC §8.

## Acceptance

- `Service`: one label; one matching component; a label and the same component; two names gives an error naming both; none gives an error; an invalid name gives an error.
- `Allowed`: `backend` allows `backend/x` and `backend/x/y`, not `backend-old/x` nor `backend` itself.
- `SameProject` treats `https://h/g/p`, `https://h:443/g/p.git` as one project, and another host, port or path as different.
- `git-credential get` answers for the configured host and is silent for another host or protocol (tested by feeding git's protocol on stdin).
- A clone from a `file://` bare repository with the helper environment set leaves no token in `.git/config`; a cut clone leaves no `<service>` directory.
- A dirty checkout, one on another branch and one that diverged from origin give three distinct errors; a behind checkout is fast-forwarded.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/jira-listener/`, nor MVP.md, CLAUDE.md, TODO.md, README.md
(JL06 documents §50.13's change).

## As built (2026-10-29)

Run `3b3001ed-2068-4e11-b487-665ab316098e` in orche: completed, 0 fix iterations, tests passed, review pass.
Branch `story/jira-listener/JL04` on `story/jira-listener/JL03` @ e0ea6a3, commit 461cbb8.
Cost $3.24. Implementation summary: Add listener service/project resolution, clone and git credential helper.
Conflicts with spec/jira-listener: none.
