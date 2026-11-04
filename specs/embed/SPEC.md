# The listener as a library: the run in process, a public package, a service on the platform

Status: **implemented** (2026-11-04: EB01–EB04 done by `story run`, landed on `spec/embed`; `scripts/check.sh` and the `bench/` tests ran outside the sandbox the same day, 635 bench tests passed, none skipped). The live check on the platform (§7, last item) is the operator's. Written as a draft on 2026-11-04. The owner's answers are §0; everything else follows from them
and from the invariants in CLAUDE.md. Normative where it says "must". The listener's own rules
(`specs/jira-listener/SPEC.md`, MVP.md §51) hold unless §9 changes them.

## 0. Decisions (the owner, 2026-11-04)

| # | question | answer |
|---|---|---|
| O1 | how the listener runs a ticket | in process: the run is a Go call, not a child `orchestrator run --listener-task` |
| O2 | the child mode | deleted, not kept behind a flag: one path (`run --listener-task` goes with it; the task file stays as the record and the input) |
| O3 | the two self-execs that remain (`git-credential`, `mcp-verify`) | the host binary answers them through `orchestrator.Embedded`; no helper script, no in-memory verify server |
| O4 | a second binary | none in this repository: the CLI keeps `orchestrator jira listen`; the corporate service built on the platform is the "second binary" |
| O5 | the provider access of the corporate service | API keys only (`transport: api`, MVP.md §52); no provider CLI in the image |
| O6 | the module path | this repository on GitHub, `github.com/kasorse/orch`; the service in GitLab takes it with `go get`. The repository is private today and will be made public (§3) |
| O7 | other sources than Jira (Slack, …) | later; this spec leaves the seam visible and builds no interface (§10) |

## 1. The problem

A corporate Go service is built on the platform library (`bootstrap.New`, `AddRunnerFunc`,
`Run`): it gets the health endpoints the deployment chart requires, the logger, metrics and the
shutdown order for free. The Jira listener should be such a service: import the orchestrator,
start the listener as a runner, deploy it through the shared GitLab CI like any other service.

Three things stand in the way today:

1. The module is `orchestrator` and every package is under `internal/`: no other module can
   import it, by path or by Go's `internal` rule.
2. The listener runs a ticket as a child of its own executable (`os.Executable()`, MVP.md
   §51.7): inside a service that executable is the service, which knows no orchestrator command.
   The same binary answers git's credential helper (`git-credential`) and the in-session verify
   server (`mcp-verify`, `action.Dispatcher.executable`).
3. The run assumes it is a process: the repository is `os.Getwd()` (`app.runTask`,
   `app.resume`), the environment of every process it starts is `os.Environ()` shaped by
   `execution.ScrubCredentials`, and its result is one JSON line on stdout (`--output json`).

O1 removes the biggest self-exec and makes the run a function the service can call; O3 covers
the two that stay with ten lines in the host's `main`; §3 and §4 make the module importable.

## Approach

The listener (`internal/app/listen.go`) keeps its loop, its state and its Jira writes, and runs a
ticket by calling the run in its own process instead of starting a child: `runTask` and
`resumeRun` take the repository directory and the task as a value, return the result the JSON
line carried, and the listener's process environment is shaped once at start (the credential
helper's entries set, the Jira variables removed after the client is built, the credential names
registered for scrubbing). The child mode and `run --listener-task` are deleted; `story run`
keeps its children. The module becomes `github.com/kasorse/orch`, and one public
package at the module root exports `Listen` (the listener as a call) and `Embedded` (the host
binary answering `git-credential` and `mcp-verify`, the two execs that remain, through
`os.Executable()` as today). `examples/embed/` shows the corporate service on the platform.

### Ground rules for the tasks of this story

- This is a story task, run with `orchestrator story run specs/embed`. `story run` writes
  `specs/embed/README.md` and each task's `## As built`: **do not edit anything under
  `specs/embed/`**. CLAUDE.md's bookkeeping for `specs/*/tasks` (ticking, the `Status:` line, As
  built) is done by `story run`; tick `TODO.md` P14 only in EB04.
