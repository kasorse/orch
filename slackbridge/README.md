# slackbridge

Mention the bot in Slack (or DM it) with a task. It runs the orchestrator, then answers in the
thread with the result and the merge request link (specs/slack-gitlab, T4 and T5).

```
@orchestrator web: fix the date filter on the orders page
```

It is a separate Go module, like `bench/`: it starts `orchestrator` as a program and reads only
its `--output json` line (MVP.md §50.10). Nothing in the core imports it.

## How a message is handled

1. **Author:** must be in `allowed_users`, otherwise the bot refuses.
2. **Project:** the `name:` prefix, else the channel's project (`channels`), else `default_project`.
3. **Queue:** one run per project at a time. The others wait, and the thread says how many are ahead.
4. **Run:**
   - `runner: local` runs `orchestrator --mode auto --output json run "<task>"` in `local.dir`.
     The task is one argv element and never reaches a shell. `env_command` is evaluated first,
     for example to get an AI gateway key.
   - `runner: gitlab` triggers the project's pipeline with `ORCH_TASK`, `ORCH_SLACK_CHANNEL` and
     `ORCH_SLACK_TS`, then polls it.
5. **Reply:** status, workflow, MR link (or why there is none), cost and run id. On a failure
   it adds the tail of the log.

## Jira tickets

A ticket can be anywhere in the message: `PROJ-15230`, its `https://…/browse/PROJ-15230` URL, or
just `15230` at the start (with `jira.default_project_key: PROJ`). Everything else in the message
goes to the orchestrator with the ticket as extra instructions and context:

```
15230 the item endpoint returns {id, location, items[]}; reuse the table from the Orders page
```

The project, in order: a `<project>:` prefix, the channel's project, then the ticket. The bridge
reads the ticket's components, labels and `[tags]` in its summary and picks the project whose
`jira_match` lists one of them, then `default_project`. It posts what it chose (`PROJ-15230 … →
web`). The orchestrator reads the full ticket itself (`run --jira`), so its config
needs a `jira:` section, and `JIRA_EMAIL`/`JIRA_API_TOKEN` must be in the run's environment.

## API spec

With `api_spec` on a local project the bridge downloads the OpenAPI document into `path` (in the
repository, git-ignored) when it is older than `max_age`, before each task. Then it looks for
endpoints the task or its ticket names (`shop/orders/item/query`, `{{…}}`, `/api/v1/…`) and
adds their request and response types to the task. The decision and clarify steps cannot read
files, so this is how they see the contract. The steps that read code can also open the full
document at `path`.

## Approve before run (T6)

Set `approve: true` on a `local` project. The bridge first runs
`orchestrator --mode plan run "<task>"`, posts the chosen workflow with **Run** and **Cancel**
buttons, and on **Run** queues `orchestrator --mode auto resume <run-id>`, so the approved plan is
the one that runs. Any user in `allowed_users` may click; a second click is refused. Pending
plans live in memory: after a restart, start the task again. The app needs interactivity on
(the manifest has it).

## Setup

1. Go to api.slack.com/apps, then **Create New App → From a manifest**, and paste
   `slack-app-manifest.yaml`.
2. Under **Basic Information → App-Level Tokens**, create a token with `connections:write`.
   That gives `SLACK_APP_TOKEN` (`xapp-…`).
3. Click **Install to Workspace**. That gives `SLACK_BOT_TOKEN` (`xoxb-…`).
4. Copy `slackbridge.example.yaml` to `slackbridge.yaml` and fill in your user ID and the projects.
5. Run it:

```sh
cd slackbridge
go build -o slackbridge .
export SLACK_BOT_TOKEN=xoxb-… SLACK_APP_TOKEN=xapp-…
./slackbridge -config slackbridge.yaml -check   # config + Slack auth, then exit
./slackbridge -config slackbridge.yaml
```

Socket Mode needs no public URL, so the bridge runs on a laptop or on any VM.

## Run as a macOS service

`run-service.sh` reads both Slack tokens from the login Keychain and starts the bridge;
`deploy/com.orch.slackbridge.plist` is a LaunchAgent for it: it starts at login and is restarted
when it exits (30 s apart). Install once:

```sh
security add-generic-password -U -a "$USER" -s slack-orch-bot -w "xoxb-…"
security add-generic-password -U -a "$USER" -s slack-orch-app -w "xapp-…"
cd slackbridge && go build -o slackbridge .
sed "s#__HOME__#$HOME#g" deploy/com.orch.slackbridge.plist > ~/Library/LaunchAgents/com.orch.slackbridge.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.orch.slackbridge.plist
```

| | |
|---|---|
| log | `tail -f ~/Library/Logs/slackbridge/slackbridge.log` |
| status | `launchctl print gui/$(id -u)/com.orch.slackbridge \| grep state` |
| restart (after a rebuild or a config change) | `launchctl kickstart -k gui/$(id -u)/com.orch.slackbridge` |
| stop | `launchctl bootout gui/$(id -u)/com.orch.slackbridge` |

A restart drops plans waiting for Run and questions waiting for a reply: they live in memory.

## Security

- **Slack text is untrusted task text:** it gets no more authority than a typed task (MVP.md §38).
- **Only `allowed_users`** can start a run.
- **Tokens:** read from the environment only. The config file holds variable names.
- **Changes reach a branch only through the orchestrator's own merge request** (§50). Merging stays with a person.

## Tests

```sh
cd slackbridge && go test -race ./...
```

They cover message parsing, config validation, the argv of a local run, `env_command`, a fake
orchestrator, a fake GitLab, the allowlist, de-duplication, the per-project queue and approve before run.
