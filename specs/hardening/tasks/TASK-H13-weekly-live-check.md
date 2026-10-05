# H13 — Weekly live permission check in CI (optional)

**Size** S (0.5 d) · **TODO** S3 · **Depends on** H06

## Goal

Learn that a new claude CLI release broke an authority before anyone widens the verified
range (`SPEC.md` §7.4). Optional: it spends real API calls and needs a key in CI.

## Prerequisites (owner)

- An Anthropic API key with a small spend limit, stored as the GitHub Actions secret
  `ANTHROPIC_API_KEY` in `kasorse/orch`.
- Agreement on the weekly cost (the suite is a handful of short calls).

## Steps

1. `.github/workflows/security-live.yml`:
   - `on: schedule: - cron: "0 6 * * 1"` and `workflow_dispatch`;
   - `ubuntu-latest`; install Go (version from `go.mod`), `bubblewrap`, the latest claude CLI
     (official install script / npm package — pin the method in a comment);
   - Ubuntu 24.04 AppArmor profile for bwrap (as in README);
   - `claude --version` printed;
   - `go test -tags security ./test/security/... -v` with `ANTHROPIC_API_KEY` from secrets.
2. On failure: `gh issue create` (or `actions/github-script`) titled
   `Live permission check failed on claude <version>` with the job link. Do not open a
   duplicate if one is open with the same version.
3. Document in `specs/permissions/DECISIONS.md` and README: the job exists, what a failure
   means (do not extend the range; fix the adapter or keep the range), how to run it by hand.

## Acceptance

A manual `workflow_dispatch` run is green on the current CLI; a deliberately broken run (e.g.
an env flag that makes one test fail) opens one issue.
