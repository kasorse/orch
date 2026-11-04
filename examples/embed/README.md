# The Jira listener as a service on the platform

`main.go` here is the smallest host: `orchestrator.Embedded`, a `signal.NotifyContext`,
`orchestrator.Listen`, standard library only. A corporate service is the same program built on the
platform library. This file lists what the service's repository holds and what its host must
provide (design record: `specs/embed/`; summary: MVP.md §53).

Everything corporate below is a placeholder: `registry.example.com`, `platform/ci-templates`,
`group/listener`, `jira.example.com`, `gitlab.example.com`, `gitlab-group`. The real ones live in
the service's repository.

The provider access is API keys only (`transport: api`, MVP.md §52): no provider CLI is in the
image. The module is private until the owner makes it public, and `LICENSE` is PolyForm
Noncommercial: a company's service needs the owner's written grant (SPEC §3).

## The service repository

```
main.go                       # the platform's bootstrap + orchestrator.Embedded + Listen
go.mod                        # require github.com/kasorse/orch
Dockerfile
.gitlab-ci.yml
deploy/values.yaml            # the deployment values
config/listener.yaml          # copied into the image at /etc/orchestrator/listener.yaml
config/default.yaml           # copied into the image at /etc/orchestrator/default.yaml
config/org.yaml               # copied into the image at /etc/orchestrator/org.yaml (the corporate policy)
```

### `main.go`

```go
package main

import (
	"os"

	orchestrator "github.com/kasorse/orch"
	"gitlab.example.com/platform/bootstrap"
)

func main() {
	// First: git's credential helper and the MCP verify server are this binary.
	if code, ok := orchestrator.Embedded(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}

	app := bootstrap.New("jira-listener")
	opts := orchestrator.Options{
		Dir:    "/data", // the persistent volume
		Config: "/etc/orchestrator/listener.yaml",
	}
	app.AddRunnerFunc(func() error { return orchestrator.Listen(app.Context(), opts) })
	app.Run()
}
```

The platform gives the health endpoints, the logger, metrics and the shutdown order. `Listen`
returns nil when the context ends and an error when it must stop (a configuration or credential
stop, exit 2 on the command line); the runner stops the service on a non-nil error.

### `Dockerfile`

The base is the Go build image, not the runtime image: the runtime image has git and make but
neither Go nor `bwrap`, and a run compiles and tests the service it changes. Add `bubblewrap`
and the toolchains of the services' test commands.

```dockerfile
FROM registry.example.com/platform/golang-build:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/jira-listener .

FROM registry.example.com/platform/golang-build:1.26
RUN apt-get update \
 && apt-get install -y --no-install-recommends bubblewrap git \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/jira-listener /usr/local/bin/jira-listener
COPY config/listener.yaml config/default.yaml /etc/orchestrator/
COPY config/org.yaml /etc/orchestrator/org.yaml
RUN useradd --create-home --uid 10001 listener && mkdir /data && chown listener /data
USER listener
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/jira-listener"]
```

### `.gitlab-ci.yml`

The platform's shared jobs build, scan and publish the image; the service adds nothing of its own.

```yaml
include:
  - project: platform/ci-templates
    ref: main
    file:
      - /go/build.yml
      - /docker/build-and-push.yml
      - /deploy/helm.yml

variables:
  IMAGE: registry.example.com/group/listener
```

### The deployment values

```yaml
replicaCount: 1                  # one: a second listener on the volume is refused, on another
                                 # volume it claims the same tickets
strategy:
  type: Recreate                 # never two pods on the volume at once

secretKeys:                      # the variables the configuration names, from the secret store
  - JIRA_EMAIL
  - JIRA_API_TOKEN
  - GITLAB_TOKEN
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY

persistence:
  enabled: true
  mountPath: /data               # Options.Dir: state, lock, task files, clones, worktrees
  size: 20Gi

resources:                       # a compile and a test run of the services it changes
  requests: {cpu: "2", memory: 4Gi}
  limits: {cpu: "4", memory: 8Gi}

terminationGracePeriodSeconds: 1200   # above execution.timeout (15m): a step ends after the cancel

securityContext:                 # bwrap needs unprivileged user namespaces
  allowPrivilegeEscalation: false
```

`bwrap` needs user namespaces inside the container: the runner's seccomp and AppArmor settings are
in `examples/ci/README.md`, "The runner". If the cluster cannot give them, every service's
configuration must say `test.sandbox: none` (the tests then reach the network and the volume).

### `config/listener.yaml`

The listener's own file: Jira, the listener, GitLab, and the providers on `transport: api`.