- Read `specs/embed/SPEC.md` in full before starting: the task text names its sections (§N), the
  prompt carries only this Approach and may cut it.
- Each task updates the documentation its own change makes out of date (MVP.md, CLAUDE.md,
  README.md) in the same change; EB04 adds only what needs every task done. The tasks form one
  chain (EB01 → EB02 → EB03 → EB04), each stacked on the one before.
- CLAUDE.md's invariants hold unless §9 changes them: the run never gets a Jira client or a Jira
  variable; `internal/listener` stays pure; git is argv only; no token in argv, `.git/config`,
  logs, state or comments; the public package imports only `internal/app`, `internal/exitcode`
  and the standard library.
- Nothing outside the listener changes behaviour: `run`, `resume`, `review`, `story run` and
  their tests stay as they are, except for the parameter §2.1 names; change a test only where the
  task's own change requires it, and say so in the commit.
- Tests need no network, no key and no platform: the fakes of `test/integration` and the standard
  library. The gate is `scripts/check.sh`; the live check on the platform is the operator's.

## 2. The run in process (O1, O2)

### 2.1 The entry

`app.runTask` and `app.resumeRun` take the repository directory as a parameter; `orchestrator
run` and `orchestrator resume` pass `os.Getwd()` as before (`run`, `review`, `story plan`,
`runs clean`, `validate` keep reading the working directory themselves). The listener calls them
with the service's directory. `reporter.Cwd`, `repository.Open` and every path the run derives
come from that parameter; nothing in a run calls `os.Getwd()` or `os.Chdir` (a test asserts it
over `internal/app`'s run and resume paths).

The listener passes the task as a value, not a file name: `listenerTask` (the struct
`parseListenerTask` validates) replaces the `--listener-task` flag of `runTask`. The listener
still writes `.orchestrator/listener/tasks/<KEY>.<attempt>.json` before the run (the record MVP.md
§51.9 documents, the input a restart reads back) and validates it as the run will, as today.
`run --listener-task` is deleted: `parseArgs` refuses the flag, `checkListenerTaskFlags` goes,
`listener_task.go` keeps the file's type, parsing and `task.source`. Nothing else about the run
changes: `--mode auto`, `execution.isolation: worktree` forced, `jira.write` off, `task.source
{listener: true}`, the clarify step, the guard, the sandbox, delivery with the merge request.

`--claude-auth` and `--codex-auth` of `jira listen` reach the run as the parameters they already
are.

### 2.2 The result

The run's result is the value `buildResult(rec, err)` already computes for the JSON line
(`run_id`, `status`, `exit_code`, `failure_reason`, `failure_kind`, `error`): `runTask` and
`resumeRun` return it to the listener together with the error, through the recorder
`withResultRecorder` creates. `childLine` and `parseChildLine` are deleted; `listener.Decide`
reads the same fields from the value. The exit code is `exitcode.FromError(err)`, as `main.go`
computes it, so the outcome table of SPEC §8 is unchanged.

A panic inside a run is the listener's to contain: `recover` around the call, the ticket's
outcome `failed` with the reason `internal error: <the panic's first line>`, the goroutine's
stack on stderr, the state saved. A panic never ends the listener.

### 2.3 The output

The run's progress goes where the child's stderr went: the listener's stderr, through the plain
reporter (no terminal). Nothing of a run goes to the listener's stdout.

### 2.4 The environment

The run's processes (git, the test command under its sandbox, the MCP servers, a provider CLI
where a service's file still routes to one) build their environment from `os.Environ()` as
today. The listener shapes its own process environment once, at start, so that they see what
the child saw (`listenRunner.environment`, SPEC §6.2):

- The credential helper's entries (`GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_n`, `GIT_CONFIG_VALUE_n`
  naming `<exe> git-credential --config <path>` and `gitlab.base_url`) are set with
  `os.Setenv`; they hold no secret.
- The Jira variables (`jira.email_env`, `jira.token_env`) are removed with `os.Unsetenv` right
  after the listener's Jira client is built: the client already takes the values at construction
  (`jira.NewCredentials` in `newListenerJiraClient`; a test asserts it keeps working after the
  variables are gone). The parent-session variables are
  removed the same way. From then on no process the listener or a run starts can inherit them.
