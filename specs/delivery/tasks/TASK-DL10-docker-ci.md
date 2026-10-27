---
id: DL10
repo: orche
depends_on: [DL07, DL08]
jira: none
---
# DL10 — Dockerfile and the CI example

**Size** S

## Goal

A team can build an image and copy a working GitLab CI job (`SPEC.md` §11.4).

## Files

- `Dockerfile`: build stage, runtime with git, bubblewrap, ca-certificates, Node.js, the claude
  CLI at `ARG CLAUDE_CODE_VERSION` (default: the newest in the adapter's verified range — a test
  reads both and fails when they drift), non-root user, `ENTRYPOINT ["orchestrator"]`.
- `.dockerignore`.
- `examples/ci/gitlab-ci.yml`: `ai-implement` and `ai-review` as in §11.4 (the note posted by the job with `curl`); the credential helper
  reading `$GITLAB_TOKEN`; `delivery.target_branch` for the detached checkout; artifacts.
- `examples/ci/README.md`: variables (`ANTHROPIC_API_KEY`, `GITLAB_TOKEN` scopes, Jira ones), the
  runner settings for bwrap (user namespaces, seccomp, AppArmor), what exit 6 means for the job.

## Acceptance

- The version-drift test passes; `examples/ci/gitlab-ci.yml` parses as YAML in a test.
- `docker build .` and `docker run … orchestrator validate` inside a runner configured as the README
  says: the operator's gate (no docker under the test step's sandbox), recorded in As built.

## Gates

`scripts/check.sh`; `docker build` and the CI job on the gitlab.com test project (operator).

## Notes

Do not edit anything under `specs/delivery/` (story run writes its README and As built). Leave
MVP.md, CLAUDE.md, TODO.md and README.md to DL12. `docker build` and the CI job are the
operator's gate: say in the summary that they were left to the operator, and do not claim they
were done.

## As built (2026-10-27)

Run `f35599b1-d415-4f9c-b23d-79f535233896` in orche: completed, 0 fix iterations, tests passed, no review.
Branch `story/delivery/DL10.4` on `story/delivery/spec (DL07 + DL08)` @ edaebab, commit dca9219.
Cost $0.34. Implementation summary: Add Dockerfile, GitLab CI example and version-drift test.
Conflicts with spec/delivery: none.
