#!/bin/sh
# Started by launchd (deploy/com.orch.slackbridge.plist): tokens come from the login Keychain,
# never from files. Exits non-zero when a token is missing, so launchd retries later.
set -eu
cd "$(dirname "$0")"
SLACK_BOT_TOKEN="$(security find-generic-password -a "$USER" -s slack-orch-bot -w)"
SLACK_APP_TOKEN="$(security find-generic-password -a "$USER" -s slack-orch-app -w)"
export SLACK_BOT_TOKEN SLACK_APP_TOKEN
exec ./slackbridge -config slackbridge.yaml
