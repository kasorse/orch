# GitLab CI example

`gitlab-ci.yml` has two jobs (specs/delivery/SPEC.md §11.4):

- `ai-implement`: manual, or when the pipeline variable `JIRA_KEY` is set. Runs
  `orchestrator run --jira "$JIRA_KEY" --mode auto --output json`, which pushes a branch and opens
  the merge request. `result.json` and `.orchestrator/runs/` are the artifacts.
- `ai-review`: on a merge request pipeline. Runs `orchestrator review --base
  "$CI_MERGE_REQUEST_DIFF_BASE_SHA" --head "$CI_COMMIT_SHA"`, then the job posts `report.md` as a
  note with `curl`. The orchestrator itself does not talk to the notes API.

## The image

No image is published. Build it from the repository root and push it to your registry:

```bash
docker build -t registry.example.com/team/orchestrator:latest .
docker build --build-arg CLAUDE_CODE_VERSION=2.1.290 .   # another claude CLI inside the verified range
```

Set `image:` in the example to it. The entrypoint is `orchestrator`; the user is not root.

## Variables

Set them in Settings → CI/CD → Variables, masked, and protected where the branch is.

| variable | used by | notes |
|---|---|---|
| `ANTHROPIC_API_KEY` | both | the only credential in CI (`--claude-auth api`) |
| `GITLAB_TOKEN` | both | a project access token: `api` (merge request and notes) and `write_repository` (push); role Developer or higher. Read by the git credential helper and by `curl`, never written into a remote URL |
| `JIRA_KEY` | `ai-implement` | the ticket key or URL; set on a manual run or by a trigger |
| `JIRA_EMAIL`, `JIRA_API_TOKEN` | `ai-implement` | the variables `jira.email_env` and `jira.token_env` name in `orchestrator.yaml` (Jira Cloud basic auth; a Data Center PAT needs only the token) |

`orchestrator.yaml` in the repository must set `delivery.target_branch` (for example `main`):
GitLab checks out a detached HEAD, which names no branch for the merge request to target. The job
stops with exit 2 when it is missing. Never set `test.sandbox: none` in CI.

## The runner

The tests and the review run under bubblewrap, which needs unprivileged user namespaces inside the
container. The sandbox probe at the start of a run refuses with a message when they are missing.

- Docker's default seccomp profile blocks them: run the job container with a profile that allows
  `clone`/`unshare` with `CLONE_NEWUSER` (for example `--security-opt seccomp=<profile.json>`; in
  the runner's `config.toml`, `security_opt = ["seccomp=/path/profile.json"]`).
- On Ubuntu 23.10+ hosts AppArmor restricts them: set
  `sysctl kernel.apparmor_restrict_unprivileged_userns=0` on the host, or give the container an
  AppArmor profile that allows `userns`.
- The host needs `kernel.unprivileged_userns_clone=1` (Debian) and a non-zero
  `user.max_user_namespaces`.

## Exit codes in a job

`0` the job passes. `6` means the run completed but needs a person: `ai-implement` ended without a
merge request (the commit was not made, or the push or merge request failed; `mr_skipped` and
`mr_error` in `result.json` say why), or `ai-review` gave a failing verdict. Both fail the job; the
`ai-implement` job may be allowed to fail with `allow_failure: { exit_codes: 6 }`. Other codes:
`2` configuration, credentials or usage; `3` a provider failed; `4` the workflow failed or a limit
stopped it; `5` cancelled.

## Left to the operator

`docker build .`, `docker run … orchestrator validate` and the job on a real runner are checked by
hand; see the task's As built.
