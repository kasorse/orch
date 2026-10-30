# AI Model Orchestrator

A local Go CLI that takes one engineering task, decides which workflow stages it needs, and
executes that workflow by shelling out to locally-installed AI CLIs (`claude`, `codex`,
`gemini`) against your current git repository — running real tests and persisting run state
to disk. The full spec is `MVP.md` (the decision layer in detail: `specs/JEV integration/`);
`CLAUDE.md` summarizes the invariants that guide the implementation.

A **decision provider** — the [Jev](https://docs.typesafe.ai) decision API, or one of the
CLIs — only estimates probabilities: does the task need repository context, how complex is
it, whether the task is clear enough to act on, how likely is each stage (research,
investigation, development, testing, review) needed. Everything chosen from those numbers is
Go code: configurable thresholds pick the complexity and the stages, the stages always run in
the same order, and a deterministic state machine drives them. The AI models only do reasoning
(research, investigation, implementation, review) — they never decide what happens next. A task
that is not clear is first made clear by one more call that has no tools and does not read the
code (see [Clarifying an unclear task](#clarifying-an-unclear-task)). Test pass/fail is decided only
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

clarify:                     # required: a route, or `clarify: {enabled: false}` written out
  enabled: true              # false: no clarify step (the clarity question is still asked and recorded)
  provider: claude
  model: sonnet
  timeout: 120s
  docs: ["**/*.md"]          # globs over the files git lists; the deny list applies first
  max_bytes: 16384           # rules + excerpts sent with the task; 1024..131072
  max_rounds: 2              # additions the operator may make after a re-check; 0..5

policy:
  context_depth:
    project_threshold: 0.5
  clarify:
    threshold: 0.80          # P(clear) >= threshold -> the task is clear enough
  complexity:
    cuts: {medium: 1.24, complex: 1.48}
  stages:
    thresholds: {research: 0.81, development: 0.81}
    force:
      medium: [investigation, review]
      complex: [investigation, review]

context:
  max_files: 20
  max_bytes: 24576
  extra_deny: []

actions:
  research:    { provider: claude, model: sonnet }
  investigate: { provider: claude, model: sonnet }
  implement:   { provider: claude, model: opus }
  review:      { provider: claude, model: sonnet, timeout: 20m }

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
  protected_paths: [".claude/**", ".mcp.json", ".github/workflows/**", ".gitlab-ci.yml", ".envrc", ".vscode/**"]
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
- **`clarify`** (required) routes the one call that makes an unclear task clear, see
  [Clarifying an unclear task](#clarifying-an-unclear-task). `enabled: false` turns the step
  off (the benchmark does); `provider`, `model`, `effort` and `timeout` route it like an
  action; `docs` (globs over the files git lists, default `["**/*.md"]`) and `max_bytes`
  (1024..131072, default 16384) bound the repository's rules and documentation excerpts it
  gets; `max_rounds` (0..5, default 2) is how many additions you may make after a re-check.
  The provider must be able to enforce authority `none`.
- **`policy`** turns probabilities into decisions (`specs/decision-shape/`, `specs/clarify/`):
  - `clarify.threshold` (in (0, 1], required): the task is clear enough when the decision
    provider's P(clear) is at or above it. 0.80 is not calibrated: `decisions calibrate`
    shows the distribution and how many runs stopped `task needs input`;
  - `project_threshold` for collecting project context;
  - `complexity.cuts` on size = P(medium) + 2·P(complex), from 0 to 2: size at or above
    `complex` is complex, at or above `medium` medium, else simple. The example's cuts are the
    50/35/15 quantiles of past decisions of one decision model on one repository's tasks;
    take yours from `decisions calibrate`;
  - `stages.thresholds` for the two stages the decision provider is asked about:
    development (are files changed; without it the run only answers) and research (is outside
    knowledge needed);
  - `stages.force`, the template: the stages on at each complexity. Testing always comes
    with development; without development, testing and review are off.

  If nothing qualifies, the workflow is `investigation`. The same values apply to every
  decision provider. `analysis` and `planning` are not stages any more: a config that still
  names them (a threshold, the template, `policy.explore`, `actions.analyze`, `actions.plan`)
  is refused with the replacement and a pointer to `specs/clarify/`.
- **`context`** bounds the project context sent to the decision provider; `extra_deny`
  adds glob patterns that are never read.
- Each entry under **`actions`** independently chooses a `provider` (`claude` | `codex` |
  `gemini`) and a `model` string passed straight through to that provider's CLI — there's no
  automatic "best model" selection, fallback, or benchmarking. `timeout` optionally tightens
  that action's share of `execution.timeout`. A provider must be able to enforce the
  action's authority: Codex supports the `read` and `edit` workflow routes; its `none` and
  `web` routes remain unavailable. Gemini is still refused until its permission flags are
  verified.
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
- **`security`** never widens what a step may do. `protected_paths` is required: the whole
  list of patterns `implement` must not change (nothing is built in; `[]` protects nothing;
  copy the list above). A pattern matches at any depth, or only from the repository root with
  a leading `/` (`/.gitlab-ci.yml` leaves `examples/ci/.gitlab-ci.yml` alone).
  `require_clean_worktree` (default `true`) makes a workflow with `implement` refuse to start
  on a dirty worktree; `allow_unverified_cli` lets a
  run go on with a provider CLI outside the verified version range (warned and recorded);
  `secret_scan` decides what happens when a step adds what looks like a secret. See
  [Security model](#security-model).
- **`jira`** (optional) is where `run --jira` reads a ticket from. It is validated whenever
  present, but its credential variables are needed only by `run --jira` (and checked by
  `validate`). See [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket).

### Getting more from the same stages (opt-in)

Every stage already gets what the earlier ones found (the investigation's files and risks, its
acceptance criteria, the clarification agreed before the run, and on a retry the diff stat of what the run changed so far), and
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
  Checks only run in a workflow with a `testing` stage, which every workflow with development
  has.
- **`verify`** is described under [Verify](#verify-let-implement-run-the-tests-opt-in); the
  `${ORCH_TEST_FILTER:+…}` in `test.command` lets the model run a subset of the tests, and a
  command that does not use it simply runs everything.
- **`by_complexity`** is a static table: Go reads the complexity the decision already made and
  picks the entry; nothing is chosen by a model, and the provider cannot be overridden. With
  **`escalate: true`** (implement only) a fix after a failed test or review runs on the entry
  one level up (simple → medium → complex), recorded as `model_source: escalated.<level>`. An
  entry can name its own fix route, `complex: {model: sonnet, effort: high, escalate: {model:
  opus}}`, which wins for its level (complex included), needs no `escalate: true` and is
  recorded as `model_source: by_complexity.<level>.escalate`.
- **`review.fix_medium`** and **`actions.review.models`** make review stricter; the second
  costs one more call per extra model.
- Nothing here changes what decides success: the `test` step's exit code and, for review,
  `ReviewResult.Decide`. Details: `MVP.md` §44 and `specs/quality/`.

## Governance

Controls for teams (`MVP.md` §45, design record in `specs/governance/`). All of it is
optional and commented out in `orchestrator.example.yaml`; the org file has its own example,
`org.example.yaml`.

- **Path rules.** `policy.paths` asks for more care where a change lands, for example
  `match: "migrations/**"`, `require_stages: [review, testing]`, `approval: manual`. They are
  checked after each implement on the files git says the run changed, never on a model's list.
  If the workflow lacks a required stage, the step fails (a new run with `--stage` adds it);
  the workflow is never rebuilt mid-run. `approval: manual` keeps `auto_commit` from
  committing and adds the rule to the commit question; `outcome.approval_required` in
  `run.json` tells a CI job. To forbid a path outright use `security.protected_paths`.
- **Budget.** `limits.max_cost_usd` stops a run before the next step once its steps have cost
  that much (status `stopped`, exit code 4); a resume counts what was spent and continues once
  you raise it. A step can overshoot by its own cost; the decision phase is not counted, except
the clarify call and its re-checks.
  Only providers that report a call's cost (claude) can be routed with a budget, or validation
  refuses it.
- **Org layer.** `/etc/orchestrator/org.yaml` (or the file `ORCHESTRATOR_ORG_CONFIG` names,
  only when the `/etc` one does not exist) sets floors, ceilings and additions that a
  repository's `orchestrator.yaml` can only tighten, plus an `allowed_models` list. A weaker
  repository setting or an unreadable org file is a config error before any call; `run.json`
  records the file's path and sha256.
- **Calibration.** `orchestrator decisions calibrate` proposes `testing` and `review`
  thresholds from your history (see [Usage](#usage)). It never writes config. The opt-in
  `policy.explore` (`band`, `rate`) turns a stage on in a share of runs just below its
  threshold, so lowering a threshold has data behind it; it never turns a stage off.

On a developer's own machine these rules hold only while the developer cooperates: they own
the machine and the files, and root can delete the org file. They are a real boundary where
the operator controls the machine, such as CI or a managed host.

## Security model

Every provider call gets the least tools its job needs, chosen by a fixed table in Go code —
never by the model, a provider default or config. **No step has a shell.** A provider that
cannot enforce an authority is refused before any AI call, and nothing ever runs a CLI with
its permission checks turned off.

| call | authority | may | may not |
|---|---|---|---|
| translation, decision, clarify | `none` | answer from the prompt | use any tool |
| `research` | `web` | search and fetch web pages | read any file |
| `investigate`, `review` | `read` | read and search the repository | write, run commands, use the network |
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
  `credentials*` and `context.extra_deny` — the same list the Context Manager
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
  file changed — the patterns of `security.protected_paths` and the config file when it lives
  in the repository (even if git-ignored) — and that `.git/config` and the git hooks are
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
      args: ["{source}/graphify-out/graph.json"] # {source} = your repository, {repo} = where the step works (a worktree run's worktree)
      tools: [query_graph, get_node, get_neighbors, shortest_path]   # or ["*"]
      actions: [investigate, implement, review]                      # default: investigate, review
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
- `test.verify_command` (optional) is what `run_tests` runs instead of `test.command`, e.g. a
  script that tests only what the run changed since `$ORCH_BASE_REF` (this repository's
  `scripts/check-changed.sh`). The `test` step always runs `test.command`.
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
   repository, and how clear the request is (P(clear)); above `project_threshold`, project
   context is collected.
3. **Clarify.** If P(clear) is under `policy.clarify.threshold`, the task is made clear before
   anything else is decided, see [Clarifying an unclear task](#clarifying-an-unclear-task).
4. **Project context.** A compact summary — never whole files: language and dependencies
   from the manifest, test layout, bullet rules from `AGENTS.md`/`CLAUDE.md`/
   `CONTRIBUTING.md`, matching README/docs sections, the files relevant to the request with
   one-line summaries, git branch and changed paths.
5. **Classification.** Complexity distribution(s) and the probabilities of the two asked
   stages (development, research).
6. **Policy.** The cuts on size pick the complexity; its template, the asked stages over
   their thresholds and testing with development make the stages.
7. **Workflow.** `research → investigate → implement → test → review`, filtered to the
   enabled stages. A workflow without `implement` ends by printing the answer written by its
   last read-only step.

### Clarifying an unclear task

The pre-check also asks whether the request is clear enough to act on: is it stated what has to
be achieved, where, and how to tell that it is done. Go compares the answer, P(clear), with
`policy.clarify.threshold`. A clear task goes on at no extra cost.

Under the threshold (and with `clarify.enabled: true`) one more call runs before the decision:
the clarify step. It is routed by the `clarify` section, has authority `none` — no tools, so it
cannot open a file — and gets the request plus the repository's *instructions*, chosen by Go: the
bullet rules of `AGENTS.md`, `CLAUDE.md` and `CONTRIBUTING.md` and the Markdown sections of the
files `clarify.docs` matches (ranked by the request's words, cut to `clarify.max_bytes`; the deny
list is applied before anything is read). It lists what is unclear, with a question for each
point and, where the documentation says so, the answer and its source. The decision provider never
sees the instructions: it is asked again about the request followed by the clarification (the
re-check).

- **At a terminal** (not `--mode auto`) you see each point with the proposed answer: Enter keeps it,
  text replaces it, `-` leaves the point open. If the re-check still rates the task unclear you
  may add text to it, up to `clarify.max_rounds` times; an empty addition stops the run.
- **`--mode auto`** takes the answers the documentation supports and the model's stated
  assumptions, and leaves the rest open.
- If the task is still unclear after the re-check (in auto mode, or when the rounds are spent),
  the run stops with `task needs input` and exit code 4. The console and `summary.md` list the open
  points and the last P(clear) against the threshold. There is nothing to resume: change the task
  and start a new run.
- What was agreed is `task.clarification` in `run.json` and is given to the stages after the
  task, as the clarification. The stages still get your original request first. A failed
  clarify call fails the run (an unclear task never goes on unclarified).
- `run --stage` runs the same step; the stages stay yours. A story task (`run --story-task`)
  skips it: the task file is the plan. `clarify: {enabled: false}` turns the step off, and the
  run goes on whatever P(clear) is; the question is still asked and recorded in `decision.json`.

`policy.clarify.threshold` is not calibrated; `decisions calibrate` shows the P(clear)
distribution and the share of runs that stopped `task needs input`, and proposes nothing.

## What is sent to Jev

With `decision.provider: jev`, two requests per run go to `decision.jev.base_url`
(`https://api.typesafe.ai` by default):

- the request text — the English translation when one was made; after a clarify step, the
  request followed by the clarification (the points, their answers and your additions — never
  the instruction files themselves), once more per re-check;
- when project context is needed, the context described above: paths, one-line summaries,
  rules, doc excerpts, dependency names, test layout, git branch and changed paths.

Never sent: file contents beyond those summaries, anything matching the deny list
(`.env`, `.env.*`, `*.pem`, `*.key`, `credentials*`, plus `context.extra_deny` —
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
your credentials in the `Authorization` header. Nothing is written to Jira by `run`,
`resume` or `story plan`; the only write is `story run` creating a story's child issues, and
only with `jira.create` configured (see [Planning and running a story](#planning-and-running-a-story)).
The request goes to `jira.base_url` only — never to a URL you pasted, and never through a
redirect — over `https` (plain `http` is accepted only for `localhost`/`127.0.0.1`/`::1`).

## Running claude on an API key

Every `claude` call (steps, translation, an LLM decision provider) uses one credential per
process: `api` (a Claude Console API key, billed per token) or `login` (whatever the CLI is
logged in with: a claude.ai subscription, `CLAUDE_CODE_OAUTH_TOKEN`, a cloud provider). The
mode comes from `--claude-auth auto|api|login`, then `providers.claude.auth`, then `auto`
(the key variable is set → `api`, otherwise `login`).

```bash
export ANTHROPIC_API_KEY=...             # the key goes in the environment, never in the config
orchestrator --claude-auth api run "<task>"
orchestrator --claude-auth login run "<task>"   # ignore the key, use the CLI's own login
orchestrator --claude-auth api resume <run-id>  # finish a run that hit the subscription limit
```

```yaml
providers:
  claude:
    auth: auto                      # auto | api | login
    api_key_env: ANTHROPIC_API_KEY  # name of the variable that holds the key
```

- The console prints the resolved mode (`claude auth: api key (ANTHROPIC_API_KEY, auto)`), and
  so does `validate`; `run.json` records it, and `summary.md` shows it. `api` with no usable
  key is a config error before any call.
- In `api` the orchestrator sets the key and removes the variables that would outrank it
  (`ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_USE_BEDROCK`/`_VERTEX`/`_FOUNDRY`,
  `CLAUDE_CODE_OAUTH_TOKEN`). After each call it checks the `apiKeySource` the CLI reported; if
  the CLI did not use the key, or the call fails on authentication or billing, the run stops
  (exit 2) and never falls back to another mode. A key from `claude setup-token`
  (`sk-ant-oat…`) belongs in `CLAUDE_CODE_OAUTH_TOKEN` with `--claude-auth login`.
- A literal `api_key` in the config is rejected. `resume` decides the mode again from its own
  flag and environment. Details: MVP.md §46.
- If the key goes to a gateway that serves other model names than your login, put the
  gateway's variables in `providers.claude.api_env` instead of exporting them. They are set
  for claude in `api` and removed in `login`, so the same config works in both modes and a
  shell export cannot pull a subscription call onto the gateway:

  ```yaml
  providers:
    claude:
      api_env:
        ANTHROPIC_BASE_URL: https://ai-gateway.example.internal
        AWS_PROFILE: claude-bedrock
        ANTHROPIC_DEFAULT_OPUS_MODEL: bedrock/eu.anthropic.claude-opus-5-5
  ```

  Routes keep `opus`/`sonnet`. The console prints `claude env (api): …` with the gateway's host
  and no other value, and `summary.md` adds `, env: N variables`. No secret goes there:
  credential names, a URL with a user or password, and values that look like a secret are
  config errors. Only claude gets these variables; the test command does not.

## Running Codex on a subscription or API key

Codex uses `codex exec` with the same `auto`, `api` and `login` choice. In API mode the
configured key is passed to the child as `CODEX_API_KEY`; login mode uses the Codex CLI's
saved ChatGPT login.

```bash
export OPENAI_API_KEY=...
orchestrator --codex-auth api run "<task>"
orchestrator --codex-auth login run "<task>"
```

```yaml
providers:
  codex:
    auth: auto
    api_key_env: OPENAI_API_KEY
```

`--codex-auth` overrides `providers.codex.auth`. The key is read from the environment,
never from YAML or argv, and the resolved mode is recorded without the key. Codex read and
edit actions use its `read-only` and `workspace-write` sandboxes. A direct API client is a
separate TODO item.

## Usage

```
orchestrator help                    # also --help or -h: the commands, run modes and every flag
orchestrator [--config path] version
orchestrator [--config path] [--claude-auth m] [--codex-auth m] validate
orchestrator [--config path] [--claude-auth m] [--codex-auth m] run [--verbose|-v] "<task description>"
orchestrator [--config path] run --jira <KEY|URL> ["notes"]
orchestrator [--config path] run --stage <name> [--stage <name> ...] "<task>"
orchestrator [--config path] run [--inplace|--worktree] [--output json] "<task>"
orchestrator [--config path] [--mode m] [--output json] review --base <ref> [--head <ref>] [--task "<text>" | --jira <KEY|URL>]
orchestrator [--config path] story plan "<request>" [--jira <KEY|URL>] [--repo <name>]...
orchestrator [--config path] [--mode m] story run <spec_dir>/<slug> [--retry <ID>] [--parallel <N>]
orchestrator [--config path] story status <spec_dir>/<slug>
orchestrator [--config path] [--mode m] [--claude-auth m] [--codex-auth m] resume <run-id>
orchestrator runs report [--dir <path>]
orchestrator runs clean [--older-than <N>d | --unreadable] [--dry-run]
orchestrator decisions report
orchestrator decisions calibrate [--runs <dir>] [--min-runs N] [--size-target S/M/C] [--simple-max-hard F]
```

- `help`, `--help`, `-h` — prints the commands, the run modes and every flag with what it does,
  and runs nothing (`-h` anywhere on the line, even after a command).
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
- `run --inplace` / `--worktree` — where a workflow with `implement` runs. A worktree is the
  default; see [Taking the result from its branch](#taking-the-result-from-its-branch-worktree-runs).
- `review --base <ref> [--head <ref>]` — reviews a change somebody else wrote. See
  [Reviewing a change](#reviewing-a-change-orchestrator-review).
- `--output json` (`run`, `resume`, `review`) — one JSON line on stdout when the process exits,
  everything else on stderr. See [Exit codes and CI](#exit-codes-and-ci).
- `story plan`, `story run`, `story status` — split a task that spans services into a spec and
  ordered tasks, and run them. See [Planning and running a story](#planning-and-running-a-story).
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
- `runs clean [--older-than <N>d] [--dry-run]` — removes the git worktrees of runs older than
  the limit (default 7 days, by the run's `created_at`) whose worktree is still there. A run in
  use is skipped; a worktree git refuses to remove (uncommitted files) is kept and listed with
  git's reason. Never `--force`, and no branch is deleted: the run's branch stays.
  `--dry-run` lists what would be removed and changes nothing.
- `runs clean --unreadable [--dry-run]` — removes the run directories this version cannot read
  (a run started before a layout change, a damaged `run.json`), whatever their age, with their
  worktree when git lets it go; locked runs are skipped and no branch is deleted.
- `decisions report` — the older name of the last part of `runs report`, kept as an alias:
  compares the two complexity questions (`choice` and `score`) over
  the `decision.json` of every run in `.orchestrator/runs/`: agreement rate, a 3×3 matrix,
  mean confidence, and the runs where they disagree. Manual (`--stage`) runs, runs chosen by
  hand, mock answers and runs without the shadow question are skipped. It needs no config and
  calls nothing. Until there is Jev access, `decision.provider: claude` collects real data.
  Deciders are told apart by the `policy_hash` every decision records (the first 12 digits are
  printed; `—` for a run written before it existed), so runs before and after a threshold
  change are not mixed.
- `decisions calibrate [--runs <dir>] [--min-runs N] [--size-target S/M/C] [--simple-max-hard F]` — proposes `testing` and `review`
  thresholds from your run history (`.orchestrator/runs/`, or every run under `<dir>`). Runs
  are grouped by decision provider, model and `policy_hash`; for each group it prints the
  runs where the stage ran in probability buckets of 0.1 with the share the stage paid off in
  (testing: a test failed after an implement; review: a failing verdict followed by an
  implement), then comparison tables for research and investigation and a table per
  complexity and model (runs, completed share, mean `fix_iterations`, mean cost), and ends
  with a YAML diff of `policy.stages.thresholds` with the rows behind each line. A threshold
  is raised to the lowest bucket edge above it such that the stage paid off in none of at
  least `--min-runs` (default 10) runs below it; a bucket with fewer runs prints `not enough
  data`. Testing and review have probabilities only in decisions made before
  `specs/decision-shape`. A second section, per decision model and questions hash, shows how
  the decisions' sizes spread, which stage answers only repeat the size, the cuts for a target
  share (`--size-target`, default 50/35/15) and the share of runs that needed a fix per size;
  it ends with a YAML diff of `policy.complexity.cuts`, the medium cut lowered when the
  simple level would hold more than `--simple-max-hard` (default 0.2) hard runs. A third
  section shows the `P(clear)` of the first check per decision model and questions hash, how
  the checks ended, and the share of runs that stopped `task needs input`; it proposes no
  clarity threshold. Nothing is written; you apply the diff by hand. Manual (`--stage`) runs, runs chosen by hand and mock
  answers are skipped. It needs no config and calls nothing.
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
`--mode auto` but not `--mode manual` (which still asks), after the same checks. It does not commit when there is a warning (a failing
review, a likely secret) or no `summary_short` to use as the message, nor when the change
matched a `policy.paths` rule with `approval: manual`; it says why and the changes stay in
the working tree. Interactively, the question lists those rules and their files first.

### Taking the result from its branch (worktree runs)

A workflow with `implement` runs by default in its own git worktree under
`$XDG_STATE_HOME/orchestrator/worktrees/` (`~/.local/state/...`), on a branch that starts at
your current HEAD. `orchestrator run --inplace` (or `execution.isolation: inplace`) runs in your
working tree instead, with the commit question above and no push; `run --worktree` overrides an
`inplace` config. Your working tree, index and branch are never touched by a worktree run, so
you can keep working (uncommitted changes are not part of the run; the run warns when there are
some).

The branch is named when the worktree is created: by what your CLAUDE.md, AGENTS.md or
CONTRIBUTING.md say about branch names when `worktree.branch.from_instructions` is set, else by
`worktree.branch.template` (`{id}`, `{key}`, `{slug}`, `{date}`), else `orchestrator/<run-id>`.

When the run completes it ends with a chain: **commit** on that branch, **push** of the branch to
`delivery.remote` (default `origin`), a **merge request** to the branch the run started from
(`delivery.target_branch` when HEAD is detached), and the worktree is removed. The push itself
opens the merge request, with GitLab's push options (title: the commit header, description:
`report.md`): the orchestrator holds no GitLab token and has no API client, and your own git
credentials do the push. With `--mode auto` nothing is asked (the commit still follows the
`auto_commit` rules: not with a warning, a missing `summary_short` or a `policy.paths` rule with
`approval: manual`); in every other mode you are asked before each step, the default is no and a
no stops the chain:

```
Commit to feature/PAY-12-use-plus-in-add? [y]es / [N]o / [e]dit message: y
Push feature/PAY-12-use-plus-in-add to origin? [y]es / [N]o: y
Open a merge request feature/PAY-12-use-plus-in-add → master? [y]es / [N]o: y
Committed 3f2a9c1 to feature/PAY-12-use-plus-in-add: fix: PAY-12 Use + in Add
Merge request !12: https://gitlab.example.com/group/app/-/merge_requests/12
```

The commit message is written by Go: a Conventional Commit type the model proposed from a closed
list, the Jira key, the `summary_short`, and the `Orchestrator-Run` and `Orchestrator-Decision`
trailers. `report.md` in the run directory (status, passed stages, tests, review, what needs
attention) is the merge request description, titled `<KEY>: <summary>` for a Jira task, followed
by the project's `.gitlab/merge_request_templates/Default.md` from the base commit (its
`/assign_reviewer` line sets the reviewers); it escapes everything a model or a ticket wrote. The push is not repeated and never forced, and a branch that already exists on the
remote is never pushed to. If the push or the merge request fails, or `--mode auto` did not
commit, the run says why, keeps the branch (and the worktree when there is no commit), prints
the command to finish by hand and exits 6. A remote that is not GitLab fails at the push (push
options). Before the worktree is created the run checks that the remote exists, a target is known
and `security.secret_scan.mode` is not `off` (a push publishes the tree); the message names
`--inplace` as the way out. A story whose config says `story: {push: false}` ends each task at the
commit on its branch: no push, no merge request, no remote needed, exit 0, and the command to push
it printed; the story stacks its tasks on the local branches and, once every task is done, lands the
result on the branch it was started from (a fast-forward, or a merge Go makes; git refuses rather
than overwrite your local changes, and the story then exits 6 until a later `story run` lands it),
committing the story's README and As built there when the spec lives in that repository.

If your branch moved on and the merge would conflict, the run says so and lists the files
(`git merge-tree`, git ≥ 2.38); you resolve it with git. A failed or stopped run keeps its
worktree for `resume`, which continues there. `worktree.setup` (for example `npm ci`) runs once
in a fresh worktree before any step, in the test sandbox but with the network. Old worktrees are removed with
`orchestrator runs clean` (branches are never deleted). With `jira.write` (`comment`,
`transition`) a run of a Jira task comments on the issue and moves it after the merge request
was opened; a failure there is a warning. See `MVP.md` §47 and §50.

### Reviewing a change (`orchestrator review`)

```
orchestrator review --base main                       # main...HEAD
orchestrator review --base v1.3.0 --head feature/x --mode auto
orchestrator review --base main --task "the new cache must not change the API"
orchestrator review --base main --jira PAY-12         # the ticket is the task
```

Reviews the change `merge-base(base, head)..head`, like a merge request, without implementing
anything: a run of `test` (your checks and `test.command`, sandboxed) and `review` in a detached
worktree at `head`, so your working tree may be dirty and is never touched. The verdict and
issues are printed and `report.md` is written; a failing verdict exits 6. Without `--task` or
`--jira`, the task is written from the commits' subjects.

### Exit codes and CI

| code | meaning |
|---|---|
| 0 | completed, and what was asked was delivered |
| 1 | a run that cannot be used, an unexpected error |
| 2 | configuration, dependency, usage, credentials, the delivery checks |
| 3 | a provider failed |
| 4 | the workflow failed or a limit stopped it (a red test, budget, no progress, `task needs input`) |
| 5 | cancelled |
| 6 | completed, but needs a person: no merge request was opened (and nobody said no), or `review` failed |

`run`, `resume` and `review` without a terminal need `--mode auto` (or `plan`, `accept-edits`)
and refuse at once otherwise. With `--output json` stdout carries exactly one line
(`run_id`, `status`, `exit_code`, `branch`, `commit`, `mr`, `review_verdict`, `cost_usd`,
`report`, …) and everything else goes to stderr:

```
orchestrator run --jira "$JIRA_KEY" --mode auto --output json > result.json
orchestrator review --base "$CI_MERGE_REQUEST_DIFF_BASE_SHA" --head "$CI_COMMIT_SHA" --mode auto --output json
```

The `Dockerfile` in the repository root builds an image with git, bubblewrap and a pinned claude
CLI (no image is published), and [`examples/ci/`](examples/ci/) has a `.gitlab-ci.yml` with an
implement job and a review job; in CI use `--claude-auth api`. See `MVP.md` §4 and §50.10.

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
orchestrator run --stage investigation "investigate the cache"
orchestrator run --stage review --jira PMC-2789
```

Use it when you already know what you need: review code you changed by hand, just run the
tests, investigate without implementing. Names are `research`, `investigation`,
`development`, `testing`, `review`, plus the aliases `investigate`, `implement`, `test`. The
old names `analysis`, `analyze`, `planning` and `plan` are refused with a message pointing to
`specs/clarify/`.
Repeat `--stage` for several; one stage per flag. The order is always the fixed one, whatever
order you give them in, and the retry rules, permissions, test command and resume are the same
as for a decided run. `decision.json` records `source: manual`.

* A stage whose usual input you skipped (development without an investigation) prints a
  warning and still runs. Testing and review never warn.
* The clarity question is asked and, when the task is unclear, the clarify step runs before the
  stages (see [Clarifying an unclear task](#clarifying-an-unclear-task)); `decision.json` still
  records `source: manual`.
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

### Planning and running a story

Some tasks are not one change: they span several services, or are safe only as a sequence of
steps that each leave the system working. A *story* splits such a task into a specification
and ordered tasks; each task is an ordinary run in its own repository.

```yaml
story:
  roots: [..]               # where the service checkouts live, side by side
  max_services: 5
  spec_dir: specs           # in the repository you run the commands in (the story home)
  max_tasks: 12
  max_cost_usd: 20          # optional; the whole story
  max_parallel: 1           # 1..8; ready tasks of different services run at once; --parallel N overrides it
  jira_issue_types: [Epic]  # run --jira on these refuses and points to story plan
```

1. **Plan.** Name the services in the task, e.g. `orchestrator story plan "Partial refunds:
   billing stores the amount, gateway accepts it"`, or take the task from a ticket with
   `story plan --jira PAY-1` (the ticket's components name services too); `--repo NAME` names
   them explicitly. Go finds the checkouts under `story.roots` (no model call), one read-only
   call surveys each of them, and one tool-less call proposes the tasks. Go checks the answer
   and writes `specs/<slug>/` (`README.md`, `SPEC.md`, `tasks/TASK-<ID>-….md`). If the answer
   is a single task, nothing is written and the command that runs it is printed.
2. **Review.** The spec is yours: read it, edit the tasks (their `## Acceptance` items become
   review criteria), commit it. Each service keeps its own `orchestrator.yaml`, which governs
   its tasks.
3. **Run.** `orchestrator story run specs/<slug>` is the approval. Go orders the tasks from
   their `depends_on`, shows the plan and asks once at a terminal; then each task runs as
   `orchestrator run` in its repository, in a worktree on `story/<slug>/<ID>`, stacked on the
   branch of the same-repository task it needs, or, when it needs several tasks of one
   repository, on `story/<slug>/spec`, the story's own branch (Go creates it in each
   repository at the first start), where Go merges their commits first. Your checkouts and
   branches are not touched. `--parallel N` (or `story.max_parallel`, 1 to 8, default 1) runs up
   to N ready tasks at once, each its own run in its own worktree; with more than one, each
   task's output goes to `.orchestrator/stories/<slug>/logs/task-<ID>.<attempt>.log` and the
   console gets one line per start and end. Only tasks of different services run at once: a ready
   task waits while another task of its service runs, and the slot goes to the next service. When
   a task is done Go appends `## As built` to its file and updates the spec's `README.md`. A
   failure stops the story after the tasks already running end; run `story run` again to resume
   it, or `--retry <ID>` to start a failed task over on a new branch. For each task that did not
   pass, the output names the step, the reason and the red test or failed review that led to it,
   and one line lists the ready tasks the stop kept from starting. A task whose run completed
   without a commit (a failing review, a secret warning) blocks the story until you commit in
   its worktree or drop the work. A task whose parents do not merge cleanly on the spec branch
   is `conflict`: Go lists the files and the story blocks until you merge the parents' branches
   on `story/<slug>/spec` yourself (in a worktree of it) and run `story run` again.
   `story status specs/<slug>` prints the table.
4. **Merge.** Each task's branch is pushed and opens one merge request (the children run under
   `--mode auto`, so `story run`'s confirmation is your yes), targeting the branch of the task it
   needs, or `story/<slug>/spec`, which `story run` pushes. A branch is named by its repository's
   rules (`worktree.branch`), `story/<slug>/<ID>` only the fallback. Merge in order, rebasing the
   stack yourself after a review; `story status` shows each merge request.

With `jira.create` (`issue_type`, `link: parent|field`, `labels`, `components`) and a story
planned from a Jira issue, `story run` creates one child issue per task after you approve,
once: a request that may have reached Jira is never repeated, and an interrupted creation
stops the next start until you set `jira: <KEY>` (or `none`) in the task file. Details:
`MVP.md` §48 and `specs/story/`.

### Running the Jira listener (`orchestrator jira listen`)

The listener takes the Jira tickets labelled `ai-run`, one at a time, and runs each as an
ordinary worktree run in its service's checkout (MVP.md §51, design record
`specs/jira-listener/`). Add the `listener` and `gitlab` sections to its configuration file
(`orchestrator.example.yaml` has them, commented), export `JIRA_EMAIL`, `JIRA_API_TOKEN`,
`GITLAB_TOKEN` and the provider's credential, and start it in a working directory:

```bash
orchestrator jira listen --config orchestrator.yaml        # poll until SIGTERM
orchestrator jira listen --config orchestrator.yaml --once # one poll and its ticket, then exit
orchestrator jira listen --claude-auth login               # every child run uses the CLI's login
```

A ticket names its service with a label `service:billing` (or a component of that name) and, when
the checkout is not there yet, its GitLab project with `gitlab:backend/billing`. The reporter
must be one of `listener.query.reporters`; the label alone starts nothing. The outcome is a
comment starting `[orchestrator]` and a transition: merge request opened → `done`; an unclear
task, an unknown service or a ticket that is too long → `blocked` (answer in a comment and move
the ticket back to the queue's status); a red run → `failed`; a provider or network failure →
`retry`, without spending one of `max_attempts`.

In Docker:

* **The working directory is a volume.** The clones (`listener.services_dir`) and
  `.orchestrator/listener/` (the state, the lock, the task files) must survive a restart; that is
  how a ticket interrupted by `docker stop` is resumed. Run one listener per volume.
* **Give the stop timeout more than the child's wait delay** (`docker stop --time` or
  `stop_grace_period`): on SIGTERM the listener sends SIGINT to the running child and waits for
  it to stop; a SIGKILL before that leaves the ticket in the start status for the next start.
* **Check the Jira workflow first.** The `start` transition must be allowed from the status in
  `query.statuses` (it is the only lock: a second listener's claim fails), and `start`,
  `blocked`, `failed`, `done` and `retry` must exist as transitions from the statuses the ticket
  can be in, with no required screen fields the REST call cannot fill.
* **A cloned repository's `orchestrator.yaml` is trusted**, test command and `worktree.setup`
  included, because its group is in `gitlab.allowed_groups`. Allow only groups whose members you
  trust to run code on this machine. A repository without the file runs under
  `listener.default_config`.
* The GitLab token reaches only git, through a credential helper the listener configures for
  `gitlab.base_url`; it is in no URL, `.git/config`, log or comment, and not in the provider
  CLIs' or the test command's environment. The Jira variables are not passed to the child runs.

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
                        # commit (the SHA, when you approved the commit at the end),
                        # worktree (path, branch, base_ref, conflicts, removed) and
                        # options.isolation for a worktree run;
                        # per step: usage (tokens, cost, partial), call (prompt size and
                        # cuts, models, API time, tool calls), an implement's diff;
                        # outcome (status, fix iterations, last test and review result,
                        # wall and active time, cost, decision overhead, net change)
                        # branch_naming, mr (the push and the merge request), jira_write,
                        # review_target (an `orchestrator review` run), options.jira_write;
                        # per implement: diff.run_files
  summary.md            # the run's report for a reader, written when the run ends
  report.md             # the reviewer's report and the merge request description
  decision.json         # the full decision trace (see below)
  project_context.json  # exactly the context sent to the decision provider, if any
  workflow.json         # the workflow built from the decision
  ticket.json           # run --jira only: the issue as fetched (a record; never read back)
  artifacts/<NNN>-<action>.json          # structured output per step, and for a model step
                                         # the "prompt" it was sent (a record; never read back)
  logs/<NNN>-<action>.{stdout,stderr}.log  # raw provider/test output per step
  logs/translate.{stdout,stderr}.log       # the translation call, if one was made
  logs/decide-{context,classify}.{stdout,stderr}.log  # LLM decision provider calls
  logs/setup.{stdout,stderr}.log           # worktree.setup, worktree runs only
  logs/branch-name.{stdout,stderr}.log     # the branch-naming call (worktree.branch.from_instructions)
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
the root module only. What `bench/` is and how to run it: [`bench/README.md`](bench/README.md).

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
- No parallel or multi-agent workflows — one task, one sequential workflow per run. The one
  exception is a story, which can run its ready tasks at once (`story.max_parallel`,
  `story run --parallel N`), each as its own run.
- No web UI, REST API, or IDE plugin — CLI only.
- No GitHub integration, no GitLab API client or token (a GitLab merge request is opened by the
  push of the run's branch) and no remote repository execution — operates on the local working
  directory only. Jira is limited to one issue, the child issues of an approved story and the
  comment and transition after a merge request: no linked issues, attachments or JQL.
- No RAG, persistent conversational memory, or database — state is per-run JSON on disk.
- No custom/dynamically-defined action types or user-defined workflow language — the stage
  set, their order and the policy rules are fixed in Go code; only thresholds are configured.

## License

Source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE): free for
personal use, research, education and noncommercial organizations. Any commercial use needs
a separate commercial license — contact the author (orsaevkhasan@gmail.com).
