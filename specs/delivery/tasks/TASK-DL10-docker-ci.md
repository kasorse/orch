# DL10 — Dockerfile and the CI example

**Size** S (1 d) · **Depends on** DL07, DL09 · **Blocks** DL12

## Goal

A team can build an image and copy a working GitLab CI job (`SPEC.md` §11.4).

## Files

- `Dockerfile`: build stage, runtime with git, bubblewrap, ca-certificates, Node.js, the claude
  CLI at `ARG CLAUDE_CODE_VERSION` (default: the newest in the adapter's verified range — a test
  reads both and fails when they drift), non-root user, `ENTRYPOINT ["orchestrator"]`.
- `.dockerignore`.
- `examples/ci/.gitlab-ci.yml`: `ai-implement` and `ai-review` as in §11.4; the credential helper
  reading `$GITLAB_TOKEN`; artifacts.
- `examples/ci/README.md`: variables (`ANTHROPIC_API_KEY`, `GITLAB_TOKEN` scopes, Jira ones), the
  runner settings for bwrap (user namespaces, seccomp, AppArmor), what exit 6 means for the job.

## Acceptance

- The version-drift test passes; `examples/ci/.gitlab-ci.yml` parses as YAML in a test.
- `docker build .` and `docker run … orchestrator validate` inside a runner configured as the README
  says: the operator's gate (no docker under the test step's sandbox), recorded in As built.

## Gates

`scripts/check.sh`; `docker build` and the CI job on the gitlab.com test project (operator).