```yaml
version: 2

jira:
  base_url: https://jira.example.com
  auth: basic
  email_env: JIRA_EMAIL
  token_env: JIRA_API_TOKEN

listener:
  poll_interval: 1m
  services_dir: services
  default_config: /etc/orchestrator/default.yaml
  max_attempts: 3
  clone_timeout: 10m
  query:
    projects: [PAY]
    label: ai-run
    statuses: ["To Do"]
    reporters: ["5b10a2844c20165700ede21g"]
  labels:
    service_prefix: "service:"
    project_prefix: "gitlab:"
  transitions:
    start: In Progress
    blocked: Blocked
    failed: Failed
    done: Ready to Review
    retry: To Do

gitlab:
  base_url: https://gitlab.example.com
  token_env: GITLAB_TOKEN
  username: oauth2
  allowed_groups: [gitlab-group]

providers:
  claude:
    auth: api
    api_key_env: ANTHROPIC_API_KEY
    transport: api
    base_url: https://ai-gateway.example.com
  codex:
    auth: api
    api_key_env: OPENAI_API_KEY
    transport: api
```

### `config/default.yaml`

`listener.default_config`: what a run uses for a service that has no `orchestrator.yaml` of its
own. Start from `orchestrator.example.yaml`; the routes name full model IDs (`transport: api`
refuses aliases) and no provider CLI. Only the parts that matter here:

```yaml
version: 2

decision:
  provider: claude
  model: claude-sonnet-5-5
  timeout: 60s
  on_failure: fail

clarify:
  enabled: true
  provider: claude
  model: claude-sonnet-5-5
  timeout: 120s

actions:
  research:
    provider: claude
    model: claude-sonnet-5-5
  investigate:
    provider: claude
    model: claude-sonnet-5-5
  implement:
    provider: claude
    model: claude-opus-5-5
  review:
    provider: claude
    model: claude-sonnet-5-5

providers:
  claude:
    auth: api
    api_key_env: ANTHROPIC_API_KEY
    transport: api
    base_url: https://ai-gateway.example.com

test:
  command: "go test ./..."
  sandbox: bwrap

execution:
  timeout: 15m
```

### `/etc/orchestrator/org.yaml`

The corporate policy, in the image, outside every repository (MVP.md §45.3). A repository's file
can only tighten it. It has its own strict schema.

```yaml
version: 1

security:
  require_clean_worktree: true
  allow_unverified_cli: false
  secret_scan:
    mode: stop
    allow_paths: []

test:
  sandbox: bwrap

limits:
  max_cost_usd: 5.00

allowed_models: [claude-sonnet-5-5, claude-opus-5-5, claude-haiku-5-5]
```

The org layer has no `transport` floor: a service's `orchestrator.yaml` that routes to a CLI fails
the run's fail-fast chain in an image without that CLI (exit 2, the ticket `failed`).

## The host's duties

A checklist for whoever deploys it (the package documentation, `go doc github.com/kasorse/orch`,
says the same):

- [ ] The process is the listener's: `Listen` changes the process environment (the git credential
      helper's entries are set; the Jira variables and the parent-session variables are removed once
      the Jira client is built) and holds a flock in `Dir`. One `Listen` per process, one process
      per `Dir`. The service reads the Jira variables nowhere else.
- [ ] `orchestrator.Embedded` is called first in `main`, so `<exe> git-credential` and
      `<exe> mcp-verify` work: `os.Executable()` is the service. The service's own first argument is
      none of `run`, `resume`, `review`, `validate`, `version`, `story`, `decisions`, `runs`,
      `jira`, `mcp-verify`, `git-credential`, `help`.
- [ ] `Dir` is a volume: the state, the lock, the task files, the clones and their worktrees
      survive a restart; that is how a ticket interrupted by a stop is resumed.
- [ ] `git` is on `PATH`.
- [ ] `bwrap` is on `PATH` and works in the container, unless every service's configuration says
      `test.sandbox: none`.
- [ ] The toolchains of the services' test commands are in the image (Go here; add Node, Python, …
      for services that need them).
- [ ] No provider CLI is needed: every provider is on `transport: api` (O5).
- [ ] Every environment variable the configuration names is set from the secret store:
      `jira.email_env`, `jira.token_env`, `gitlab.token_env`, `providers.*.api_key_env`.
- [ ] One replica, strategy `Recreate`: a second listener on the same volume is refused, two on
      different volumes claim the same tickets.
- [ ] The stop timeout is above `execution.timeout`: on SIGTERM the listener cancels the run in
      progress and waits for the call to return.
- [ ] The Jira workflow allows `start` from the status in `query.statuses` (it is the only lock),
      and `start`, `blocked`, `failed`, `done` and `retry` exist as transitions.
- [ ] `gitlab.allowed_groups` names only groups whose members may run code on this machine: a cloned
      repository's `orchestrator.yaml` and `test.command` are trusted.

## Left to the operator

The image build, the pipeline and one ticket end to end on the dev environment are checked by hand
(SPEC §7, last item); the result is recorded in `specs/embed/README.md`, `Status:`.
