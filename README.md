# AI Model Orchestrator

A local Go CLI that takes one engineering task, decides which workflow stages it needs, and
executes that workflow by shelling out to locally-installed AI CLIs (`claude`, `codex`,
`gemini`) against your current git repository — running real tests and persisting run state
to disk. The full spec is `MVP.md` (the decision layer in detail: `specs/JEV integration/`);
`CLAUDE.md` summarizes the invariants that guide the implementation.

A **decision provider** — the [Jev](https://docs.typesafe.ai) decision API, or one of the
CLIs — only estimates probabilities: does the task need repository context, how complex is
it, how likely is each stage (research, analysis, planning, development, testing, review)
needed. Everything chosen from those numbers is Go code: configurable thresholds pick the
complexity and the stages, the stages always run in the same order, and a deterministic state
machine drives them. The AI models only do reasoning (research, analysis, planning,
implementation, review) — they never decide what happens next. Test pass/fail is decided only
by the real exit code of your test command, never by anything a model says.

## Prerequisites

- Go 1.26.1 or later.
- `git`, on `PATH`, and the target repository must already be a git repo.
- For each provider referenced in your config (`claude`, `codex`, and/or `gemini`) — by an
  action, the translator, or an LLM decision provider — that provider's CLI must be installed
  and resolvable via `PATH` (`exec.LookPath`). You only need the providers your config
  actually uses.
- With `decision.provider: jev`: a Jev API key in the environment variable named by
  `decision.jev.api_key_env` (default `JEV_API_KEY`) — or `decision.jev.mock: true` to run
  without one (see below).
- Only for `run --jira`: a `jira` section in the config and the Jira credentials in the
  environment variables it names (see [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket)).
- `claude` within the version range its permission flags were verified on (today
  `>= 2.1.283, < 2.2.0`); outside it the run refuses to start, see
  [Security model](#security-model).
- [bubblewrap](https://github.com/containers/bubblewrap) (`bwrap`): the test command runs
  sandboxed by default. Linux only; on macOS, or a host where bwrap cannot sandbox, set
  `test.sandbox: none` (see [Sandboxing the test step](#sandboxing-the-test-step)).

## Building

```
go build -o orchestrator ./cmd/orchestrator
```

## Configuration

Copy `orchestrator.example.yaml` to `orchestrator.yaml` (the default config path; override with
`--config <path>`) and adjust it. Version 2 is required; a version 1 config (with
`classifier` and `actions.answer`) is rejected with a list of what changed.

```yaml
version: 2

decision:
  provider: jev              # jev | claude | codex | gemini
  model: jev-1.13.0          # jev: a version, not an alias; CLI provider: its model
  timeout: 5s                # jev: one HTTP request; CLI provider: one call
  complexity:
    primary: choice          # choice | score - the question the policy decides on
    shadow: true             # also ask the other one, for comparison in the trace
  on_failure: ask            # ask: choose complexity and stages by hand; fail: stop
  jev:                       # read only when provider is jev
    base_url: https://api.typesafe.ai
    api_key_env: JEV_API_KEY
    max_retries: 1
    mock: false

translation:
  provider: claude
  model: haiku
  timeout: 60s

policy:
  context_depth:
    project_threshold: 0.5
  complexity:
    complex: 0.55
    medium: 0.70
  stages:
    thresholds: {research: 0.81, analysis: 0.81, planning: 0.81, development: 0.81, testing: 0.60, review: 0.81}
    force:
      complex: [analysis, planning, review]

context:
  max_files: 20
  max_bytes: 24576
  extra_deny: []

actions:
  research:  { provider: claude, model: sonnet }
  analyze:   { provider: claude, model: sonnet }
  plan:      { provider: claude, model: sonnet }
  implement: { provider: claude, model: opus }
  review:    { provider: claude, model: sonnet, timeout: 20m }

test:
  command: "go test ./..."
  sandbox: bwrap             # the default; none must be written out
  env_pass: []               # variables the tests need beyond the allowlist
  baseline: require_green    # run the tests before any change; off skips it

limits:
  max_steps: 12
  max_fix_iterations: 2
  stop_without_progress: true  # stop a fix loop that changes nothing or repeats a failure
  prompt:                    # optional bounds on repository data in prompts
    max_test_output_bytes: 32768
    max_diff_bytes: 262144
    diff_summarize: []

execution:
  timeout: 15m

security:
  require_clean_worktree: true
  protected_paths: []
  allow_unverified_cli: false
  secret_scan:
    mode: stop               # stop | warn | off
    allow_paths: []
```

- **`decision`** chooses who estimates the probabilities, like an action chooses its
  provider:
  - `jev` — the Jev HTTP API. The API key is read from the environment variable
    `api_key_env` names; a literal `api_key` in the file is a validation error.
    `max_retries` retries only 408/429/5xx/529, honouring `Retry-After`.
  - `jev` with `jev.mock: true` — an offline stand-in for Jev: no network, no key,
    deterministic answers from keywords in the request. Useful while you have no API
    access; a warning is printed and the trace marks the model as `mock:<model>`. The
    numbers are plausible, not calibrated.
  - `claude` / `codex` / `gemini` — the same questions answered by that CLI (today only
    `claude` passes the authority check, see [Security model](#security-model)).
  - The flow is the same for all of them. A failing provider never falls back to another:
    with `on_failure: ask` you choose the complexity and stages yourself (interactive runs
    only); with `fail` the run stops.
  - `complexity.primary` picks the question the policy decides on; with `shadow` on, the
    other one is asked too and only recorded, so the two can be compared.
- **`translation`** — a request that is mostly Russian is translated to English for the
  decision provider (the stages still get your original text). If translation fails, the
  original is sent and a warning is printed.
- **`policy`** turns probabilities into decisions: `project_threshold` for collecting
  project context; the cumulative complexity thresholds (complex is checked first); a
  per-stage threshold (inclusive); and stages forced on for a complexity. If no stage
  qualifies, the workflow is `analysis`. The same thresholds apply to every decision
  provider.
- **`context`** bounds the project context sent to the decision provider; `extra_deny`
  adds glob patterns that are never read.
- Each entry under **`actions`** independently chooses a `provider` (`claude` | `codex` |
  `gemini`) and a `model` string passed straight through to that provider's CLI — there's no
  automatic "best model" selection, fallback, or benchmarking. `timeout` optionally tightens
  that action's share of `execution.timeout`. A provider must be able to enforce the
  action's authority: `codex` and `gemini` currently cannot, so routing an action (or the
  translator, or the decision provider) to them fails validation with exit 2.
- `test.command` runs via `sh -c` in the target repository (the one place a shell is used —
  it's trusted operator configuration, not user input). It runs under bubblewrap unless
  `test.sandbox: none` — see [Sandboxing the test step](#sandboxing-the-test-step);
  `test.ro_binds` / `test.rw_binds` re-expose paths such as a module cache (read-only) and a
  build cache (writable). It inherits only an allowlist of your environment; `test.env_pass`
  adds names (never credentials). `test.baseline` (default `require_green`) runs the tests
  once before any change in a workflow that implements and tests, see
  [Tests that already fail](#tests-that-already-fail).
- `limits.max_steps` bounds total workflow steps; `limits.max_fix_iterations` bounds how many
  times a failing test or review can send the workflow back to `implement` — both exist purely
  to guarantee termination. `limits.stop_without_progress` (default `true`) ends a fix loop
  earlier when it goes nowhere: `implement` left the working tree as the previous implement
  did, the same tests fail the same way, or review repeats its blocking issues. The run is
  `stopped` with a `no progress: …` reason and exit code 4, and `orchestrator resume` goes back
  into `implement`; `false` spends every iteration as before. `limits.prompt` bounds what goes into a prompt: a failing test's
  output in the implement retry (keeping the failure lines) and the diff review sees (lock
  files as a stat line, the rest in full while it fits). Artifacts and logs keep everything.
- `execution.timeout` (Go duration syntax, e.g. `15m`) bounds the whole run, including the
  decision — except the time you spend on a manual choice.
- **`security`** (optional) never widens what a step may do. `require_clean_worktree`
  (default `true`) makes a workflow with `implement` refuse to start on a dirty worktree;
  `protected_paths` adds patterns `implement` must not change; `allow_unverified_cli` lets a
  run go on with a provider CLI outside the verified version range (warned and recorded);
  `secret_scan` decides what happens when a step adds what looks like a secret. See
  [Security model](#security-model).
- **`jira`** (optional) is where `run --jira` reads a ticket from. It is validated whenever
  present, but its credential variables are needed only by `run --jira` (and checked by
  `validate`). See [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket).

### Getting more from the same stages (opt-in)

Every stage already gets what the earlier ones found (the analysis and plan files and risks, the
plan's acceptance criteria, and on a retry the diff stat of what the run changed so far), and
review checks each acceptance criterion. The keys below are all optional and off unless
written; a run with none of them behaves as before, apart from those richer prompts and
`limits.stop_without_progress`:

```yaml
checks:                          # run in order before test.command; the first non-zero exit stops the step
  - {name: vet,  command: "go vet ./..."}
  - {name: lint, command: "golangci-lint run --new-from-rev=$ORCH_BASE_REF"}
test:
  command: 'go test ./... ${ORCH_TEST_FILTER:+-run "$ORCH_TEST_FILTER"}'
actions:
  implement:
    provider: claude
    model: sonnet
    effort: medium
    timeout: 30m                 # the verify calls run inside this step: raise its timeout
    verify: true                 # implement may run test.command and the checks (sandbox, guard)
    by_complexity:               # model and effort per decided complexity; --stage uses the base
      simple:  {model: haiku, effort: low}
      complex: {model: opus,  effort: high}
  review:
    provider: claude
    model: sonnet
    models: [opus]               # extra reviewers, one after the other; issues are merged
review:
  fix_medium: true               # a passing review with a medium issue sends implement back once
limits:
  verify: {max_calls_per_step: 8, call_timeout: 10m}
```

- **`checks`** are exit-code gates like the tests: build, vet, lint or a SAST scan, each run
  under the test sandbox, environment and guard, with `ORCH_BASE_REF` set to the commit the run
  started from. A failed check is named in the retry prompt, and the pre-run test runs them too.
  Checks only run in a workflow with a `testing` stage.
- **`verify`** is described under [Verify](#verify-let-implement-run-the-tests-opt-in); the
  `${ORCH_TEST_FILTER:+…}` in `test.command` lets the model run a subset of the tests, and a
  command that does not use it simply runs everything.
- **`by_complexity`** is a static table: Go reads the complexity the decision already made and
  picks the entry; nothing is chosen by a model, and the provider cannot be overridden.
- **`review.fix_medium`** and **`actions.review.models`** make review stricter; the second
  costs one more call per extra model.
- Nothing here changes what decides success: the `test` step's exit code and, for review,
  `ReviewResult.Decide`. Details: `MVP.md` §44 and `specs/quality/`.

## Security model

Every provider call gets the least tools its job needs, chosen by a fixed table in Go code —
never by the model, a provider default or config. **No step has a shell.** A provider that
cannot enforce an authority is refused before any AI call, and nothing ever runs a CLI with
its permission checks turned off.

| call | authority | may | may not |
|---|---|---|---|
| translation, decision | `none` | answer from the prompt | use any tool |
| `research` | `web` | search and fetch web pages | read any file |
| `analyze`, `plan`, `review` | `read` | read and search the repository | write, run commands, use the network |
| `implement` | `edit` | read, create and modify files **inside the repository** | run commands (not even `go build`), use the network, write outside the repository, touch `.git/` or `.claude/` |
| `test` | — | the orchestrator runs `test.command` itself | — |

No call has both repository access and a way out (network or shell). Anything a call was not
granted is denied on the spot, never prompted, so no mode can hang on a permission question.
**Modes do not change authority**: `manual`, `accept-edits` and `auto` all run each step with
the table above; they differ only in how much you see and confirm between steps.

For `claude` that means `--restricted --permission-mode dontAsk --permission-prompts none
--strict-mcp-config --setting-sources ""` plus the authority's `--tools`, with the prompt on
stdin. Those flags were verified on specific CLI versions, so every run first checks
`claude --version`: outside the verified range (`>= 2.1.283, < 2.2.0` today) it refuses
before any call, rather than risk flags that silently mean something else. Run
`go test -tags security ./test/security/...` on the new version and extend the range, or set
`security.allow_unverified_cli: true` to go on with a red warning. Consequences:

- The target repository's `.claude/settings.json` hooks, `.mcp.json` servers and settings are
  never loaded.
- Your own `~/.claude/settings.json` is not loaded either. Authentication that lives in the
  environment or in Claude Code's credentials keeps working; if yours depends on `env` or
  `apiKeyHelper` in user settings, export the variable instead.
- The file tools cannot read the secret deny list — `.env`, `.env.*`, `*.pem`, `*.key`,
  `credentials*`, `secrets*` and `context.extra_deny` — the same list the Context Manager
  applies. The match is case-sensitive (`.ENV` is not covered).

Around `implement` the orchestrator itself guards the result, for any provider:

- A workflow with `implement` refuses to start on a dirty worktree (commit or stash first, or
  set `security.require_clean_worktree: false`).
- Before the first step it records a restore point, printed as `Restore point: <sha>` and
  saved as `pre_run_ref` in `run.json`: HEAD, or on a dirty tree a snapshot commit of
  everything (untracked files included) that leaves your tree, index and stash alone.
  `git checkout <sha> -- .` brings the files back. The commit is pinned under
  `refs/orchestrator/runs/<run-id>` so `git gc` never prunes it; those refs are not
  branches or tags and are never pushed by default. They are kept until you drop them:
  `git for-each-ref refs/orchestrator` lists them,
  `git update-ref -d refs/orchestrator/runs/<run-id>` removes one.
- After every `implement` call, before anything runs its output, it checks that no protected
  file changed — `.claude/**`, `.mcp.json`, `.github/workflows/**`, `.gitlab-ci.yml`,
  `.envrc`, `.vscode/**`, the config file when it lives in the repository (even if
  git-ignored), `security.protected_paths` — and that `.git/config` and the git hooks are
  untouched. Otherwise the run stops (exit 4) naming the files; the changes stay for you to
  inspect.
- In `manual` mode the prompt before the step after `implement` lists what it changed.
- What a resume reads back — every run's `run.json`, `workflow.json`, `decision.json` and
  `artifacts/` — is protected the same way. The file tools are denied the whole
  `.orchestrator/runs/` directory as well.
- The same check runs around the test command, which executes code the model wrote: a test
  that plants a hook, CI file or agent setting stops the run too. `.git` is compared before
  the orchestrator runs `git status`, which would otherwise execute a planted
  `core.fsmonitor`.
- After every `implement` and test step, what the working tree adds is scanned for likely
  secrets (cloud and service tokens, private keys, JWTs, passwords in URLs, high-entropy
  assignments). With `security.secret_scan.mode: stop` (the default) a new one stops the run
  (exit 4) naming `file:line (rule)` — never the value; `warn` records it in `run.json` and
  goes on; `off` skips it. What was already there when the run started does not count, and
  `allow_paths` (e.g. `testdata/**`) exempts fixtures. While the scan is on, the diff sent
  to `review` has every match replaced by `[REDACTED:<rule>]`, and it never includes the
  content of a file on the deny list.

### Sandboxing the test step

The test command runs under [bubblewrap](https://github.com/containers/bubblewrap) by
default (`test.sandbox: bwrap`, or no key at all):

| | access |
|---|---|
| the filesystem (`/usr`, `/etc`, your toolchain) | read-only |
| the repository | read-write; its `.git` read-only |
| `/tmp` | a private, empty, writable tmpfs (`TMPDIR=/tmp`) |
| `$HOME` (`~/.ssh`, `~/.aws`, `~/.config/gh`, …) | hidden — an empty tmpfs |
| `test.ro_binds` / `test.rw_binds` | re-exposed read-only / read-write |
| network | none |

So a test can build and write inside the repository, but cannot read your credentials, send
anything out, or write outside the repository. For Go, re-expose the module cache and the
build cache, or every run starts cold and cannot download modules:

```yaml
test:
  command: "go test ./..."
  sandbox: bwrap
  ro_binds: [~/go/pkg/mod]
  rw_binds: [~/.cache/go-build]
```

Tests that need the network or outside services (a database, docker) fail in the sandbox;
write `sandbox: none` for them — every run then says `NOT sandboxed: network, $HOME and your
files are reachable`. Linux only: on macOS write `sandbox: none`. `run` and `validate` probe
bwrap before any AI call and refuse to start when it cannot sandbox; on Ubuntu 24.04 and
later AppArmor blocks bwrap's user namespaces until you install a profile for it:

```bash
sudo tee /etc/apparmor.d/bwrap > /dev/null <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>
profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE
sudo systemctl reload apparmor
```

The test command — sandboxed or not — inherits only an allowlist of your environment:
`PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, the locale, `TERM`, `TZ`, `TMPDIR`, `CI`, and
toolchain variables (`GOPATH`, `GOROOT`, `GOFLAGS`, `GOCACHE`, `GOMODCACHE`, `GOTOOLCHAIN`,
`CGO_ENABLED`, `CARGO_HOME`, `RUSTUP_HOME`, `JAVA_HOME`, `VIRTUAL_ENV`, `PYTHONPATH`,
`NODE_OPTIONS`, `XDG_CACHE_HOME`). No `ANTHROPIC_API_KEY`, `AWS_*`, `GITHUB_TOKEN`,
`SSH_AUTH_SOCK` and no proxies (a proxy URL can carry credentials). Name what your tests need
in `test.env_pass`; a name that looks like a credential (`TOKEN`, `SECRET`, `PASSWORD`, a
`_KEY` or `_AUTH` segment, …) is refused — a run that executes model-written code never gets
one.

Provider CLIs keep the rest of your environment, but do not inherit the variables through
which a Claude Code session you launched the orchestrator from reaches its children
(`CLAUDECODE`, `CLAUDE_CODE_MESSAGING_*`, …), nor the credential variables your config names
— the one in `decision.jev.api_key_env` and, with a `jira` section, `jira.token_env` and
`jira.email_env`. The orchestrator reads those itself.

### Extensions: your MCP servers, plugins and hooks (opt-in)

By default no step sees your Claude setup. With an `extensions` section you choose what it
does see:

```yaml
extensions:
  mcp:
    graphify:                     # a code knowledge graph instead of grep-only search
      command: graphify-mcp
      args: ["{repo}/graphify-out/graph.json"]   # {repo} = the repository root
      tools: [query_graph, get_node, get_neighbors, shortest_path]   # or ["*"]
      actions: [analyze, plan, implement, review]                    # default: analyze, plan, review
  user_settings: true             # ~/.claude/settings.json: plugins, skills, hooks
  user_settings_actions: [implement, review]                         # default: every model step
```

- **`mcp`** passes only the listed servers (`--mcp-config` with `--strict-mcp-config`).
  - Only the listed tools are allowed; any other tool of the server is denied.
  - Only the listed actions get the server.
  - Steps keep `--restricted`, so the file tools and the authority table stay as above.
  - The step's prompt names the extra tools, so the model uses them rather than defaulting
    to grep.
  - The server runs with your permissions, like any MCP server you use with Claude.
- **`user_settings`** loads your **user** settings only (`--setting-sources user`): your
  plugins, skills and hooks, such as writing a session to memory.
  - The repository's own `.claude/` settings and hooks still never load.
  - claude's `--restricted` ignores settings files, so these steps run without it.
  - What it would add is kept by the rest of the command line: `--tools` lists the same
    built-in tools (no shell), anything not pre-approved is denied, and the file tools stay
    confined to the repository (checked against claude 2.1.287).
  - **Your hooks run your code with your permissions** on every such step. Enable this for
    hooks you trust, ideally only on the actions that need them.

Extensions are only supported with provider `claude`; `validate` refuses them elsewhere. The
server names `orch` (the orchestrator's own) and any name with `__` are refused.

### Verify: let `implement` run the tests (opt-in)

With `actions.implement.verify: true` the implement session gets two tools served by the
orchestrator itself (`orchestrator mcp-verify`, a hidden subcommand), `run_tests` and, when
`checks:` are configured, `run_check`:

- The model picks only a check name or a test filter (`[A-Za-z0-9_./:-]`, reaching the command
  as `ORCH_TEST_FILTER`); the command is `test.command` or a check from your config, never
  model text. It needs `test.command`, `test.sandbox: bwrap` and provider `claude`.
- Each call runs under the test sandbox, after the protected-paths guard has compared the
  repository with the start of the step. If the model has changed hooks, CI or the
  orchestrator config, the call is refused, nothing more runs in the step, and the step fails
  with "the model's code changed protected files during a verify call".
- `limits.verify.max_calls_per_step` (default 8, at most 50, refused calls included) and
  `limits.verify.call_timeout` (default 10m) bound it. The `test` step still runs afterwards
  and is the only result that counts.
- Every call is recorded in `run.json` (`executions[].verify_calls`, `outcome.verify_calls`),
  its output in `logs/<NNN>-implement.verify-<K>.{stdout,stderr}.log`, and `summary.md` has a
  `Verify` row.
- The step works in a private `orch-verify-*` directory of `$TMPDIR` that it removes when it
  ends; a start of `run` or `resume` removes your own such directories older than a day.

### What is not protected

- **With `test.sandbox: none`, code the model writes runs in `test`** with your filesystem
  and network (its environment is still the allowlist) — reviewing the changes in `manual`
  mode before `test` is then the main mitigation. With the sandbox, a test can still read
  what your user can read outside `$HOME`, and change anything inside the repository (the
  secret scan looks at what it added).
- **The secret scan is a set of patterns**, not a guarantee: a secret split across lines,
  encoded, written into a git-ignored file or into a model's prose is not caught.
- **Your repository goes to the model provider** — that is the job. Only the deny list above
  is withheld, and only from the file tools.
- **Prompt injection is reduced, not eliminated.** Text in the repository or the diff can
  still mislead a step; what it can make that step do is bounded by the table above, and the
  guard, `review` and the diff expose the result.
- **`test.command` is trusted configuration** run through `sh -c`. For a repository you do not
  control, pass `--config` from outside it, keep the sandbox on, keep `manual` mode, and run
  the whole thing in a container.
- **A Jira ticket is task text written by whoever can edit it.** With `run --jira` it is
  treated exactly like text you typed: printed in full before anything is decided, kept
  verbatim in `run.json` and `ticket.json`, and given no extra authority — but also no
  extra gate, even in `--mode auto`. Read the printed task when the ticket's author is not
  you.
- **A hostile provider CLI binary** is out of scope.
- **A provider CLI orphaned by `kill -9`** of the orchestrator can keep writing after the
  last check. A resume catches protected files and a malformed `run.json`, but a subtly
  edited artifact could reach the next prompt. Check the history a resume prints before
  approving it.

Details, the threat model and the rejected alternatives: `specs/permissions/`; normative
summary: `MVP.md` §38.

## How a workflow is decided

1. **Language.** A request whose prose is at least 30% Cyrillic is translated.
2. **Pre-check.** The decision provider estimates whether classifying the request needs the
   repository; above `project_threshold`, project context is collected.
3. **Project context.** A compact summary — never whole files: language and dependencies
   from the manifest, test layout, bullet rules from `AGENTS.md`/`CLAUDE.md`/
   `CONTRIBUTING.md`, matching README/docs sections, the files relevant to the request with
   one-line summaries, git branch and changed paths.
4. **Classification.** Complexity distribution(s) and each stage's probability.
5. **Policy.** Thresholds pick the complexity and the stages; forced stages are added.
6. **Workflow.** `research → analyze → plan → implement → test → review`, filtered to the
   enabled stages. A workflow without `implement` ends by printing the answer written by its
   last read-only step.

## What is sent to Jev

With `decision.provider: jev`, two requests per run go to `decision.jev.base_url`
(`https://api.typesafe.ai` by default):

- the request text — the English translation when one was made;
- when project context is needed, the context described above: paths, one-line summaries,
  rules, doc excerpts, dependency names, test layout, git branch and changed paths.

Never sent: file contents beyond those summaries, anything matching the deny list
(`.env`, `.env.*`, `*.pem`, `*.key`, `credentials*`, `secrets*`, plus `context.extra_deny` —
matched against every path segment), files ignored by `.gitignore`, binaries, files over
256 KB, symlinks, `.git/`, `.orchestrator/`. `project_context.json` in the run directory is
exactly what was sent. Per Jev's documentation, customer data is not used for training, and
zero data retention is offered only on the enterprise plan — check their current terms before
sending proprietary context. With `jev.mock: true` nothing leaves the machine; with an LLM
decision provider the same data goes to that CLI. With `run --jira`, the request text is the
rendered ticket (and your notes), so the ticket goes to the decision provider and, when it
is mostly Russian, to the translator, exactly as typed text would.

## What is sent to Jira

Only with `run --jira`, and only one read: `GET <jira.base_url>/rest/api/2/issue/<KEY>`
asking for the summary, description, issue type, parent and your configured `fields`, with
your credentials in the `Authorization` header. Nothing is ever written to Jira. The
request goes to `jira.base_url` only — never to a URL you pasted, and never through a
redirect — over `https` (plain `http` is accepted only for `localhost`/`127.0.0.1`/`::1`).

## Usage

```
orchestrator [--config path] version
orchestrator [--config path] validate
orchestrator [--config path] run [--verbose|-v] "<task description>"
orchestrator [--config path] run --jira <KEY|URL> ["notes"]
orchestrator [--config path] run --stage <name> [--stage <name> ...] "<task>"
orchestrator [--config path] [--mode m] resume <run-id>
orchestrator runs report [--dir <path>]
orchestrator decisions report
```

- `version` — prints the build version.
- `validate` — loads and validates the config, including checking that every referenced
  provider CLI is actually resolvable on `PATH`, that the Jev API key is set and, when the
  config has a `jira` section, that its credential variables are set, and reports the
  result without running anything. With a real (not mock) Jev it also asks Jev whether it
  serves `decision.model` (`✓ jev serves jev-1.13.0`). Run this first when setting up a new
  config. Unknown keys in a version-2 config (a typo such as `timout`) are an error, listed
  with their line numbers.
- `run "<task>"` — decides the workflow (printing the decision: context depth, complexity,
  who decided, the workflow) and executes it step by step against the repository in the
  current working directory, printing progress as it goes.
- `run --jira <KEY|URL> ["notes"]` — takes the task from a Jira ticket. See
  [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket).
- `run --stage <name> [--stage <name> ...] "<task>"` — runs exactly the stages you name and
  skips the decision layer (no translation, Jev or LLM call, project context). See
  [Choosing the stages yourself](#choosing-the-stages-yourself).
- `resume <run-id>` — continues a run that failed, was stopped, was only planned
  (`--mode plan`) or whose process died, from where it stopped. See
  [Resuming a run](#resuming-a-run).
- `runs report [--dir <path>]` — what your runs cost and how they ended, over the run
  directories in `.orchestrator/runs/` (or every directory holding a `run.json` under
  `<path>`): the runs by status, then cost, success and the share of cost spent on the
  decision by complexity and by stage set, then the `decisions report` comparison below. Runs
  written before `outcome` existed are computed on the fly. It needs no config and calls
  nothing. The benchmark in [`bench/`](bench/README.md) uses it on the run directories it
  collects.
- `decisions report` — the older name of the last part of `runs report`, kept as an alias:
  compares the two complexity questions (`choice` and `score`) over
  the `decision.json` of every run in `.orchestrator/runs/`: agreement rate, a 3×3 matrix,
  mean confidence, and the runs where they disagree. Manual (`--stage`) runs, runs chosen by
  hand, mock answers and runs without the shadow question are skipped. It needs no config and
  calls nothing. Until there is Jev access, `decision.provider: claude` collects real data.
- `--mode plan|manual|accept-edits|auto` — without it, `run` asks you to approve or cancel the
  decided workflow and then to pick a mode; `manual` confirms every step. `auto` asks nothing
  (and, if the decision provider fails, stops instead of asking you to choose).
- `--verbose` / `-v` — the sole verbosity switch (no config key or environment variable
  equivalent). Adds the per-stage decision table (probability, threshold, forced, reason),
  each AI step's `Authority:` line and, for every provider call including translation and
  LLM decisions: the child process's live output prefixed with `  | `, a heartbeat line
  every 30s while the call is in flight, and a `✓ completed (Ns)` line. Without `--verbose`
  you only see a `→ running <provider>...` line before each call. The command line (the
  prompt is never in it: it goes on stdin, and the message says `prompt: N bytes on stdin` —
  the prompt body itself is never printed or logged, anywhere) and the working directory
  appear in a failure's message, with the path of the full log.
- **Terminal UI.** When stdout is an interactive terminal, the same information is rendered
  the way agentic CLIs like Claude Code do: a boxed run header, `⏺` blocks with `⎿` details,
  probability bars for the stages, an animated spinner with elapsed time while a model or
  the test command works, coloured review severities, styled prompts and a boxed summary
  with the chain of steps that ran. Piped or redirected output (CI logs, `| tee`) stays the
  plain text described above, byte for byte. `NO_COLOR=1` or `ORCHESTRATOR_PLAIN=1` forces
  plain text on a terminal too. Preview it without running a model:
  `ORCHESTRATOR_DEMO=1 go test ./internal/logging -run TestPretty_FullRun -v`.
- **Sidebar.** On a terminal at least 120 columns wide and 20 rows tall, the context lives
  in a sidebar pinned to the top right, out of the transcript. It shows the workflow
  checklist (the running step marked), usage and cost so far, the 5-hour and weekly rate
  limits, and the repository's branch and recent commits. It updates live. The transcript
  stays ordinary scrolling output: copy, scrollback and prompts work as usual, the sidebar
  steps aside while a question waits for an answer, and it never scrolls into the
  scrollback. When the run ends it closes, and the summary prints usage and limits
  inline. On narrower terminals a single dim status line between steps takes its place
  (`▸ test · 1/2 steps · 32k tok · $0.053 · 5h 95%`). `ORCHESTRATOR_SIDEBAR=off` keeps that
  layout on wide terminals too. Code diffs always stay in the transcript.
- **Review screen.** On an interactive terminal (macOS, Linux, BSD), the approval is a
  keyboard form instead of a typed `[a]pprove / [c]ancel`. In it you can:
  - toggle stages: `↑↓` and `space`, or the digits `1`-`6`;
  - override the complexity with `←→`. Picking one switches on the stages
    `policy.stages.force` forces for it, and switching a forced stage off is marked as an
    override;
  - pick the mode when no `--mode` was given;
  - press **Run** or **Cancel**: `Enter` runs, `Esc` cancels, `Tab` moves between rows.

  The workflow line under the form previews exactly what will run. An edit is recorded as
  `decision.edit` in `run.json` and `decision.json`, holding the complexity and the stages
  as decided, and a resume continues the edited workflow. A resume shows the same screen
  without the editing. Piped input, `--mode auto` and other terminals keep the line
  prompts.

Config loading, structural validation, dependency validation (executables on `PATH`, the
Jev API key) and the authority check (every route's provider can enforce its authority) always
run to completion, in that order, *before* any AI provider is invoked or any request is sent
to Jev — a missing executable for an action used later in the workflow (e.g. `codex` for
`review`) fails `run` immediately. `orchestrator validate` runs the same chain. With a real
Jev, `run` then asks Jev whether it serves `decision.model` before anything else; with
`on_failure: ask`, an unreachable Jev is only a warning there.

`execution.timeout` bounds the entire run, including the decision — not just workflow step
execution. Each provider/test child process runs in its own process group, so a timeout or
Ctrl-C kills the whole process tree it spawned, not just the direct child.

If the target repository has uncommitted changes when `run` starts, a workflow with
`implement` refuses to start (see [Security model](#security-model)); any other workflow
prints a warning and continues. The orchestrator never stashes, resets, or otherwise touches
your working tree on your behalf.

### Committing the result

When a run that implemented something completes, `run` (and `resume`) shows what changed
and offers to commit it on the current branch:

```
Changes:
   M fixture.go

Commit message:
  Fix Add to use +

  Orchestrator-Run: 3f2a9c1e-...

Commit to main? [y]es / [N]o / [e]dit message:
```

Nothing is committed without an explicit `y`; `e` lets you replace the first line. The
message is the implementation's one-line `summary_short` (prefixed with the ticket key for
`run --jira`), with no model call; when the model left it out, you are asked to type the
message (empty skips the commit). The commit stages everything the run changed (untracked
files included, ignored files and `.orchestrator/` left out), uses your git identity and
runs your hooks; if it fails, the changes stay staged and the error is printed. A failing
review verdict or a likely secret (`secret_scan.mode: warn`) is shown as a warning above
the question. `run.json` records the commit as `commit`.

It is not offered with `--mode auto`, when the run started on uncommitted changes (they
would end up in the same commit), or when git's config or hooks or a protected path
changed since the run started (`git commit` runs those hooks). See `MVP.md` §42.

`auto_commit: true` in the config commits without the question, in every mode including
`--mode auto`, after the same checks. It does not commit when there is a warning (a failing
review, a likely secret) or no `summary_short` to use as the message; it says why and the
changes stay in the working tree.

### Resuming a run

```
orchestrator resume 3f2a9c1e             # the full run ID, or a unique prefix of ≥ 8 characters
orchestrator --mode auto resume 3f2a9c1e # unattended
```

The run ID is printed as `Run: <id>` when the run starts. Resume **never decides again**. It
reuses the run's decision and workflow and makes no Jev or decision-provider call. From the
run's history it works out where to continue: a failed step is retried, and after a failed
test it goes back to `implement` with the test output, as if the run had never stopped. Steps
that already succeeded are not paid for again. The current config applies from here on:
providers, models, timeouts, limits, test command.

Before asking anything it shows the previous attempt, every step so far, the working tree,
the next step and the remaining workflow. Without `--mode` you approve and pick a mode as for
`run`, and pressing Enter keeps the run's earlier mode. A run that was `auto` still asks, so an
unattended resume needs `--mode auto`.

A resume refuses, before anything runs:

- when a protected file, `.git/config` or a git hook differs from when the run started — for
  example after a step the guard stopped, or a crash mid-step. `.git` is compared before any
  `git status`. Revert those files to resume, or start a new run to keep them;
- when HEAD has moved since the run started, because review diffs against HEAD
  (`git reset --soft <head_ref>` brings it back with your changes kept);
- when a limit is already used up. Limits count the whole history, so raise the named key
  (`limits.max_fix_iterations`, `limits.max_steps`) first;
- when `run.json` or an artifact was edited, is missing or does not match (`corrupt`,
  exit 1), or when another orchestrator process is working on the run (`in_use`, exit 1).

Your own edits to other files between the stop and the resume are fine. They show up in the
working tree listing, and `review` sees them in the diff. A completed run is just reported
(with its answer, for a read-only workflow). Details: `MVP.md` §37 and `specs/resume/`.

### Tests that already fail

A workflow that implements and tests first runs `test.command` once, before any change
(`pre-run test` in the output). If the tests already fail, the run stops before `implement`
(exit 4) — otherwise `implement` would be sent to fix failures it did not cause:

```
tests fail before any change (pre-run test exited 1; output in .orchestrator/runs/<id>/logs/pre-run-test.stdout.log)
```

When the task *is* to make failing tests pass, say so for that run:

```bash
orchestrator run --allow-red-tests "fix the bug TestParseDate shows"
```

The run goes on, the first `implement` is shown what failed, and the run still succeeds only
if the test step ends green. A run stopped this way resumes once the tests pass
(`orchestrator resume <run-id>`). `test.baseline: off` skips the check for every run.

### Choosing the stages yourself

```
orchestrator run --stage review "review my change"
orchestrator run --stage testing --stage review "check the hand edit"
orchestrator run --stage plan "plan the cache"
orchestrator run --stage review --jira PMC-2789
```

Use it when you already know what you need: review code you changed by hand, just run the
tests, plan without implementing. Names are `research`, `analysis`, `planning`,
`development`, `testing`, `review`, plus the aliases `analyze`, `plan`, `implement`, `test`.
Repeat `--stage` for several; one stage per flag. The order is always the fixed one, whatever
order you give them in, and the retry rules, permissions, test command and resume are the same
as for a decided run. `decision.json` records `source: manual`.

* A stage whose usual input you skipped (planning without analysis, development without a
  plan) prints a warning and still runs. Testing and review never warn.
* `review` looks at your uncommitted changes against `HEAD`. With no changes it fails with
  `nothing to review` before calling the model.
* `development` still needs a clean worktree (or `security.require_clean_worktree: false`);
  without it a dirty tree only prints the usual warning.
* The config must still be valid and every executable it names present, including the
  decision provider's and the Jev key, even though they are not called.

### Taking the task from a Jira ticket

```
orchestrator run --jira PMC-2789
orchestrator run --jira pmc-2789 "only the FE part; leave pim-qa alone"
orchestrator run --jira https://yourcompany.atlassian.net/browse/PMC-2789 --mode plan
```

The orchestrator reads the issue once, renders its key and summary, type, parent, a link,
the description and any configured `fields` into plain task text, appends your notes last
(`Operator notes:`), and prints the whole text before deciding anything. From there the run
is exactly a typed `run`. Wiki markup (`{{code}}`, `*bold*`, `#` lists) is passed through
verbatim — models read it fine. A ticket that renders to more than `jira.max_bytes` is
refused, never cut. A moved issue is followed to its new key, with a warning. A URL must be
the issue's `/browse/<KEY>` link on your configured instance; board and search links are
refused.

Add a `jira` section to the config (it is commented out in `orchestrator.example.yaml`):

```yaml
jira:
  base_url: https://yourcompany.atlassian.net   # Data Center: including any context path
  auth: basic                  # basic: Jira Cloud | bearer: Data Center
  email_env: JIRA_EMAIL        # basic only
  token_env: JIRA_API_TOKEN
  fields:                      # optional extra text fields, rendered in this order
    - id: customfield_10035
      label: Acceptance criteria
```

- **Jira Cloud:** create an API token at <https://id.atlassian.com/manage-profile/security/api-tokens>,
  use `auth: basic`, and export your account email and the token:
  `export JIRA_EMAIL=you@example.com JIRA_API_TOKEN=...`.
- **Jira Data Center / Server:** create a personal access token in your Jira profile, use
  `auth: bearer`, leave out `email_env`, and export the token in the variable `token_env`
  names.
- **Custom field ids:** `GET <base_url>/rest/api/2/field` (e.g. `curl -u "$JIRA_EMAIL:$JIRA_API_TOKEN"
  https://yourcompany.atlassian.net/rest/api/2/field`) lists every field with its `id` and
  `name`. Only text fields work; a select, user or number field fails the run with a message
  naming it.

Credentials only ever come from the environment — a `token`, `api_token`, `password`, `pat`
or `email` key in the file is rejected. They are needed only by `run --jira`; a plain `run` and a
`resume` work without them, even with the section configured. A resume never re-reads the
ticket: the task saved when the run started is the task, and the resume header says so
(`Task from PMC-2789, fetched … (not re-fetched)`).

When the ticket cannot be read, nothing is created and no AI is called: bad credentials,
a missing or invisible issue, or a redirect exit 2; Jira being down or answering
unexpectedly exits 3. Details: `MVP.md` §39 and `specs/jira/`.

## Run artifacts

Each `run` persists its state under `.orchestrator/runs/<run-id>/` in the target repository:

```
.orchestrator/runs/<run-id>/
  run.json              # the task (with task.source for run --jira), run status, the
                        # decision, step history (with each AI step's
                        # provider.authority and attempt), limits/iteration counters,
                        # pre_run_ref, head_ref, resume_baseline, resumes, provider_cli
                        # (CLI versions), allow_red_tests, pre_run_test,
                        # secret_scan_baseline, secret_findings (never a value),
                        # commit (the SHA, when you approved the commit at the end);
                        # per step: usage (tokens, cost, partial), call (prompt size and
                        # cuts, models, API time, tool calls), an implement's diff;
                        # outcome (status, fix iterations, last test and review result,
                        # wall and active time, cost, decision overhead, net change)
  summary.md            # the run's report for a reader, written when the run ends
  decision.json         # the full decision trace (see below)
  project_context.json  # exactly the context sent to the decision provider, if any
  workflow.json         # the workflow built from the decision
  ticket.json           # run --jira only: the issue as fetched (a record; never read back)
  artifacts/<NNN>-<action>.json          # structured output per step, and for a model step
                                         # the "prompt" it was sent (a record; never read back)
  logs/<NNN>-<action>.{stdout,stderr}.log  # raw provider/test output per step
  logs/translate.{stdout,stderr}.log       # the translation call, if one was made
  logs/decide-{context,classify}.{stdout,stderr}.log  # LLM decision provider calls
  logs/pre-run-test.{stdout,stderr}.log    # the tests before any change, when red
  logs/<NNN>-test.<check>.{stdout,stderr}.log  # each `checks:` command (pre-run-test.<check>.* before the run)
  logs/<NNN>-implement.verify-<K>.{stdout,stderr}.log  # each verify call of an implement step
```

The quality-loop keys add `checks`, `failed_check` and `failed_tests` to a test result,
`options`, `tree_after`, `verify_calls`, `model_source`, `criteria`, `reviewers` and
`extra_calls` to `run.json`, `models` to `decision.json` and `acceptance_criteria` to the plan
artifact; the field reference is `MVP.md` §44.9.

A review artifact also records what the reviewer was shown: `diff: {bytes, files, shown,
summarized, withheld, truncated}`.

`summary.md` is the place to start after a run: status, wall and active time, cost (a lower
bound when a call was cut off before its final report), tokens, the run's net code change,
then a table of steps (time, API time, cost, tokens, turns, the model that actually
answered, prompt size and what was cut), the tool calls per step, each implement's change
from git against what its artifact claims, and the failures. Every field is explained in
`MVP.md` §43.

`decision.json` answers "why did this run have these stages?": the request and its
translation, who decided (`jev`, `llm`, or `user` with the provider's error), the model
version that answered, the pre-check distribution and chosen depth, both complexity questions
with their picks and whether they agree, every stage's probability, threshold, forced flag and
reason, the workflow, how long each decision step took and what each decision call cost
(`usage`, where the provider reported it).

Every provider call's raw stdout/stderr is persisted this way, whether it succeeds or fails —
not just failures.

Add `.orchestrator/` to the target repository's own `.gitignore` if you don't want this
bookkeeping tracked there.

## Reading a failure

When a provider call fails, `run` exits non-zero (see `MVP.md` §24 for the exact codes) and
prints a self-contained error, e.g.:

```
provider execution failed

provider: claude
action: implement
model: opus
failure: exit (the CLI ran and exited non-zero)
command: claude --print --input-format text --output-format stream-json --verbose --restricted --permission-mode dontAsk --permission-prompts none --strict-mcp-config --setting-sources '' --settings '{"permissions":{"deny":["Read(**/.env)",...]}}' --tools Read,Grep,Glob,Edit,Write --allowed-tools Edit,Write --model opus
prompt: 1832 bytes on stdin
working dir: /home/user/repo
exit code: 1
duration: 2.4s

stdout:
API Error: Connection error.

stderr:
(empty)

full output: .orchestrator/runs/2f3c.../logs/002-implement.stdout.log
```

A decision provider failure reads `decision provider <name> failed: <kind>` — `timeout`,
`connection`, `rate limited`, `unavailable`, `auth`, `bad request` or `invalid response` —
with the HTTP status, the number of attempts and a bounded excerpt of the response (never the
API key). It exits 3, or 2 for `auth`.

A failed `run --jira` fetch names the ticket and what to fix, e.g. `PMC-2789 does not exist,
or the account behind JIRA_EMAIL cannot see it (HTTP 404)` or `Jira rejected the credentials
in JIRA_EMAIL/JIRA_API_TOKEN (HTTP 401)`, never a credential. It exits 2 for credentials, a
missing issue or a redirect, 3 when Jira is down or answers unexpectedly, and creates no run.

- `failure:` says *how* the call failed: `spawn` (couldn't start the executable at all),
  `exit` (ran and returned non-zero), `timeout` (exceeded `execution.timeout`), or `cancelled`
  (SIGINT/SIGTERM). Some provider CLIs — notably `claude --print` — write backend/API errors to
  stdout rather than stderr, so both streams are always shown; a blank stream renders as
  `(empty)` rather than nothing, so you can tell "no output" from "output not captured".
- `stdout:`/`stderr:` are each truncated to their last 4000 bytes in this message. The
  `full output:` line, when present, points at the untruncated file on disk.
- `command:` is the real argv that ran; the prompt is not in it — it goes on stdin, and the
  message says `prompt: N bytes on stdin`. The prompt text itself is never printed or written
  to any log file.

Phase-0 safety stops read like this (all before or instead of the step that would have gone
on):

- `claude 2.2.1 has not been verified ... verified: >=2.1.283 <2.2.0` (exit 2) — see
  [Security model](#security-model).
- `the test command runs under bwrap (test.sandbox: bwrap, the default), but bubblewrap
  cannot create a sandbox on this host` (exit 2) — install the AppArmor profile above, or
  write `test.sandbox: none`.
- `tests fail before any change (pre-run test exited 1; output in …)` (exit 4) — see
  [Tests that already fail](#tests-that-already-fail).
- `implement added what looks like a secret; the run stops before anything sends it on:
  pkg/cfg.go:12 (aws-access-key-id)` (exit 4) — remove it, exempt a fixture with
  `security.secret_scan.allow_paths`, or switch to `mode: warn`.

## Testing

```
go build ./...
go vet ./...
go test ./...
gofmt -l .   # should print nothing
```

The benchmark in `bench/` is a separate Go module, which the root `go test ./...` does not
include: test it with `cd bench && go test ./...`. `scripts/check.sh` runs the four gates in
both modules. What `bench/` is and how to run it: [`bench/README.md`](bench/README.md).

`go test ./...` requires no network access, no Jev key and no real `claude`/`codex`/`gemini`
installs: the `test/integration` suite uses fake executables installed on a scoped `PATH` and
a fake Jev served by `httptest`.

The live security suite checks that `claude` actually enforces each authority — every
refusal paired with a control showing the same harness sees the action when it is allowed.
It makes about a dozen small real calls (model `haiku`, override with
`ORCH_SECURITY_MODEL`) and needs network; its bwrap test skips where bwrap cannot sandbox:

```
go test -tags security ./test/security/...
```

## Limitations

This is an MVP, deliberately scoped down (see `MVP.md` for the full rationale). Notably absent:

- No HTTP-based provider APIs (OpenRouter, LiteLLM, etc.) — the AI steps run only as local
  CLI subprocesses. The HTTP integrations are the Jev decision API, which only returns
  probabilities, and reading one Jira issue for `run --jira`.
- No automatic model selection, routing, or fallback between providers — including between
  decision providers.
- Stages cannot yet be edited before a run (approve or cancel only); the decision thresholds
  are calibrated for Jev and shared with LLM decision providers.
- No parallel or multi-agent workflows — one task, one sequential workflow, one run at a time.
- No web UI, REST API, or IDE plugin — CLI only.
- No GitHub/GitLab integration or remote repository execution — operates on the local working
  directory only. Jira is read-only and limited to one issue: no comments, transitions,
  linked issues, attachments or JQL.
- No RAG, persistent conversational memory, or database — state is per-run JSON on disk.
- No custom/dynamically-defined action types or user-defined workflow language — the stage
  set, their order and the policy rules are fixed in Go code; only thresholds are configured.