- The names of the Jira and GitLab variables are registered with `execution.ScrubCredentials`
  (what `scrub_env` of the task file did in the child): provider CLIs and the test command never
  get the GitLab token; git keeps it, for the helper, as before.

`os.Setenv`/`os.Unsetenv` are process-global. The listener's process is the listener's: the CLI
is, and a host service (§4) is told so (§4.3). The `scrub_env` field stays in the task file: it
is what the listener registers, and a restart reads it back.

### 2.5 Stopping and restart

SIGTERM/SIGINT cancels the listener's context; the run's context is derived from it, so the step
in flight ends as it ends in a child today (the HTTP call aborts, the test process is killed by
`CommandContext`, the engine persists the step as cut off). `childWaitDelay` no longer applies to
the listener (it stays `story run`'s): there is no child to wait for; the listener waits for the
call to return. The ticket stays in `start` and
is resumed on restart as today (SPEC §9), the resume now the in-process `resumeRun`.

### 2.6 One process, many runs

Nothing a run leaves in package-level state may reach the next run. The known registry,
`execution.ScrubCredentials`, is additive and holds names only, which is correct across runs.
The task reviews `internal/app`, `internal/action`, `internal/execution` and `internal/provider`
for other package-level variables a run writes and makes each per-run or documents why it is
safe; the integration test of §7 runs two tickets in one process and asserts the second run's
artifacts carry nothing of the first (its run id, its service, its `scrub_env`).

Long uptime: every run's processes are waited for and every file the run opens is closed when
`runTask` returns (the test of §7 compares the open file descriptors and the goroutine count
before the first run and after the second, within a tolerance).

## 3. The module path (O6)

`go.mod` names `github.com/kasorse/orch`; every import `orchestrator/internal/...`
becomes `github.com/kasorse/orch/internal/...` (437 files, mechanical). `bench/go.mod`
keeps `orchbench`; `bench/boundary_test.go` checks the new prefix. The documents that spell the
old prefix (CLAUDE.md's bench paragraph, MVP.md where it quotes an import) follow.
`cmd/orchestrator` stays the CLI's main.

What `go get` from the corporate CI needs, and this spec does not do:

- The repository public. The build image sets `GOPRIVATE`, `GONOPROXY` and `GONOSUMDB` for
  `gitlab.com/corp/*` only, so the module comes through `proxy.golang.org` and is checked
  against `sum.golang.org`: a private repository fails there, and a token for GitHub in the
  corporate CI is not wanted.
- A version. There is no tag yet, so `go get` records a pseudo-version
  (`v0.0.0-<date>-<commit>`); a `v0.N.0` tag per merged spec gives the service something to pin
  and renovate to (TODO P9).
- A licence that covers the service. `LICENSE` is the PolyForm Noncommercial License 1.0.0,
  which excludes commercial use, and a company's internal service is commercial use: the owner
  grants the company a separate licence (a written grant naming the company; the repository's
  `LICENSE` stays as it is). The platform's dependency scan will classify PolyForm Noncommercial
  as non-permissive; the grant is the answer to that finding (TODO P10).

## 4. The public package (O3, O4)

One package outside `internal/`, at the module root, named `orchestrator`, file `embed.go`. Two
functions; nothing else is exported, and the package imports only `internal/app`,
`internal/exitcode` and the standard library.

### 4.1 `Listen`

```go
type Options struct {
    Dir        string    // the listener's working directory (state, services_dir); default os.Getwd()
    Config     string    // the listener's configuration file; required
    Once       bool      // one poll and its ticket, then return
    ClaudeAuth string    // "", "auto", "api" or "login": --claude-auth
    CodexAuth  string    // likewise --codex-auth
    Stderr     io.Writer // the log, one line per event; default os.Stderr
}

func Listen(ctx context.Context, o Options) error
```

`Listen` is `orchestrator jira listen` as a call: it builds the argv the CLI would have
(`--config`, `--once`, `--claude-auth`, `--codex-auth`) and calls `app.Run` with `o.Stderr` as
both stdout and stderr, so the fail-fast chain, the flock, `GET /myself`, the state and the
loop are the CLI's, with no second code path. `Dir` is the one thing the CLI has no flag for:
`app.listen` takes it as a parameter and `jira listen` passes `os.Getwd()`. It returns when
`ctx` ends (nil, nothing half-written) or with the error the CLI would map to exit 2 or 1; the
host decides what to do with it (the platform's runner stops the service on a non-nil error).

### 4.2 `Embedded`

```go
func Embedded(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int, handled bool)
```

When `args[0]` is one of the orchestrator's commands (`run`, `resume`, `review`, `validate`,
`version`, `story`, `decisions`, `runs`, `jira`, `mcp-verify`, `git-credential`, `help`), it
runs `app.Run(context.Background(), args, …)` with the CLI's signal handling and returns the exit
code and true; otherwise it returns false without touching anything. The host's `main` calls it
first:

```go
if code, ok := orchestrator.Embedded(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); ok {
    os.Exit(code)
}
```

That is how `git-credential` (git calls `<exe> git-credential --config …`, the entries of
§2.4) and `mcp-verify` (the MCP client starts `<exe> mcp-verify --dir …`) keep working when
`<exe>` is the service: `os.Executable()` stays the way both are found, unchanged. A host whose
own command line uses one of those words cannot embed the listener; the package's doc says so.

### 4.3 What the package's documentation states

- The process is the listener's: `Listen` changes the process environment (§2.4) and holds a
  flock in `Dir`; one `Listen` per process, one process per `Dir` (the flock refuses a second).
- `Dir` must be a volume: the state, the lock, the task files, the clones and their worktrees
  (MVP.md §51.9, README "The working directory is a volume").
- Everything the listener needs on the host: `git` on PATH; `bwrap` unless every service's
  configuration says `test.sandbox: none`; the toolchains of the services' test commands; no
  provider CLI with `transport: api` (O5); the environment variables the configuration names
  (`jira.*_env`, `gitlab.token_env`, `providers.*.api_key_env`).
- Replicas: one. A second listener on the same volume is refused, two on different volumes
  claim the same tickets.

## 5. The example service (O4, O5)

`examples/embed/` in this repository, buildable by the root module (`go vet ./...` compiles it)
and free of any corporate import:

- `main.go`: `Embedded` first, then a `signal.NotifyContext` and `Listen` (the platform's
  `bootstrap` is replaced by the standard library; a comment shows the two platform lines,
  `app.AddRunnerFunc(func() error { return orchestrator.Listen(app.Context(), opts) })`).
- `README.md`: the service repository's files, as the operator writes them on the platform:
  `main.go` on `bootstrap`; a `Dockerfile` whose base is the Go build image plus `bubblewrap`
  (the runtime image has git and make but neither Go nor bwrap, and a run compiles and tests the
  service it changes); the `.gitlab-ci.yml` that includes the platform's shared jobs; the
  deployment values (`secretKeys` for the Jira, GitLab and provider variables, one replica, a
  persistent volume at the working directory, CPU and memory for a compile and a test run, the
  stop timeout above `execution.timeout`); the two configuration files the image carries,
  `listener.yaml` (jira, listener, gitlab, the providers on `transport: api`) and
  `default.yaml` (`listener.default_config`, for a service without its own); and
  `/etc/orchestrator/org.yaml` as the place for the corporate policy (`allowed_models`, the
  cost ceilings, MVP.md §45.3).
- The corporate names (group paths, image registries, CI include paths) are placeholders here;
  the real ones live in the service's repository.

"API only" is not a new rule: a service's `orchestrator.yaml` that routes to a CLI fails the
run's fail-fast chain in an image without that CLI (exit 2 → the ticket `failed`, SPEC §8). The
org layer has no `transport` floor; adding one is out of scope (§10).

## 6. Security

- The invariant "the listener owns Jira; its child never calls it" becomes "the run never calls
  it": `runTask` gets no Jira client and no Jira variable (§2.4), `jira.write` is off for a
  listener task, and `internal/listener` stays pure. CLAUDE.md's text changes accordingly (§9).
- Tokens: unchanged. The GitLab token reaches git through the helper only; provider CLIs and the
  test command get neither it nor a Jira variable (`ScrubCredentials`, `TestEnv`); nothing goes
  to argv, `.git/config`, logs, state or comments.
- `Embedded` adds no capability: it runs the same commands the orchestrator binary would with the
  same argv, under the host's user. A `git-credential` call answers only for `gitlab.base_url`
  of the configuration file named in its argv, as today.
- A panic in a run (§2.2) is reported with the panic's first line only, never a prompt, a diff
  or an environment.

## 7. Tests

- `test/integration/listen_test.go` and `listen_fakes_test.go`: the children were the test
  binary (`TestMain`); the tickets now run in the test's process against the same fake Jira,
  fake GitLab remote and fake provider. `TestMain` keeps answering `mcp-verify` and
  `git-credential`, which are still execs. Every scenario of MVP.md §51 (claim, outcomes, retry,
  interrupted → resumed, a second listener refused, the token never in a URL or a comment)
  passes unchanged in what it asserts.
- New: two tickets in one process (§2.6); the Jira variables absent from a git and a test
  process's environment during the run (`FAKE_ENV_DIR`); a panic injected into a run ends as
  `failed` and the listener polls again; `Listen` with a missing configuration returns a
  `ConfigError` before any request; `Embedded` returns false for an unknown word and the CLI's
  exit code for `version` and for `git-credential get` with the fake's input.
- `go vet ./...` compiles `examples/embed`; `bench/boundary_test.go` passes under the new path.
- The live check on the platform (the service built from `examples/embed/README.md`, deployed to
  dev, one ticket end to end) is the operator's gate, recorded in the README's `Status:`.

## 8. Not changed

Routing, the decision, the workflow, the guard, the sandbox, the secret scan, resume's
validation, delivery, `story run` (its children stay children: `runChildEnv` remains for it),
`run --jira`, the Jira client, the state file's format, the configuration schema.

## 9. Changes to MVP.md and CLAUDE.md

- MVP.md §51: the summary paragraph, §51.1 (the fail-fast chain no longer checks "the listener's
  own executable" for a run), §51.7 (the task is a value; `run --listener-task` gone), §51.10
  (no `childWaitDelay`; cancellation), §51.11 (the run instead of the child); a new §53 "The
  listener as a library" (the package, `Embedded`, the host's duties of §4.3, the example of
  §5). §4 (the commands) loses `--listener-task`.
- CLAUDE.md: the invariant "The listener owns Jira; its child never calls it" reworded for the
  run in process; the architecture paragraph's module path (§3); a line pointing at this spec in
  the summary list; "Run artifacts" (`tasks/<KEY>.<attempt>.json` is the run's input, read in
  process).
- README.md: "Running the Jira listener" (no child; the library in one paragraph with a link to
  `examples/embed/`); `go.mod` path in the build section.
- TODO.md P14.

## 10. Out of scope

- A `Source` interface for Slack or another origin of tasks. The seam is the supervisor loop
  of `listen.go` against the Jira calls it makes (`search`, `Fetch`, `Comments`, `Transition`,
  `Comment`, `Myself`); the loop gains no new Jira knowledge in this spec, and the interface is
  cut when the second source exists.
- A `transport` floor in the org layer; the Jira or GitLab token from anything but the
  environment; several tickets at once; a webhook; a UI.
- An in-memory verify server and a credential helper script (O3).
- Publishing the module (a tag, a proxy, the mirror's CI) beyond naming its path (O6).

## 11. Risks

- A run that leaks a goroutine, a file or a process now leaks into a long-lived service (§2.6
  measures it; a leak found there is a bug in the run, fixed where it is).
- A package-level variable one run sets for the next (§2.6 reviews the four packages; the
  two-ticket test catches what the review misses).
- `os.Unsetenv` of the Jira variables breaks a host that reads them elsewhere; §4.3 says so, and
  a corporate service has no other use for them.
- The module rename touches every Go file: it is one commit, first on its branch, with nothing
  else in it.
