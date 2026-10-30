# Task: Implement AI Model Orchestrator MVP

Implement the MVP of a local CLI application called **AI Model Orchestrator**.

The implementation language must be **Go**.

The application is intended for software developers using AI coding assistants. A user provides one engineering task. A decision provider (the Jev decision API, or an LLM CLI) estimates how complex the task is and which workflow stages it needs; the orchestrator turns those estimates into a deterministic workflow, executes the stages with configured AI providers, runs tests, performs review, and may return the workflow back to implementation when tests or review fail.

> **Revision note.** This document was revised for the Jev integration
> (`specs/JEV integration/SPEC.md`, `specs/JEV integration/TASKS.md`): the original
> single-call classifier (`type`/`complexity`/`requires_*` → fixed workflow table) was
> replaced by the decision flow in §8–§9, the `answer` action by the `research` stage and
> a final answer from the last read-only step (§10), and the config moved to version 2
> (§5). §32–§35 describe the original delivery sequence and are kept as history.
>
> **Revision note (clarify).** `specs/clarify/` added the clarity question and the clarify step
> before the decision (§49), removed the analysis stage and the `analyze` action and renamed
> planning to investigation (`plan` to `investigate`). Where this document still names the old
> stages or actions it says so; §49 is the normative home.

Do not over-engineer the solution. Build a clean MVP with explicit interfaces, deterministic workflow logic, good testability, and minimal external dependencies.

---

## 1. Product Behavior

The user runs:

```bash
orchestrator run "Fix retry logic. HTTP 404 must not be retried"
```

The application should:

```text
validate configuration (everything, before any AI call or HTTP request)
        ↓
decide the workflow (§8):
  translate the request to English if needed
  pre-check: does classifying it need repository context? is the task clear (§49)?
  clarify an unclear task before anything else is decided (§49)
  collect compact project context if so
  classify: complexity + probability of each asked stage
  policy: thresholds → complexity and enabled stages
        ↓
build workflow (stages in fixed order)
        ↓
execute workflow steps
        ↓
run real tests
        ↓
review changes
        ↓
PASS?
 /   \
yes   no
 ↓     ↓
DONE  IMPLEMENT
        ↓
       TEST
        ↓
      REVIEW
```

The main MVP goal is to automate flows such as:

```text
investigate → implement → test → review
```

while allowing backward transitions:

```text
test FAIL
→ implement
→ test
```

and:

```text
review FAIL
→ implement
→ test
→ review
```

By default `orchestrator run` is interactive: after the decision it prints the decision
summary and the derived workflow, lets the user approve or cancel it, then lets the user
choose an execution mode (`plan`, `manual`, `accept-edits`, or `auto`) before anything
runs. Passing `--mode auto` skips all of that - this is the escape hatch for scripts and
CI. If the decision provider fails and `decision.on_failure` is `ask`, an interactive run
asks the user to choose the complexity and the stages by hand instead (§8.6). See §4 for
the flag and §27 for example transcripts.

---

# 2. Technology

Use:

```text
Language: Go
Configuration: YAML
Persistence: local filesystem
AI providers: local CLI clients
Repository: local Git repository
```

Supported AI providers:

```text
claude
codex
gemini
```

All three are accepted by the configuration and have adapters. Claude supports the full
authority table. Codex supports its read-only and workspace-write `exec` modes; it cannot
serve the tool-free or web-only routes. Gemini remains refused until its permission flags
are verified against the real CLI (§38.2).

Do not integrate directly with provider HTTP APIs. There are two exceptions, both called
with `net/http`:

* the decision provider: the Jev decision API (`POST /v1/systemone`, §8). It only returns
  probabilities; it never writes code or runs tools;
* the Jira REST API, read-only: one `GET /rest/api/2/issue/{key}` per `run --jira`, before
  the run exists (§39). Nothing is ever written to Jira.

Do not add LiteLLM, OpenRouter, a database, Web UI, IDE plugin, server mode, or distributed execution.

---

# 3. Important Architecture Principle

The responsibilities must be separated as follows:

```text
LLM
→ task reasoning
→ clarifying an unclear task (no tools, §49)
→ research, investigation
→ implementation
→ review

Decision provider (Jev or an LLM CLI)
→ bounded probabilities: does the task need project context,
  is it clear enough, how complex is it, how likely is each stage needed

Orchestrator
→ translation and decision-round invocation
→ the clarify step: choosing its instructions, the operator's questions (§49)
→ project context collection (secrets excluded)
→ policy: thresholds turn probabilities into decisions
→ workflow construction (fixed stage order)
→ provider routing
→ state transitions
→ limits
→ context/artifacts
→ test execution
→ persistence

Git
→ source of truth for code changes

Test command
→ source of truth for test success/failure
```

The orchestrator must not implement AI-provider-specific behavior inside workflow code.

---

# 4. CLI

Implement these commands:

```bash
orchestrator run "<task>"
orchestrator run --jira <KEY|URL> ["notes"]
orchestrator run --stage <name> [--stage <name> ...] "<task>"
orchestrator run --allow-red-tests "<task>"
orchestrator run --inplace "<task>"
orchestrator run --output json --mode auto "<task>"
orchestrator resume <run-id>
orchestrator review --base <ref> [--head <ref>] [--task "<text>" | --jira <KEY|URL>]
orchestrator story plan "<request>" [--jira <KEY|URL>] [--repo <name>]...
orchestrator story run <spec_dir>/<slug> [--retry <ID>] [--parallel N]
orchestrator story status <spec_dir>/<slug>
orchestrator validate
orchestrator version
orchestrator runs report [--dir <path>]
orchestrator decisions report
orchestrator decisions calibrate [--runs <dir>] [--min-runs N] [--size-target S/M/C] [--simple-max-hard F]
```

`resume` continues a persisted run where it stopped, without deciding again (§37).

`runs report` reads the run directories under `.orchestrator/runs/` (or, with `--dir <path>`,
every directory holding a `run.json` anywhere under `<path>`, so it also reports the run
directories a benchmark collected, `bench/README.md`) and prints: the runs by status; cost,
success and the share of cost spent deciding, by complexity and by stage set (from `outcome`
and `decision.json`, §21; both are recomputed for runs written before they were recorded);
the unreadable run directories; then the complexity comparison of `decisions report`. It
needs no configuration and calls nothing, and nothing decides anything from it.

`decisions report` is the older name, kept as an alias that prints only the last part: it
reads the `decision.json` of every run under `.orchestrator/runs/` and
compares the two complexity questions (§8.2): how often `choice` and `score` select the
same level, the 3×3 matrix of their selections, each one's mean confidence, and the runs
where they disagree. It skips manual (`--stage`) runs, runs chosen by hand after a provider
failure, mock answers and runs without the shadow question, and lists unreadable traces.
It needs no configuration and calls nothing. It is the input for choosing one question
(`specs/JEV integration/TASKS.md` T21). Every `decision.json` of a provider or user decision
records `policy_hash`, the sha256 of the `DecisionPolicy` it used (absent for `--stage`), and
`decisions report` counts deciders per hash.

`decisions calibrate [--runs <dir>] [--min-runs N] [--size-target S/M/C] [--simple-max-hard F]` reads the same runs, read-only, and
proposes testing and review thresholds and `policy.complexity.cuts` with the evidence behind
them; the design records are `specs/governance/` §7 and `specs/decision-shape/` §7.

`run --jira` takes the task from one Jira issue: `--jira PMC-2789`, `--jira=pmc-2789` or
the issue's `/browse/<KEY>` URL, in any position. Any positional text becomes operator
notes appended to the ticket. `--jira` is valid only with `run`, and giving it twice or
empty is a usage error (exit 2). Details in §39.

`run --stage` names the stages to run and skips the decision layer. It is repeatable, valid
only with `run`, and combines with `--jira`. Details in §40.

`run --allow-red-tests` is for a task that is about making failing tests pass: the tests
already failing before any change do not stop the run (§41.5). Valid only with `run`; it
combines with `--stage` and `--jira`.

`story run --parallel N` (1..8; a usage error otherwise, and with any other command) is how many
ready tasks of a story run at once; it overrides `story.max_parallel`. Details in §48.6.

`run --inplace` runs one run in the operator's working tree; `run --worktree` overrides an
`inplace` config. A workflow with `implement` is a worktree run by default (§47.1) and ends with
a commit, a push and a merge request (§50.5). Given together the two flags are a usage error.

`--output json` (valid with `run`, `resume` and `review`) sends every human-readable line to
stderr and prints exactly one JSON line on stdout when the process exits (§50.10). `review`
reviews the change `base...head` of a repository without implementing anything (§50.11). The
`run`, `resume` and `review` commands refuse to start without a terminal unless `--mode`
names a mode that asks nothing (§50.10).

### Exit codes

The exit code is part of the interface, for a CI job as much as for a person (§24, §50.10):

| code | meaning |
|---|---|
| 0 | completed, and what was asked was delivered |
| 1 | a run that cannot be used, an unexpected error |
| 2 | configuration, dependency, usage, credentials (`FailureAuth`), Jira fail-fast, the delivery checks of §50.7 |
| 3 | a provider failed |
| 4 | the workflow failed or a limit stopped it (a red test, `max_steps`, the budget, no progress, `task needs input`) |
| 5 | cancelled by the operator |
| 6 | completed, but needs a person: a worktree run with `implement` ended without a merge request that nobody declined (`--mode auto` did not commit, or the push or the merge request failed in any mode, §50.6), or `orchestrator review` gave a failing verdict. A story task of a story with `story.push: false` that committed is 0 (§50.5) |

`orchestrator help`, and `--help` or `-h` anywhere on the line, print the commands, the run modes
and every flag an operator types, each with one line on what it does, on stdout with exit 0, before
any config is read; the hidden flags and commands the orchestrator starts itself are not listed.

Support configuration file selection, for example:

```bash
orchestrator --config orchestrator.yaml run "..."
```

A sensible default config path may be used when `--config` is omitted.

Support an execution-mode flag on `run` (and `resume`, §37.7):

```bash
orchestrator run "..." --mode plan            # preview the plan, don't execute
orchestrator run "..." --mode manual           # confirm every step
orchestrator run "..." --mode accept-edits     # approve once, then hands-off
orchestrator run "..." --mode auto             # fully unattended (no prompts at all)
```

Omitting `--mode` shows the decided workflow for approval (approve/cancel) and then
interactively asks the user to choose one of the four modes above (see §1 and §27).

Modes change how much the human sees and confirms between steps, never what a step is
allowed to do: every provider call runs with the fixed authority of §38 in every mode.

---

# 5. Configuration

Use YAML. The current version is 2 (version 1 - `classifier` and `actions.answer` - is
rejected with a message listing what changed).

Example (`orchestrator.example.yaml`):

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
    api_key_env: JEV_API_KEY # the key itself never goes in this file
    max_retries: 1           # 408/429/5xx/529 only, honours Retry-After
    mock: false              # true: offline keyword heuristic instead of the API

translation:
  provider: claude
  model: haiku
  timeout: 60s

clarify:                     # required: a route, or `clarify: {enabled: false}` written out (§49)
  enabled: true              # false: no clarify step (the benchmark); the keys below are optional
  provider: claude
  model: sonnet
  effort: medium             # optional
  timeout: 120s
  docs: ["**/*.md"]          # globs over git-listed files; the deny list applies first
  max_bytes: 16384           # rules + excerpts sent with the task, 1024..131072
  max_rounds: 2              # operator additions after a re-check, 0..5

providers:                   # optional; absent = CLI login in both adapters
  claude:
    auth: auto               # auto | api | login; --claude-auth overrides it
    api_key_env: ANTHROPIC_API_KEY   # the variable that holds the key, never the key itself
    api_env:                 # optional: set for claude in api mode, removed in login (§46.3)
      ANTHROPIC_BASE_URL: https://ai-gateway.example.internal
  codex:
    auth: auto               # auto | api | login; --codex-auth overrides it
    api_key_env: OPENAI_API_KEY # the variable that holds the key, never the key itself

policy:
  context_depth:
    project_threshold: 0.5
  clarify:
    threshold: 0.80          # P(clear) >= threshold -> clear enough; (0, 1], required (§49)
  complexity:
    cuts: {medium: 1.24, complex: 1.48}  # on size = P(medium) + 2·P(complex), §9
  stages:
    thresholds: {research: 0.81, development: 0.81}  # the two asked stages
    force:                   # the template: stages on at each complexity
      medium: [investigation, review]
      complex: [investigation, review]
  # paths:                   # optional path rules, checked on the diff (§45.1)
  #   - {match: "migrations/**", require_stages: [review, testing], approval: manual}
  # explore: {band: 0.05, rate: 0.1}   # optional, opt-in exploration (§45.4)

context:
  max_files: 20
  max_bytes: 24576
  extra_deny: []

actions:
  research:
    provider: claude
    model: sonnet
    effort: medium           # optional for claude and codex: low|medium|high|xhigh|max (§15)
  investigate:
    provider: claude
    model: sonnet
  implement:
    provider: claude
    model: opus
  review:
    provider: claude
    model: sonnet
    timeout: 20m             # optional; only tightens execution.timeout (§25.1)

test:
  command: "go test ./..."
  sandbox: bwrap             # the default (§38.3); none must be written out (§41.3)
  # ro_binds: [~/go/pkg/mod] / rw_binds: [~/.cache/go-build]  re-expose caches
  env_pass: []               # variables beyond the test allowlist; no credentials (§41.3)
  baseline: require_green    # run the tests before any change; off skips it (§41.5)

limits:
  max_steps: 12
  max_fix_iterations: 2
  # max_cost_usd: 5.00       # optional budget for the steps (§12, §45.2)
  prompt:                    # optional bounds on repository data in prompts (§41.2)
    max_test_output_bytes: 32768
    max_diff_bytes: 262144
    diff_summarize: []

execution:
  timeout: 15m
  isolation: worktree          # worktree (default) | inplace: where implement runs (§47)

# worktree:
#   setup: "npm ci"            # optional; trusted, runs once before any step, with the network (§47.4)
#   branch:                    # optional; how a worktree run's branch is named (§50.3)
#     template: "feature/{key}-{slug}"
#     from_instructions: {provider: claude, model: haiku, timeout: 60s}

# delivery:                    # optional; where a worktree run's branch is pushed (§50.7)
#   remote: origin
#   target_branch: main        # the merge request target when HEAD is detached

security:                      # required for protected_paths (§38.3); no key widens what a step may do
  require_clean_worktree: true # a workflow with implement refuses a dirty tree
  protected_paths: [".claude/**", ".mcp.json", ".github/workflows/**", ".gitlab-ci.yml", ".envrc", ".vscode/**"]
                               # required: the whole list implement must not change; [] is none
  allow_unverified_cli: false  # run on a provider CLI outside the verified range (§41.4)
  secret_scan:                 # what implement and the tests add (§41.6)
    mode: stop                 # stop | warn | off
    allow_paths: []

# jira:                        # optional; needed only for run --jira (§39)
#   base_url: https://example.atlassian.net   # DC: with any context path
#   auth: basic                # basic: Cloud (email + API token) | bearer: DC PAT
#   email_env: JIRA_EMAIL      # basic only
#   token_env: JIRA_API_TOKEN  # the token itself never goes in this file
#   timeout: 10s               # one HTTP attempt
#   max_retries: 1             # 0-3; 408/429/500/502/503/504 only
#   max_bytes: 32768           # cap on the rendered task; over it is refused
#   fields:                    # extra text custom fields, in this order
#     - id: customfield_10035
#       label: Acceptance criteria
#   write:                     # a comment and a transition after a merge request (§39.12)
#     comment: true
#     transition: "In Review"

# listener:                    # optional; needed only for `jira listen` (§51), with jira and gitlab
#   poll_interval: 1m          # 30s..1h
#   services_dir: .            # relative to the working directory
#   default_config: service.yaml   # optional; --config for a service without orchestrator.yaml
#   max_attempts: 3            # 1..10 finished runs per ticket
#   clone_timeout: 10m         # 1m..1h
#   query:
#     projects: [PAY]          # required, at least one
#     label: ai-run
#     statuses: ["To Do"]      # required; tickets are picked only from these
#     reporters: ["5b10a2844c20165700ede21g"]   # required: accountId (Cloud) or username (DC)
#   labels:
#     service_prefix: "service:"
#     project_prefix: "gitlab:"
#   transitions:               # each a transition's name or its target status
#     start: In Progress
#     blocked: Blocked
#     failed: Failed
#     done: Ready to Review
#     retry: To Do
# gitlab:                      # optional; needed only for `jira listen`
#   base_url: https://gitlab.corp   # https, no userinfo
#   token_env: GITLAB_TOKEN    # env only; a literal token is refused
#   username: oauth2
#   allowed_groups: [backend, platform/payments]   # required; clone only from under these
```

Model names must be treated as opaque provider-specific strings.

Do not hardcode lists of allowed models.

The decision provider is chosen statically, like any action's provider: `jev` or a CLI
provider. Switching providers does not change the flow (§8). An LLM is never an
automatic fallback for Jev.

---

# 6. Configuration Validation

Configuration validation must happen completely **before any AI call, HTTP request or
repository modification**.

Validate:

* YAML syntax;
* config version (2);
* `decision`: supported provider (`jev` or a CLI provider), model, positive timeout,
  `complexity.primary` in `choice|score`, `on_failure` in `ask|fail`;
* `decision.jev` when the provider is `jev`: an `http(s)` `base_url`, `api_key_env`,
  `max_retries` in 0–3; a literal `api_key` in the file is an error whatever the
  provider;
* `translation`: provider, model, positive timeout;
* `clarify` (§49): present (a route, or `enabled: false` written out); with `enabled: true`
  provider, model, positive timeout, `docs` globs, `max_bytes` in 1024..131072, `max_rounds`
  in 0..5;
* `policy`: every stage threshold present (absent is not 0) and in [0, 1], known stage
  and complexity names only, `project_threshold` and complexity thresholds in [0, 1],
  `clarify.threshold` present and in (0, 1]; the names `analysis` and `planning` in a
  threshold, in `force` or in `explore` are refused with their replacement and `specs/clarify`;
* `context`: positive `max_files`/`max_bytes`, no empty `extra_deny` pattern;
* every action's provider and model (`research`, `investigate`, `implement`,
  `review`; `actions.analyze` and `actions.plan` are refused naming `actions.investigate`),
  non-negative per-action timeouts, and an optional `effort` in
  `low|medium|high|xhigh|max`, accepted only with provider `claude` (an effort another
  provider cannot apply is an error, never silently dropped);
* supported provider names;
* limits;
* execution timeout;
* test command;
* `jira`, when present (§39.2): an absolute `https` `base_url` with no query, fragment
  or credentials (`http` only for `localhost`, `127.0.0.1`, `::1`); `auth` in
  `basic|bearer`; `token_env` required and `email_env` required for `basic`, rejected for
  `bearer`, both valid variable names; `timeout` > 0, `max_retries` in 0–3, `max_bytes` in
  1024–262144; each `fields[]` a `customfield_<N>` id with a unique, single-line label
  that is not a reserved section name. A literal `token`, `api_token`, `password`, `pat` or
  `email` key is an error;
* `listener`, when present (§51.2): `poll_interval` in 30s–1h, `clone_timeout` in 1m–1h,
  `max_attempts` in 1–10, one-line `services_dir`, `query.projects`, `query.statuses` and
  `query.reporters` non-empty, every JQL value one printable line without `"` or `\` (so Go's
  quoting is total), a `query.label` without whitespace, two different label prefixes, and the five
  `transitions` (`start`, `blocked`, `failed`, `done`, `retry`) all present. `listener` without
  `jira` and `gitlab` is an error;
* `gitlab`, when present: an absolute `https` `base_url` with no userinfo, query or fragment (a
  context path is allowed), `token_env` a valid variable name, `username` (default `oauth2`),
  `allowed_groups` non-empty project-path prefixes. A literal `token`, `api_token`, `password` or
  `pat` key is an error. `gitlab.token_env` and the Jira variables are registered with
  `execution.ScrubCredentials` right after `Validate`.

All problems are aggregated into one error.

A version-2 file is parsed strictly: a key the schema does not know - a typo such as
`timout`, or a key in the wrong section such as `translation.effort` - is an error listing
each unknown key with its line, never silently ignored. A file of another version is parsed
leniently, so the version check can say what changed.

Supported CLI provider IDs are only:

```text
claude
codex
gemini
```

Unknown provider example:

```yaml
provider: openrouter
```

must produce an error.

---

# 7. Mandatory Local CLI Validation

This is an important MVP requirement.

AI integrations use locally installed executable clients:

```text
claude → executable "claude"
codex  → executable "codex"
gemini → executable "gemini"
```

For every provider referenced by the configuration, verify that the executable is available in `PATH`.

Use an injectable abstraction around behavior equivalent to:

```go
exec.LookPath(...)
```

Do not call `exec.LookPath` directly from business logic so the validation is unit-testable.

If the configuration contains:

```yaml
actions:
  review:
    provider: codex
```

and `codex` is not installed, configuration validation must fail.

Example error:

```text
configuration validation failed:

provider "codex" is configured for action "review",
but executable "codex" was not found in PATH.

Install the Codex CLI or update the configuration.
```

If one provider is used by several actions, aggregate them:

```text
provider "codex" is required by actions [investigate, review],
but executable "codex" was not found in PATH
```

The translator's provider is always checked (even for an English request, so fail-fast
does not depend on the request's language), and so is an LLM decision provider's and, with
`clarify.enabled: true`, the clarify step's. With
the Jev decision provider, `decision.jev.api_key_env` must name a set, non-blank
environment variable (`jev decision provider: JEV_API_KEY is not configured`), unless
`decision.jev.mock` is on.

The Jira credential variables are checked only when they will be used (§39.2):
`run --jira` requires the `jira` section and a set, non-blank `jira.token_env` (and
`jira.email_env` for `basic`), e.g. `jira.token_env names JIRA_API_TOKEN, which is not
set`. `orchestrator validate` checks them whenever the section is present. `run` without
`--jira` and `resume` never check them, so a `jira` section does not break runs that do
not use it.

### Critical fail-fast requirement

Given:

```text
claude installed
codex missing
```

and config uses Claude for translation and Codex for review:

```bash
orchestrator run "..."
```

must fail before Jev is called or Claude is invoked.

Do not partially execute the workflow and discover missing dependencies later.

### Authority check

The chain is `config.Load` → `Validate` → `ValidateDependencies` →
**`ValidateAuthorities`**, all before any provider call. `ValidateAuthorities` asks, for
every route (each action, the translator, the clarify step when enabled, an LLM decision
provider), whether the routed
provider can enforce the authority that route runs with (§38). A route it cannot enforce
fails the run with an `AuthorityError` (exit 2), e.g.:

```text
configuration validation failed:

provider "gemini" cannot enforce the "read" authority required by "review"

The orchestrator does not run a provider with authority it cannot enforce, rather than let it run with unknown authority (specs/permissions/SPEC.md §6).

remedy: route review to a provider that supports it: claude (actions.review.provider: claude)
```

Gemini currently supports no authority (its flags are unverified, §38), so routes to it fail
here even when its executable is installed. Codex routes requiring `none` or `web` fail for
the same check. `orchestrator validate` runs the same chain.

### Claude credential resolution

Inside the same chain, right after `ValidateDependencies`, `config.ResolveClaudeAuth` decides
the credential mode of the process (`api` or `login`, §46) from `--claude-auth`,
`providers.claude.auth` and the environment. An explicit `api` (or `auto` with an invalid key)
without a usable key is a config error before the translator, Jev or any CLI runs; the
resolved mode is handed to the claude adapter before the first `claude` process, the
`--version` probe included. `validate` prints the same line.

### The org file and the budget

The org layer is merged inside the `Load` step (`config.LoadLayered`, §45.3), so `Validate`
and every later check see the merged config. An org file that exists but cannot be read, a
repository setting weaker than the org's and a route outside the org's `allowed_models` are
config errors before any call; none is a warning. With `limits.max_cost_usd`,
`ValidateDependencies` refuses any routed provider that does not report a call's cost
(`provider.CostReporter`; only claude does), because a budget that cannot be enforced is the
one wrong answer (§45.2).

### Provider CLI version and the test sandbox

Right after `ValidateAuthorities`, every routed provider CLI's version is checked against
the range its permission flags were verified on (§41.4); outside it the run stops (exit 2)
unless `security.allow_unverified_cli: true`. The test sandbox is on unless
`test.sandbox: none` (§41.3), so `ValidateDependencies` checks `bwrap` and its binds and the
chain probes it by default.

### Jev model check

After the chain, and only with `decision.provider: jev` without `mock`, the orchestrator
asks Jev which models it serves (`GET {base_url}/v1/models`, the same client, timeout and
retries as the decision rounds). `decision.model` passes when it is listed or is a pinned
version (`^jev-\d+\.\d+\.\d+$`, which Jev accepts but does not list); otherwise the run
stops with a `ConfigError` (exit 2) naming the models Jev serves. `run` asks before the
repository is opened and before any Jira request, translation or decision round, so a
failure creates no run; `run --stage` and `resume` never ask, and `validate` asks and
prints `✓ jev serves <model>`.

A rejected key (401/403) and a cancellation always stop. Any other failure to check - Jev
unreachable, overloaded, or answering in an unexpected shape - stops the run with
`on_failure: fail`; with `on_failure: ask` it is a warning on stderr and the run goes on,
because a failing decision round falls back to the manual choice anyway (§8.6).

---

# 8. Decision Flow

The workflow is decided in two rounds by a **decision provider** that only returns
probabilities; every choice made from them is Go code (§9). Details:
`specs/JEV integration/SPEC.md` (normative) and `specs/JEV integration/TASKS.md`; the clarity
question and the step it triggers: §49.

```text
request
  → language check (deterministic) → translation if needed (§8.1)
  → pre-check: P(none), P(project), P(clear) -- decision round 1 (one call)
  → Go: P(clear) >= policy.clarify.threshold ?
      yes → go on
      no  → clarify step (§49): an LLM call with authority none, given instructions Go chose
          → re-check: P(clear) of request + clarification (decision.Provider.Clarity)
          → still below: at a terminal the operator adds to the task (up to clarify.max_rounds);
            in --mode auto, or with the rounds spent, the run stops: task needs input
  → policy picks the context depth
  → project → Context Manager → ProjectContext (§8.3; its query is request + clarification)
  → classification: complexity + the 2 asked stages    -- decision round 2
  → policy → complexity, enabled stages (§9)
  → workflow
```

With `run --stage` the decision round 2 and the policy do not run: the operator's stages
become the decision and the flow continues at the workflow builder (§40). The clarity
question is still asked and the clarify step still runs (§49.2). With `run --story-task` the
step never runs (§48).

## 8.1 Translation

The questions are asked in English. A request whose prose letters are at least 30%
Cyrillic (identifiers, paths and backticked text are left out of the count) is
translated by one CLI call (`translation.provider/model`, `translation.timeout`). The
translation is sanity-checked: non-empty, 0.3×–3× the original length, under 10%
Cyrillic, every identifier and path of the original preserved verbatim. Any failure sends
the original instead, with `translation.failed` in the trace and a warning - it never
fails the run. The translated text is used only for the decision provider and for
relevant-file search; the stages always get the original. After a clarify step the text the
decision provider sees is the request followed by the rendered clarification (§49.4); the
same rule translates it, and a second translation call is made only when a clarification was
added.

## 8.2 Questions

Every provider answers the same questions (`internal/decision/questions.go`), over the
same state:

* `context_depth` (choice: `none` | `project`) - is repository context needed to classify
  the request? Asked against the request alone.
* `clarity` (yes/no, §49.1) - is the request clear enough to act on? Asked in the same call as
  `context_depth`, against the request alone (so a clear task costs no extra call), and again
  alone, against the request followed by the clarification, in each re-check.
* `complexity` (choice: `simple` | `medium` | `complex`) and, when
  `decision.complexity.shadow` is on, `complexity_score` (score over the same three
  levels). The policy decides on `primary`; the other is recorded for comparison only.
  Both are built from one criteria list, so their texts cannot drift apart.
* `stage_<stage>` (yes/no) for the two asked stages, each judged independently:
  `development` (must repository files change?) and `research` (is outside knowledge
  needed?). Investigation, testing and review are not asked: their answers repeated the
  task's size (§9.1); they come from the level and from development (§9). Analysis was asked
  until `specs/clarify`; it is not a stage any more (§49), and an answer to
  `stage_analysis` is an unexpected answer, from Jev and from an LLM alike.
* `scope_story` (yes/no, a shadow question, §48.9) - does fulfilling the request need several
  changes that are each worth reviewing and merging on their own, in different services or as
  steps that each leave the system working? Its probability is recorded in `decision.json` as
  `scope.story` and **decides nothing**: no threshold, no hint, no workflow change. It is not
  asked for `--stage` (no provider) or `--story-task` (a story task is never a story).

Answers are strictly validated: the expected questions only, the right answer type,
exactly our options, values in [0, 1], distributions summing to 1. Anything else is an
`InvalidResponse` decision error. A provider's own pick (Jev's `choice` field) is ignored.

## 8.3 Context Manager

Runs only when the policy picks depth `project`. It builds a compact `ProjectContext` -
never whole files, except the ones the request names:

* metadata from the manifest (`go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`,
  `pom.xml`): language, project type, module, direct dependencies;
* test layout (unit/integration);
* rules: bullet lines of `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`;
* excerpts of the `README.md` / `docs/*.md` sections that mention the request's terms;
* relevant files - path-word matches plus verbatim identifiers in sources - with a
  one-line summary each, followed by their tests; a file the request names by its
  repository path (`specs/x/TASK.md`, `./specs/x/TASK.md`) comes first and also carries
  its `content`, clipped to 8 KiB, so a request that points at a task file is classified
  on that file's text;
* git branch, dirty flag, changed paths.

Files come from `git ls-files --cached --others --exclude-standard`, so `.gitignore` is
honoured. Before anything is read, a deny list (`.env`, `.env.*`, `*.pem`, `*.key`,
`credentials*`, plus `context.extra_deny`) is applied to every path segment;
binaries, files over 256 KB, symlinks, `.git/` and `.orchestrator/` are never read. The
`max_files`/`max_bytes` budget drops doc excerpts first, then the least relevant files,
then rules, and marks the context `truncated`.

A failed collection does not stop the run: classification continues without context and
the trace records `context_limited` and why.

## 8.4 Jev provider

`POST {base_url}/v1/systemone` with `Authorization: Bearer <key>`; one request per round,
all questions evaluated in parallel against one state. The key is read from
`decision.jev.api_key_env` by the app and handed only to the client's constructor; it is
redacted from every error. Failures map to `DecisionError` kinds: timeout/408,
connection, 429 (rate limited), 5xx/529 (unavailable), 401/403 (auth), other 4xx (bad
request), non-JSON (invalid response). Transient kinds are retried up to `max_retries`,
honouring `Retry-After` within one request's `timeout`; cancellation is never retried.
The model version Jev reports is recorded in the trace. Before the first round,
`GET {base_url}/v1/models` checks `decision.model` (§7).

`decision.jev.mock: true` swaps the HTTP client for an offline stand-in: it validates the
request the way Jev does and answers deterministically from keywords in the request. It
exists while there is no API access; its model is reported as `mock:<model>` and a
warning is printed.

## 8.5 LLM provider

With `decision.provider: claude|codex|gemini` the same questions go to that CLI - one
call per round (`ACTION: decide-context`, `ACTION: decide-classify`) and one per clarity
re-check (`ACTION: decide-clarity`), the state embedded as JSON data. The answer is the same probability JSON, validated the same way (with a
slightly wider sum tolerance, then normalised). CLI failures become `DecisionError`s, so
everything downstream - including §8.6 - behaves exactly as with Jev. The trace records
`source: llm`.

## 8.6 Provider failure: manual choice

If either round, or a clarity re-check, fails and `decision.on_failure` is `ask`, the user
chooses by hand (a failed pre-check means the clarify step does not run, §49.2): the
failure is shown (with the collected context, if any), then a complexity
(`[1] simple [2] medium [3] complex`) and the stages in their fixed order. Stages forced
by the chosen complexity are pre-selected; switching one off takes an explicit warning
and confirmation. An empty selection is refused. The result goes through the same forced
rules and is recorded as `source: user`. With `on_failure: fail`, in `--mode auto`, or
without a terminal on stdin, the provider's error ends the run (exit code per §24).
Cancellation never offers a choice.

---

# 9. Policy and Deterministic Workflow Builder

Workflow selection belongs to Go code. The policy (`internal/policy`) is a set of pure
functions over the provider's probabilities and `policy` config:

```text
clarity        clear if P(clear) >= policy.clarify.threshold          (inclusive, §49)

context depth  project if P(project) >= policy.context_depth.project_threshold

size           P(medium) + 2·P(complex), in [0, 2], from the probabilities (any provider;
               Jev's own score is recorded, never used)

complexity     complex if size >= policy.complexity.cuts.complex
               else medium if size >= policy.complexity.cuts.medium
               else simple
               (applied to both questions; primary decides, agree = primary == shadow)

stages         policy.stages.force[complexity]                       (the template)
               ∪ asked stages with P >= policy.stages.thresholds    (inclusive)
               ∪ testing, whenever development is enabled
               without development: testing and review off ("dropped: no code change")
               nothing enabled → investigation
                 ("forced: no stage cleared its threshold, defaulting to investigation")
```

The asked stages are research and development (`domain.AskedStages`, §8.2); investigation,
testing and review have no probability and come from the template. A config that still sets
`policy.complexity.medium`/`complex`, a threshold for investigation, testing or review, or
the removed `analysis`/`planning` (a threshold, a `force` entry, a `policy.explore` entry) is
refused with a message naming the replacement; for `analysis` and `planning` that is
`policy.clarify.threshold` and `investigation`, and `specs/clarify`. The design records are
`specs/decision-shape/` and `specs/clarify/`.

The clarity question is not a stage question. Whether the task is clear is decided first
(§49) and decides whether the clarify step runs, never which stages do.

Stages run in a fixed order, and each maps to one action:

```text
research      → research
investigation → investigate
development   → implement
testing       → test
review        → review
```

`analysis` is deliberately not reused for the merged stage: in every `decision.json` written
before `specs/clarify` it means "the cause or the place to change is unknown", and `decisions
calibrate` would mix the two meanings. A story task (§48) turns off research and
investigation.

The workflow builder filters that order by the enabled stages. The same decision always
produces the same workflow. A decision with `source: manual` (§40) carries the operator's
stages and no probabilities; the builder treats it the same way.

Do not ask an LLM to generate arbitrary workflow graphs, and do not let a decision
provider choose stages or their order - it only estimates probabilities.

Two additions keep this determinism (§45):

* `policy.explore` (opt-in) turns a stage on that the policy left just below its threshold,
  in a share of runs chosen by a hash of the run id and the stage (`policy.Explore`, a pure
  function). The input to "the same decision always produces the same workflow" is then the
  decision plus the run id, and `decision.json` records the stage as `explored`. It never
  turns a stage off, never explores development (it would skip "testing with development")
  and never applies to `--stage` or a resume.
* `decision.json` records `policy_hash`, the sha256 of the `DecisionPolicy` the decision used,
  so runs before and after a threshold change are compared and never mixed (§45.4).
  `policy.paths` is not part of the decision: it is checked on the diff after implement (§45.1).

## 9.1 The example thresholds

`orchestrator.example.yaml` (and the benchmark configs, which follow it) ships the cuts
`medium: 1.24, complex: 1.48`, the thresholds `research: 0.81, development: 0.81`, the
template `medium: [investigation, review]`, `complex: [investigation, review]` and
`policy.clarify.threshold: 0.80`. A simple task with development gets no investigation unless
the template says so: implement reads the code itself.

*Historical note.* The rest of this section is the record of how the shape was measured, before
`specs/clarify` removed the analysis stage and renamed planning to investigation (§49); the
stage names in it are the ones of that time. The thresholds and the template were then
`research: 0.81, analysis: 0.81, development: 0.81`, `medium: [analysis, review]`,
`complex: [analysis, planning, review]`.

**Why the shape changed** (`specs/decision-shape/SPEC.md` §1). The first benchmark round
(2026-10-12: 8 Go tasks from this repository, configs A–D, 3 repetitions;
`specs/benchmark/tasks/TASK-BM16-pilot-and-full-run.md`) asked six stage probabilities and
cut complexity with absolute thresholds. The claude/haiku decision provider enabled analysis
and review in every run, planning by chance, and called every task `medium`; `auto` cost about
2.4× development + testing alone with no task solved that development + testing missed.
Raising the stage thresholds to 0.81 moved the coin, it did not add signal. Measured on 43
claude/opus decisions of this repository's own tasks (2026-10-13), planning and review
correlate with the size of the task at r = 0.94 and 0.83 (they only repeat it), while size
spreads (interquartile range 0.35) and is stable across repeats of one task.

**Where the values come from.** The cuts are the 50/35/15 quantiles of those 43 opus decisions
(`orchestrator decisions calibrate` prints them, §45.4); they hold for that decision model and
task stream only. "Complex" therefore means "the top share of this stream's tasks", not an
absolute difficulty. On the benchmark's 22 haiku decisions size barely spreads (interquartile
range 0.14) and calibrate refuses to propose cuts; whether that is the model or the
benchmark's tasks is open, since the two sets of decisions were made on different tasks. The
stage thresholds keep their earlier values: no data justifies moving them. The offline Jev mock
answers 0.85 for a strong signal and 0.15 for a weak one.

Size spreads tasks for routing (§44.8) but, on the same data, does not predict which task will
need a fix (about 45% did, at every size). A failed check escalating the model
(`actions.implement.escalate`, §31.1, §44.8) is the lever for those. Recalibrate when the
decision model, the task stream or the questions' wording changes.

**Recalibration note (the scope question, 2026-10-19).** The fourth question `scope_story`
(§8.2, §48.9) joined the classification call, which changes `questions_hash`: `decisions
calibrate` keeps the old and the new decisions apart. One more question in the same call may
move the other answers, so the cuts and thresholds above are to be re-checked once enough
decisions with the new hash exist; until then they are the values measured without it.

**Recalibration note (the clarity question, 2026-10-25).** `clarity` joined the pre-check
call and `stage_analysis` left the classification call (§8.2, §49), which changes
`questions_hash` on purpose. The cuts and the thresholds above are to be re-checked once
enough decisions with the new hash exist, and `policy.clarify.threshold: 0.80` is not
calibrated at all: `decisions calibrate` shows the P(clear) distribution and the share of runs
that stopped `task needs input` and proposes nothing for it (§45.4, §49.9).

---

# 10. Supported Actions

Define a fixed set:

```text
research
investigate
implement
test
review
```

No dynamically invented action types. The clarify step (§49) is not one of them: it runs
before the decision, is never a step of a workflow, and `actions` has no entry for it (it
has its own `clarify:` section).

`research` and `investigate` are read-only: their prompts forbid changing files.
Research gathers external knowledge (approaches, libraries, alternatives) and feeds
investigate. `investigate` reads the code: with `implement` in the workflow its artifact is
the plan (root cause when it is a defect, steps, affected files, risks, acceptance criteria),
read by implement and review; without `implement` it is the last read-only step and writes the
answer. It gets research's artifact and the clarification, if there is one. Before
`specs/clarify` two actions, `analyze` and `plan`, did this work in two passes; `actions.analyze`
and `actions.plan` in a config are refused, naming `actions.investigate`.

There is no separate `answer` action. When a workflow has no `implement`, its last
read-only step also writes the reply to the user's request (an `answer` field), which is
printed when the run completes.

Each AI-backed action uses the provider/model configured for that action.

Example:

```yaml
actions:
  implement:
    provider: claude
    model: opus
```

---

# 11. Workflow Engine

Implement workflow execution as a state machine, not just a `for` loop over immutable steps.

Transitions:

```text
step success
→ the next step of the workflow
→ done after the last step

test failure
→ implement, if implement is in the workflow
→ otherwise the run fails ("tests failed")

review fail
→ implement, if implement is in the workflow
→ otherwise the run completes and the verdict is shown
  (reviewing a diff and reporting problems fulfils a review-only task)

any other step failure
→ the run fails
```

After a review failure, the normal flow is:

```text
review
→ implement
→ test
→ review
```

when testing is in the workflow.

Maintain execution history rather than overwriting previous step runs.

Support an optional `Gate` hook the engine consults immediately before each step
executes:

```text
Gate.BeforeStep(ctx, run, index, action) -> proceed | abort
```

A nil `Gate` reproduces fully-automatic execution exactly (`--mode auto`/`accept-edits`).
A `Gate` implementation may mutate `run.Workflow` before returning `proceed`; such a
mutation only affects future transition lookups, never the step about to execute. An
`abort` decision stops the run (status `stopped`, exit code 5 - the same "user
cancellation" path as `errors.Is(err, context.Canceled)`) without ever invoking the step
executor for that step. The engine itself stays unaware of *why* a step was declined - it
only sees `proceed`/`abort`, keeping the state machine free of interactive concerns.

---

# 12. Workflow Limits

Protect against infinite loops.

Config:

```yaml
limits:
  max_steps: 12
  max_fix_iterations: 2
  stop_without_progress: true   # default; §44.6
```

Track:

* total executed steps;
* number of fix iterations.

When a limit is reached, stop execution gracefully.

Example:

```text
Run status: stopped
Reason: maximum fix iterations exceeded
```

Do not continue invoking AI clients after a limit is exceeded.

`limits.stop_without_progress` (default `true`) ends a fix loop before `max_fix_iterations` when
it goes nowhere: implement left the working tree as the previous implement did, the same
tests fail the same way, or review repeats its blocking issues. The run stops with status
`stopped`, a reason that starts with `no progress:` and exit code 4. The check is Go's, over the
persisted executions only (§44.6); `max_fix_iterations` still applies when it is off.
`limits.verify` bounds the in-session verify tools (§44.3).

`limits.max_cost_usd` (optional, positive, finite) is a budget for the workflow steps: before
each step the engine sums the cost the executions reported, and at or over the budget the run
stops with status `stopped`, `budget exceeded: $5.12 of $5.00` and exit code 4. A step can
overshoot by its own cost; the decision phase is not counted; a call cut off before its final
report has no cost, so the sum is then a lower bound. A provider that reports no cost cannot
be routed with a budget (§7). Details in §45.2.

---

# 13. AI Provider Abstraction

Create a common interface similar to:

```go
type Request struct {
    Prompt     string
    Model      string
    Effort     string           // optional reasoning effort; "" = the CLI's default
    WorkingDir string
    Authority  domain.Authority // §38; no usable zero value - "" is refused
    DenyRead   []string         // secret deny list (+ the runs directory) for file tools
    Stream     io.Writer        // optional live sink for --verbose; nil = capture only
}

type Result struct {
    Output   string // stdout
    Stderr   string
    ExitCode int
    Duration time.Duration
}

type Client interface {
    Execute(ctx context.Context, req Request) (Result, error)
    Supports(a domain.Authority) bool // only authorities verified live (§38.2)
}
```

Implement:

```text
ClaudeClient
CodexClient
GeminiClient
```

Create a provider registry that resolves a provider ID to the correct client.

Workflow code must depend only on the common interface.

---

# 14. Generic Process Executor

External commands must use a reusable abstraction.

Example:

```go
type Spec struct {
    Executable string
    Args       []string
    Stdin      string
    WorkingDir string
    Env        []string  // nil inherits (git); providers and test pass ScrubbedEnv()
    StdoutSink io.Writer // optional live tee (--verbose), in addition to capture
    StderrSink io.Writer
}

type ProcessExecutor interface {
    Run(ctx context.Context, spec Spec) (ProcessResult, error)
}
```

Capture:

* stdout;
* stderr;
* exit code;
* duration.

Use:

```go
exec.CommandContext
```

Do not invoke provider commands through:

```bash
sh -c
```

Do not interpolate the user prompt into a shell command string.

Pass arguments safely.

---

# 15. Provider Clients

Implement one package per provider.

Suggested structure:

```text
internal/provider/
  client.go
  registry.go

  claude/
    client.go

  codex/
    client.go

  gemini/
    client.go
```

Provider-specific CLI arguments must be isolated inside those packages.

The common orchestrator must not know how individual provider CLI syntax works.

Each adapter must support:

* configured model, and the configured `effort` where the CLI can apply it (claude and
  codex);
* prompt;
* working directory;
* context cancellation;
* stdout;
* stderr;
* non-zero process exit;
* the call's authority (§38): `Supports(authority)` declares what the adapter can
  enforce, only after a live check against the real CLI; `buildArgs` translates the
  authority into that CLI's flags - the only place such flags exist; `Execute` refuses a
  missing or unsupported authority without spawning anything;
* the scrubbed environment (`execution.ScrubbedEnv`, §38).

---

# 16. Repository Handling

The MVP operates on the current Git repository.

Example:

```bash
cd ~/projects/my-service
orchestrator run "Fix bug"
```

Before workflow execution:

* verify current directory belongs to a Git repository;
* read initial Git status;
* detect dirty working tree;
* do not discard existing changes.

If there are existing uncommitted changes and the workflow contains `implement`, the run
refuses to start (exit 2, before any approval prompt) unless
`security.require_clean_worktree: false` (§38). Otherwise - a read-only workflow, or the
key set to false - display a warning:

```text
Warning: repository contains uncommitted changes.
Existing changes will not be reverted.
```

The tree's state is read at startup, before `.orchestrator/runs/` is created inside it.

Before the first step the run records a restore point, `pre_run_ref` (§38): HEAD on a
clean tree, otherwise a snapshot commit of everything the run starts on top of, untracked
files included, built on a throwaway index. It is printed as `Restore point: <sha>`, and
pinned under `refs/orchestrator/runs/<run-id>` so `git gc` cannot prune it (§38.3).

Do not automatically stash, reset, checkout, or revert user files.

After implementation obtain the actual Git diff.

The actual repository state is the source of truth.

---

# 17. Action Context and Artifacts

Do not pass an indefinitely growing chat transcript between models.

Use structured artifacts plus repository state.

Implement artifact types similar to:

```text
ResearchArtifact
InvestigationArtifact
ImplementationArtifact
TestArtifact
ReviewArtifact
```

The read-only artifacts (research, investigation) carry an optional `answer`: the reply
to the user when that step is the last read-only step of a workflow without
development (§10). The investigation artifact is the plan when development follows:
`summary`, `summary_short`, an optional `root_cause`, `steps`, `affected_files`, `risks` and
`acceptance_criteria`. There is no analysis artifact (it was folded into this one by
`specs/clarify`); the clarify step (§49) writes `artifacts/clarify-<N>.json`, which is a
record and not an input of any stage.

What was agreed in the clarify step reaches the stages as the clarification (§49.4): the stages
that take the task (research, investigate, implement first and retry, review) get the request
through the task block as always and, after it, the rendered clarification as a fenced block.
An empty clarification adds nothing, so a run without one is byte-for-byte what it was.

Every stage's artifact, and the review result, also carries `summary_short`: `summary` in
one line of at most 72 characters, for an operator skimming the run (it is printed first,
as `In short`). Implement's is asked for in the imperative mood and becomes the commit
message (§42.2). It is optional: without implement's, the operator is asked to write the
commit message.

Implement is also told to finish the change the way the repository's own instructions say
(`CLAUDE.md`, `AGENTS.md`, `CONTRIBUTING.md`, the README of the specification or task list
the task comes from): the documentation the change makes out of date and the task tracking
they require (marking the task done) belong to the change. Review checks the same (§19).
There is no documentation stage: the stage set is the decision's (§9), and a missed update
goes back to implement like any other missed requirement.

Implement is also told what it cannot do: its authority (§38) has no shell, and the run
executes nothing but the test command. A step the task or the repository requires that needs
running something else - a live check against an installed tool, a build, lint or format
gate the test command does not run - is the operator's: implement names it in `summary` as
left to the operator, never claims it done, and leaves the task tracking that waits on it
open. The way to have such a gate checked by the run is to make it part of `test.command`.
Review reports tracking left open this way as `operator:` medium, never high - also when it
waits on the test command and the latest test passed: implement edits before the tests run
and cannot know their result, so sending it back would loop until `max_fix_iterations`.

Each stage's prompt carries what the earlier stages found that it can use
(`specs/quality/SPEC.md` §4): investigate gets research's findings; implement gets the
investigation's `affected_files` and `risks`, its steps and `acceptance_criteria`;
review gets the investigation's steps, `risks` and criteria (numbered). Empty lists are left
out. The first implement after an investigation with `affected_files` is told to start from
those files and not
to survey the repository again, opening another file only for what they do not answer, and to
follow the code where the plan is wrong (a retry is not: it starts from the failure). A
retried implement also gets the git diff --stat of what the run has changed so far (against
`pre_run_ref`), fenced and bounded by `limits.prompt.max_diff_bytes`; if git cannot tell, the
step goes on without it and a warning is shown. The investigation's optional `acceptance_criteria`
(at most 8, each at most 300 bytes) are checkable statements about the result; the parser
keeps the first 8 non-empty ones and cuts each on a UTF-8 boundary.

When the investigation has criteria, review must return a verdict for each one
(`criteria: [{index, met, evidence}]`, `specs/quality/SPEC.md` §7.1): exactly one entry per
criterion, indexes `1…N` each once, `evidence` of 1 to 500 bytes. Any other answer is a
malformed review (a hard step error). Go fills `criterion` from the investigation. An unmet criterion
fails the review like a `high` issue, and the retried implement is told which criteria were
unmet, with the evidence, before the issues. Without criteria the answer has none. Every
review prompt also tells the reviewer it has Read, Grep and Glob and should read the code
around the change (§7.2).

Example investigation artifact (development follows):

```json
{
  "summary": "Retry policy retries all non-2xx responses.",
  "summary_short": "Every non-2xx response is retried",
  "root_cause": "404 is not excluded.",
  "steps": [
    "Mark 404 as non-retryable in shouldRetry"
  ],
  "affected_files": [
    "internal/http/client.go"
  ],
  "risks": [],
  "acceptance_criteria": [
    "A 404 response is returned after one request, without a retry"
  ]
}
```

Example implementation artifact:

```json
{
  "summary": "Excluded HTTP 404 from retries.",
  "summary_short": "Exclude HTTP 404 from retries",
  "changed_files": [
    "internal/http/client.go"
  ],
  "tests_added": [
    "TestDoesNotRetry404"
  ]
}
```

Artifacts must be serializable to JSON.

---

# 18. Test Execution

Tests must be run by the orchestrator itself, not trusted from an LLM response.

Configuration:

```yaml
test:
  command: "go test ./..."
```

The orchestrator runs the command and records:

```json
{
  "command": "go test ./...",
  "exit_code": 1,
  "success": false,
  "stdout": "...",
  "stderr": "...",
  "duration": "..."
}
```

Source of truth:

```text
exit code 0
→ PASS

exit code != 0
→ FAIL
```

On test failure:

```text
test
→ implement
```

The next implementation prompt must contain:

* original user task;
* relevant previous artifacts;
* failing test command;
* stdout/stderr;
* instruction to fix the implementation.

Then run tests again.

The test command runs with an allowlisted environment and, unless `test.sandbox: none`,
under bwrap (§41.3). The stdout/stderr in the retry prompt are bounded by
`limits.prompt.max_test_output_bytes`, keeping the failure lines (§41.2); the artifact and
the step log keep everything. A workflow with `implement` and `test` also runs the command
once before the first step and refuses a red suite unless the run was started with
`--allow-red-tests` (§41.5).

An optional top-level `checks:` list (build, vet, lint, SAST) runs inside this step, in
order, before `test.command`, and stops at the first non-zero exit code. Checks are exit codes
like the test, run under the same sandbox, environment and guard (a guard comparison after each
command), and `TestResult` records them in `checks` and `failed_check` (§44.2). From a failed
test's output Go may also read the names of the failing tests for the retry prompt
(`failed_tests`, §44.7); that never decides pass/fail.

---

# 19. Review

Review is an AI-backed action.

Review input should contain:

* original task, followed by the clarification when there is one (§49.4);
* the investigation where available;
* implementation summary;
* latest test result;
* current Git diff - new files with their content, deny-listed files by name only, lock
  files as a stat line, within `limits.prompt.max_diff_bytes`, and redacted by the secret
  scan (§41.2, §41.6).

Require structured output:

```json
{
  "status": "fail",
  "summary": "404 is still retried in one path.",
  "summary_short": "404 still retried in one path",
  "issues": [
    {
      "severity": "high",
      "file": "internal/http/client.go",
      "description": "Wrapped 404 remains retryable."
    }
  ]
}
```

Allowed severity:

```text
critical
high
medium
low
```

Decision rule for MVP:

```text
critical
→ fail

high
→ fail

medium
→ pass with warning

low
→ pass with warning
```

The review prompt states this rule to the reviewer, with a rubric for each level
(critical: breaks the build, data or security; high: incorrect behavior, missed requirement
or regression; medium: real but non-blocking; low: style), so the severity it picks is the
one that decides. The outcome is still `ReviewResult.Decide()`, never the model's `status`.
It also checks the change against the repository's own instructions (§17): a documentation
or task-tracking update they require and the diff lacks is a missed requirement (high), so
the change goes back to implement; other documentation left out of date is medium.
A requirement only running something other than the test command can meet (§17), and task
tracking left open because it waits on one, is never critical or high: another
implementation attempt cannot do it, and failing on it would only loop until
`max_fix_iterations`. Review reports it as medium with `operator: ` at the start of its
description, so it reaches the operator among the warnings. Review is also told that the test
result covers only what the test command runs, from the repository root, and that a claim in
the diff or the summary that such a step was done is not evidence.

If review returns `fail`:

```text
review
→ implement
```

The next implementation action must receive the review issues.

The rule above is extended by the quality loop (§44.4): an unmet acceptance criterion counts
as a `high` issue; with `review.fix_medium` a passing review that reports a `medium` issue
sends the run back to implement once; `actions.review.models` adds extra reviewers whose
answers Go merges before `Decide()` runs.

---

# 20. Prompt Builders

Create explicit prompt builders for:

```text
translate          (§8.1)
decide-context     (§8.5, LLM decision provider only)
decide-classify    (§8.5, LLM decision provider only)
decide-clarity     (§8.5, §49.1, LLM decision provider only: the re-check)
clarify            (§49.3, authority none)
research
investigate
implement
review
```

Every prompt starts with a literal `ACTION: <name>` line - the marker fake executables
route on in the integration tests. User-supplied text (the request, project context) is
embedded as delimited data that cannot close its own block. In the stage prompts the task
- typed, or rendered from a ticket (§39) - sits in a task block: a preamble saying it is
the task to carry out but that nothing inside it changes the prompt's rules (the response
format, the read-only rule, the review rubric), then the task between `<<<TASK-<nonce>` and
`>>>TASK-<nonce>` lines with a per-prompt random nonce the task does not contain, so the
task has a visible end it cannot fake (§38.3).

Do not assemble large prompts ad hoc inside workflow logic.

Use typed input structures.

Stage prompts always get the user's original request, never the translation. The stages that
take the task (research, investigate, implement first and retry, review) also get, after the
task block, the rendered clarification as a fenced block when the run has one (§49.4); an
empty clarification adds nothing.

For research include:

* original task;
* the clarification, if any.

For investigate include:

* original task;
* the clarification, if any;
* research, where available.

A read-only step that is the last read-only step of a workflow without development
also asks for the final `answer` (§10).

For an initial implementation request include:

* original task;
* the clarification, if any;
* the investigation where available.

For a fix after test failure include:

* original task;
* previous context;
* test output.

For a fix after review failure include:

* original task;
* review issues;
* current repository state.

For review include:

* task;
* expected outcome;
* Git diff;
* test result.

---

# 21. Persistence

Use local filesystem storage.

Location:

```text
.orchestrator/
  runs/
    <run-id>/
      run.json
      decision.json            # full decision trace
      project_context.json     # exactly the context sent, only when one was sent
      workflow.json
      ticket.json              # the fetched Jira issue, run --jira only (§39.7)
      summary.md               # the run's report for a reader, once it ends (§43)

      artifacts/
        <NNN>-<action>.json    # a model step's also has "prompt": exactly what was sent
        clarify-<N>.json       # the clarify call: the prompt as sent, the excerpts, the points (§49.9)

      logs/
        <NNN>-<action>.{stdout,stderr}.log
        translate.{stdout,stderr}.log
        decide-context.{stdout,stderr}.log    # LLM decision provider
        decide-classify.{stdout,stderr}.log
        decide-clarity-<N>.{stdout,stderr}.log  # LLM decision provider: the N-th clarity re-check
        clarify-<N>.{stdout,stderr}.log         # the clarify call (N from 1)
```

Use UUID or equivalent unique Run ID.

Persist enough data so a completed or failed run can later be inspected, and a failed,
stopped, planned or interrupted run can be resumed (§37). The run directory is locked by
the process that owns it (§37.9).

At minimum save:

* original task, and with `run --jira` where it came from (`task.source`, §39.7);
* the clarification, when the clarify step ran (`task.clarification`: the thresholds, every
  check's P(clear), the rounds with their points, answers and who gave them; §49.9);
* run ID;
* the decision (`run.json`'s `decision`) and its full trace (`decision.json`): the
  request and its translation, who decided (`jev`, `llm` or `user`, with the provider
  error in the last case), the model version that answered, the pre-check distribution
  and depth, the clarity checks (`clarity`: the threshold, every P(clear), the status and
  why), both complexity questions with their selections and agreement, each stage's
  probability, threshold, forced flag, enabled flag and reason, the workflow, and the
  latency and, where the provider reported one, the usage (`usage.translation`,
  `usage.context_depth`, `usage.classification`, `usage.clarify`, `usage.clarity_recheck`:
  tokens, cost, turns) of each decision call;
* workflow;
* executed steps, each AI step with its `usage` (tokens, cost, turns; `partial` for a call cut
  off before its final report, §43.3) when the CLI reported one;
* provider/model and authority for each AI step (`provider.authority`, §38);
* the restore point (`pre_run_ref`) recorded before the first step, and with it
  `head_ref` and `resume_baseline` (§38.3);
* each resume (`resumes`) and, per execution, the `attempt` that ran it (§37.10);
* step result;
* test result;
* review result;
* final run status;
* failure reason if any;
* `outcome`, written to `run.json` whenever the run reaches a terminal status (and again
  after a resume, over the whole history): `status`, `failure_reason`, `fix_iterations`,
  `tests_passed_final` (the last test step's result, absent without one), `review_verdict`,
  `duration_ms`, `usage` (summed over the steps and the decision calls, with `calls`) and
  `decision_overhead` (`cost_usd`, `ms`, `calls` of the decision rounds; zero for
  `--stage`), plus the fields of §43.3. It is derived data, a record that nothing decides
  from; `runs report` recomputes it for runs written before it existed and for runs that died
  without a terminal status;
* execution mode the run actually ran under (`mode`);
* each step's call report, an implement's diff from git, and the run's times, cost and net
  change (§43).

From `decision.json` alone it must be possible to tell why each stage ran.

No database is required.

---

# 22. Domain Model

Design clear domain types.

Suggested concepts:

```text
Task
Stage, Complexity, ContextDepth
ContextDepthDecision, TaskClassification     (provider's raw probabilities; the pre-check carries P(clear))
ClarityDecision, Clarification               (the re-check's P(clear); what was agreed, §49)
DecisionPolicy                               (thresholds, forced stages, the clarity threshold)
Decision, StageSelection                     (the policy's verdict)
DecisionTrace                                (decision.json)
ProjectContext
Run
Workflow
Step
StepExecution
Resume                                       (one `orchestrator resume`, §37)
Artifact
ProviderRef                                  (provider, model, authority)
Authority                                    (none, web, read, edit - §38)
ReviewResult
TestResult
RunMode
```

Suggested statuses:

Run:

```text
pending
running
completed
failed
stopped
```

`RunMode` (§1/§4/§27) is one of:

```text
plan
manual
accept-edits
auto
```

It selects how much user interaction happens before and during a run, and is orthogonal
to the decision and the workflow - the same decision always produces the same workflow
regardless of mode.

Step execution:

```text
pending
running
completed
failed
```

Keep domain structures independent from CLI/UI concerns.

---

# 23. Errors

Use typed/sentinel categories or equivalent clean error wrapping for:

```text
ConfigError
DependencyError
ProviderError
DecisionError
WorkflowError
RepositoryError
ExecutionError
LimitExceededError
AuthorityError
RunStateError
JiraError
```

Preserve underlying errors.

`JiraError{Key, Kind, StatusCode, Attempts, Detail, Body, Err}` is a failed fetch for
`run --jira` (§39.9). `Kind` is one of `auth`, `not_found`, `redirect`, `timeout`,
`connection`, `rate_limited`, `unavailable`, `invalid_response`, `cancelled`;
`Kind.Transient()` drives the retries. `Detail` names the credential variables, the
redirect's host, the attempt timeout or the offending JSON path. `Body` is a bounded,
redacted excerpt. No credential ever appears in its text.

`AuthorityError{Provider, Route, Authority, Reason, Remedy}` means a provider cannot
enforce the authority a call requires (§38); it is raised by `ValidateAuthorities` before
any AI call and again by an adapter's `Execute` as a backstop.

`RunStateError{Kind, RunID, Msg, Err}` is a problem with a persisted run rather than with
the config, a provider or the workflow: `not_found`, `ambiguous`, `in_use`,
`not_resumable`, `corrupt` (§37.11).

`DecisionError{Provider, Kind, StatusCode, Attempts, Body, Err}` is every decision
provider failure, Jev or LLM (§8.4, §8.5). `Kind` is one of `timeout`, `connection`,
`rate_limited`, `unavailable`, `auth`, `bad_request`, `invalid_response`, `cancelled`;
`Kind.Transient()` drives Jev's retries. `Body` is a bounded excerpt of the response (or
of the model's output), never containing the API key. An LLM provider's CLI failure is
wrapped, so the underlying `ProviderError` stays inspectable.

Provider CLIs do not consistently report failures on stderr - e.g. `claude --print`
writes backend/API errors (bad model, connectivity failure) to **stdout**. A
`ProviderError` that only carried `Stderr` therefore renders an empty, useless message
for exactly the failures users hit most. `ProviderError` carries both streams, a
`FailureKind` describing *how* the call failed, and enough context to reproduce the
call:

```go
type FailureKind string

const (
    FailureSpawn     FailureKind = "spawn"     // executable not found / not executable
    FailureExit      FailureKind = "exit"      // ran, exited non-zero
    FailureTimeout   FailureKind = "timeout"   // execution.timeout exceeded
    FailureCancelled FailureKind = "cancelled" // SIGINT/SIGTERM
)

type ProviderError struct {
    Provider   string
    Action     string        // e.g. "implement"; "translate", "decide-context", "decide-classify", "decide-clarity", "clarify" outside steps
    Model      string
    Kind       FailureKind
    Executable string
    Args       []string      // prompt argument redacted to a length marker
    WorkingDir string
    ExitCode   int
    Duration   time.Duration
    Stdout     string
    Stderr     string
    LogPath    string        // run-relative path to the raw logs, when persisted
    Err        error
}
```

Rendered message: each stream independently truncated to its last 4000 bytes (with an
explicit truncation marker), and rendered as `(empty)` rather than a blank line when the
stream produced nothing:

```text
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

full output: .orchestrator/runs/2f3c.../logs/001-implement.stdout.log
```

`command:` renders the real argv verbatim; the prompt is not in it (it goes on stdin,
§41.1), and `prompt: N bytes on stdin` gives its size - the prompt body itself is never
echoed or logged anywhere. Do not dump secrets or complete environment variables.

`full output:` is only present once the failing call's raw stdout/stderr has actually
been persisted (see §21) - it points at the exact file to read for output longer than
the message's 4000-byte tail. A call that produced no output (e.g. killed by a timeout
before it printed anything) leaves no log file, so the line is omitted.

A timeout names the limit that actually fired: `actions.<name>.timeout` or
`decision.timeout` when that per-call limit was tighter than what was left of the run,
otherwise `execution.timeout`.

---

# 24. Exit Codes

Use consistent CLI exit codes.

Suggested:

```text
0 success

1 generic runtime error

2 configuration/dependency validation error

3 provider execution error

4 workflow error / limits exceeded (also a run that stopped `task needs input`, §49.6)

5 user cancellation

6 completed, but needs a person (§50.6, §50.11; the table in §4)
```

Exact values may be encapsulated centrally.

A `DecisionError` maps to 3, except `auth` (2 - a key problem is a configuration
problem) and `cancelled` (5). A failed clarify call is a `ProviderError` (3; `FailureAuth`
is fatal as everywhere), and a clarification cancelled at the terminal (EOF or Ctrl-C) is a
cancellation (5). An `AuthorityError` maps to 2: routing an action to a
provider that cannot enforce its authority is misconfiguration. A `RunStateError` maps to
1, whatever it wraps. A `JiraError` maps to 2 for `auth`, `not_found` and `redirect`
(the operator's credentials, key or `base_url`), 5 for `cancelled`, and 3 otherwise.

---

# 25. Timeout and Cancellation

Support:

```yaml
execution:
  timeout: 15m
```

`limits.max_cost_usd` (§12) is the cost bound next to this time bound: it is checked before
each step and cannot interrupt a call, so `execution.timeout` still bounds a single hanging
call.

`execution.timeout` bounds the entire run, not just workflow step execution: it wraps
opening the repository, the decision phase (translation, both decision rounds, the clarify
step and its re-checks, context collection), and the full engine run. A provider call that
hangs while deciding is
bounded by the same timeout as one that hangs during an implement step - there is no
separate, un-timed phase. The one thing not counted is the time a user spends on a
manual choice (§8.6) or on the clarify questions (§49.5): they wait on a context without the
deadline, and the workflow gets that time back. `decision.timeout`, `translation.timeout`
and `clarify.timeout` bound each of their calls within the run's deadline. A resume has no decision phase: its `execution.timeout` is a
fresh budget that starts after approval (§37.6).

Use `context.Context` throughout the execution chain.

SIGINT/SIGTERM must cancel an active execution.

Provider processes and test processes must be terminated through context cancellation.
Each child process runs in its own process group; on cancellation the whole group is
killed, not just the direct child - a provider CLI that is (or wraps) a shell script may
fork grandchildren that inherit its stdout/stderr pipes, and killing only the direct child
would leave those running, holding the pipes open and defeating the timeout.

## 25.1 Per-Action Timeout Override

Support an optional `timeout` alongside `provider`/`model` on each of the four AI-backed
actions (`research`, `investigate`, `implement`, `review`; `test` has no provider
entry and runs under `execution.timeout`):

```yaml
actions:
  review:
    provider: claude
    model: sonnet
    timeout: 20m
```

Without an override, an action's behavior is unchanged: it runs under whatever remains of
`execution.timeout`. With an override, the action's context is wrapped in its own
`context.WithTimeout`, nested inside the run-wide context - by ordinary
`context.WithTimeout` semantics this can only tighten the effective deadline, never extend
it past `execution.timeout`. This does not create a second un-timed phase or contradict
§25's "no separate, un-timed phase" invariant: the run-wide bound still applies to
everything, including an overridden action; the override only lets a specific action (most
usefully `review`, which inspects the full git diff and can be the slowest step) claim a
dedicated slice of that budget instead of being at the mercy of how much earlier steps
happened to leave behind.

A negative override fails config validation (`actions.<name>.timeout must not be
negative`). A resulting timeout failure's error message names which limit fired -
`actions.review.timeout = 20m` vs. the run-wide `execution.timeout` - so a report of
"context deadline exceeded" is attributable to a specific, tunable cause.

## 25.2 Waiting for a Usage Limit

```yaml
execution:
  rate_limit_wait: 5m   # default; 0 fails the run at once
```

A step the account's usage limit refused does not end the run. The Claude adapter recognizes
`api_error: "usage_limit_reached"`; the Codex adapter recognizes its JSONL rate-limit error
and `codex.rate_limits` events. A generic org policy, server throttle or ordinary API error
is not converted to this retryable kind. The adapter reports `FailureRateLimit` with the
reset time when the CLI provides one. The shared tail
(`executeWorkflow`) then writes the run's outcome as failed - a process killed while waiting
leaves a run `resume` continues - and sleeps until the reset plus one minute, or
`rate_limit_wait` when the CLI gave no reset time. Nothing is called while it sleeps.
Afterwards it runs what a resume checks first (the git fingerprint, the protected paths,
HEAD; §37) and enters the engine again, which retries the refused step (`workflow.Entry`)
with a fresh `execution.timeout`: the wait is not the steps' time. A step refused again
waits again. SIGINT/SIGTERM during the wait leaves the run failed and resumable.

---

# 26. Project Structure

```text
cmd/
  orchestrator/
    main.go

internal/
  app/              composition root: fail-fast, wiring, the run
  config/           schema, loader, validation, dependency checks
  domain/           plain data types (no I/O)
  translate/        language detection and translation (§8.1)
  decision/         Provider interface, shared questions and state (§8.2)
    jev/            Jev HTTP client, offline mock, provider, response mapper (§8.4)
    llm/            LLM decision provider over a CLI (§8.5)
  projectctx/       Context Manager (§8.3) and the clarify step's instructions (`Docs`, §49.4)
  policy/           thresholds → decisions (§9); imports only domain
  classification/   the decision flow, the clarify loop and the manual-choice fallback (§8, §49)
  jira/             read one Jira issue: ParseRef, the client, Render (§39); imports only domain, apperr
  workflow/         builder, engine, transitions; imports only domain
  provider/         CLI provider abstraction, registry, claude/codex/gemini adapters
  execution/        process executor
  repository/       git (read-only helpers)
  action/           step executors and prompt builders
  testexec/         test runner
  guard/            the protected-paths and git-fingerprint guard (§38.3); aliased in action
  verify/           the in-session verify MCP server behind `mcp-verify` (§44.3); standard library, guard, testexec, domain
  testreport/       failing test names out of a test step's output (§44.7); standard library and domain only
  report/           runs report and decisions report over the persisted runs (§4); imports only domain
  interaction/      approval, mode select, manual-mode gate, manual choice, the clarify questions
  persistence/      run directory
  logging/          console output
  apperr/, exitcode/
```

Jev's transport types stay unexported inside `internal/decision/jev`, and Jira's inside
`internal/jira`.

---

# 27. CLI Output

Provide readable progress.

Example (`--mode auto` - no prompts):

```text
$ orchestrator run "Fix retry logic. 404 must not be retried" --mode auto

Validating configuration...
✓ configuration valid
Run: 2f3c1a8b  (.orchestrator/runs/2f3c1a8b)

Deciding the workflow...
context: project (p=0.91 ≥ 0.50)
complexity: medium (choice p=0.63, score=1.20 → medium ✓)
decision: jev (jev-1.13.0)
workflow: investigate → implement → test → review
authority: investigate=read, implement=edit, review=read; test runs: go test ./...
Restore point: 9c1e0f4d2b7a...

[1] INVESTIGATE
Provider: claude
Model: sonnet
→ running claude...
✓ completed (12.8s)

Result:
  Summary: Every non-2xx response is retried, 404 included.
  Root cause: shouldRetry only checks for err != nil.
  Steps:
    1. Retry only on 5xx and network errors.
  Affected files:
    - internal/http/retry.go

[2] IMPLEMENT
Provider: claude
Model: opus
→ running claude...
✓ completed (68.1s)

Result:
  Summary: 4xx responses are no longer retried.
  Changed files:
    - internal/http/retry.go
  Tests added:
    - internal/http/retry_test.go

[3] TEST
Command: go test ./...
✓ passed

[4] REVIEW
Provider: claude
Model: sonnet
→ running claude...
✗ review failed

Result:
  Summary: 404 handling is fixed; 429 is no longer retried either.
  Issues:
    - [high] internal/http/retry.go: 429 must still be retried

1 high severity issue found.
Returning to implementation (iteration 1/2).

[5] IMPLEMENT
Provider: claude
Model: opus
→ running claude...
✓ completed (41.3s)

[6] TEST
Command: go test ./...
✓ passed

[7] REVIEW
Provider: claude
Model: sonnet
→ running claude...
✓ PASS

Run completed successfully.
Run ID: <run id>
```

The final status block ends with the run ID (also after a failed or stopped run), so it
need not be looked up in the header.

The decision summary shows, when they apply: a `translation: ru → en via <provider>/<model>`
line (or a `Warning: translation failed (...)` line), the context depth with the
probability and threshold (plus `limited: <reason>` when collection failed), the
complexity with both questions' readings (a disagreement is flagged `✗ disagrees`), who
decided, and the workflow. The `authority:` line follows in every mode, before any prompt:
each AI step's authority and the command the test step runs (§38). `Restore point:` is
printed once the mode is settled, before the first step; with `--verbose` each AI step's
header is followed by `Authority: <authority>`. A completed step is followed by a
`Result:` block - its artifact (§21) as labelled text, empty fields left out; a review
lists its issues, a failed test its exit code and the last 20 lines of its output. In
`manual` mode the gate names the step it asks about (`Next step: [N] ACTION`), and the
gate before the step that follows a completed `implement` first lists the working tree's
changes and `Protected paths: unchanged`. `--verbose` adds the per-stage table:

```text
stages:
  research       off  p=0.05 < 0.70
  investigation  on   forced: complexity=complex
  development    on   p=0.99 >= 0.60
  testing        on   with development
  review         on   forced: complexity=complex
```

When the task is rated unclear, the lines of the clarify step come before the decision
summary (§49.5): in a terminal each point with its question and the model's answer
(`answer [Enter keeps it, - leaves it open]:`), and, if the re-check still rates the task
unclear, the open points and a prompt for an addition. A run that stops there ends with:

```text
Run status: stopped
Reason: task needs input

The task is not clear enough: P(clear) 0.55 < 0.80.
Open points:
  - Which locale is the date in?

Add what is missing to the task and start a new run.
```

Every provider call prints a line immediately before it - `→ running <provider>...` for a
workflow step, `→ <provider> (model <model>)` for translation and LLM decision rounds - so
a hang is visible in real time rather than as a silent pause. `--verbose` (the only
verbosity switch: no config key, no environment variable) additionally prints, per call:

```text
[2] IMPLEMENT
Provider: claude
Model: opus
Authority: edit
→ running claude...
  | <the CLI's live output, line by line, verbatim>
   … still running (30s)
   … still running (60s)
✓ completed (72.4s)
```

The live output is a generic tee of whatever the CLI prints - no provider-specific
parsing - and is captured into the logs (§21) as before. The heartbeat fires every 30s
while a call is in flight. Durations are always in seconds, rounded to 0.1s. The redacted
argv and the working directory are printed only in a failure's message (§23).

On an interactive terminal the same information is rendered with colour, a spinner with
elapsed time and boxed summaries, using ANSI escapes only (no terminal library);
the `  | ` prefix becomes a dim `│`. Off a terminal, or with `NO_COLOR`,
`ORCHESTRATOR_PLAIN` or `TERM=dumb`, the output is the plain text of this section, byte
for byte.

On failure, the error rendering (§23) includes a `full output: <path>` line pointing at
the persisted raw stdout/stderr for that call - there is no separate "read the logs at..."
line printed by the CLI itself.

A workflow without development ends with the reply:

```text
Run completed successfully.

Answer:
Use a token bucket: ...
```

`orchestrator resume` (§37) prints an overview before the approval prompt. It shows the
previous attempt (`running` is shown as "interrupted") and every recorded execution, with
test and review lines showing the trusted verdict. Steps a resume ran are marked with its
number. The working tree is listed once the baseline check has passed, and not at all
when it is clean. Last come where the run continues, why, and the workflow from there on,
followed by the usual `authority:` and `Restore point:` lines:

```text
Resuming run 3f2a9c1e-…  (.orchestrator/runs/3f2a9c1e-…)
Previous attempt: stopped — maximum fix iterations exceeded
History:
  ✓ 000 investigate claude/sonnet  read  completed
  ✓ 001 implement  claude/sonnet  edit  completed
  ✓ 002 test       test command         passed
  ✗ 003 review     claude/sonnet  read  FAIL (2 issues)
  ✓ 004 implement  claude/sonnet  edit  completed  (resume 1)
  ✗ 005 test       test command         tests failed (exit 1)  (resume 1)
Working tree:
   M internal/retry/retry.go
Next: implement — tests failed, retrying implement
Remaining workflow: implement → test → review
authority: investigate=read, implement=edit, review=read; test runs: go test ./...
Restore point: 8b4dd14…
```

A completed run prints `Run <id> already completed. Nothing to resume.` and its answer, if
it has one.

With `run --jira` (§39), the fetch and the whole rendered task are printed between the
configuration check and the run header, in every mode and whatever `--verbose` says:

```text
Validating configuration...
✓ configuration valid

Fetching PMC-2789 from yourcompany.atlassian.net...
✓ PMC-2789 (Bug) fetched in 0.3s

Task (from PMC-2789):
  | PMC-2789: FE[catalog]: Tags/Labels disable reason not shown on hover in the editor
  | Type: Bug
  | Parent: PMC-2663 — Tags & Labels
  | Source: https://yourcompany.atlassian.net/browse/PMC-2789
  |
  | Description:
  | ...
  |
  | Operator notes:
  | only the FE part; leave pim-qa alone

Run: 2f3c1a8b  (.orchestrator/runs/2f3c1a8b)
```

A moved issue adds `Warning: PMC-1 has moved to NEW-9; using NEW-9` after the fetched
line. Resuming a Jira run adds `Task from PMC-2789, fetched 2026-09-30 10:12 (not
re-fetched)` under the `Resuming run` line (local time).

Example (no `--mode` flag - the interactive default: approve the workflow, choose a mode,
then confirm each step under `manual`):

```text
$ orchestrator run "Fix retry logic. 404 must not be retried"

Validating configuration...
✓ configuration valid
Run: 2f3c1a8b  (.orchestrator/runs/2f3c1a8b)

Deciding the workflow...
context: project (p=0.91 ≥ 0.50)
complexity: simple (choice p=0.71, score=0.42 → simple ✓)
decision: jev (jev-1.13.0)
workflow: implement → test → review

Approve this workflow? [a]pprove / [c]ancel: a

Choose execution mode: [1] plan  [2] manual  [3] accept-edits  [4] auto: 2

Next step: [1] IMPLEMENT
Run it? [y]es / [n]o / [A]ll remaining: y

[1] IMPLEMENT
Provider: claude
Model: opus
→ running claude...
✓ completed (68.1s)

Result:
  Summary: 404 responses are no longer retried.
  Changed files:
    - internal/http/retry.go

Changes in the working tree:
 M internal/http/retry.go
Protected paths: unchanged

Next step: [2] TEST
Run it? [y]es / [n]o / [A]ll remaining: n
[r]etry / [a]bort: r

[2] TEST
Command: go test ./...
✓ passed

[3] REVIEW
...
```

Example (the decision provider is down, `on_failure: ask`):

```text
Deciding the workflow...

Decision provider failed: decision provider jev failed: unavailable (the service is overloaded or failing), HTTP 529, after 2 attempts
Choose the task's complexity and workflow stages yourself.

Project context: go (go-module), unit and integration tests, branch main
Relevant files:
  internal/http/client.go
  internal/http/client_test.go

Complexity:
  [1] simple  [2] medium  [3] complex
Complexity [1-3]: 3

Stages (run in this order):
  [1] research
  [2] investigation  * forced for complexity=complex
  [3] development
  [4] testing
  [5] review  * forced for complexity=complex
Stage numbers, comma-separated (* stages are included; -N excludes one): 3,4
complexity: complex (chosen by user)
decision: chosen by user (decision provider jev failed: unavailable ...)
workflow: investigate → implement → test → review
```

Do not make terminal formatting a major dependency of the MVP.

Plain text output is sufficient.

---

# 28. Required Automated Tests

Unit test core logic thoroughly.

At minimum cover:

### Config

* valid config;
* malformed YAML;
* missing required fields;
* unsupported provider;
* invalid limits.

### Dependency validation

* all configured clients installed;
* Claude missing;
* Codex missing;
* Gemini missing;
* provider used by several actions;
* translation and LLM decision provider missing;
* Jev API key missing (and not required with the mock or an LLM provider);
* no AI execution or HTTP request happens after validation failure.

### Decision

* Jev client: every status mapping, Bearer header, retries (529 retried once, 401
  never), `Retry-After`, cancellation, the key never appearing in an error;
* Jev requests pinned by golden files; responses mapped, every malformed shape an
  `InvalidResponse`;
* LLM provider: same questions and state as Jev, malformed answers rejected;
* translation: detection, sanity checks, fallback to the original;
* Context Manager: secrets, ignored and binary files never collected, budget respected;
* manual choice: valid input, invalid input re-asked, empty selection, EOF, no reading
  in `--mode auto`.

### Policy and workflow builder

* stage thresholds are inclusive; forced stages; nothing enabled → investigation; the clarity
  threshold is inclusive;
* complexity rule on both questions, agreement flag;
* fixed stage order regardless of input order.

### Workflow engine

* successful forward workflow;
* test failure → implementation;
* review failure → implementation;
* review failure → implementation → test → review;
* workflows without implement (research → investigation; testing only; review only);
* maximum fix iterations;
* maximum steps.

### Review

* PASS;
* critical issue;
* high issue;
* medium warning;
* low warning;
* malformed output.

### Process execution

* success;
* non-zero exit;
* stdout;
* stderr;
* cancellation.

---

# 29. Integration Test Harness

Create integration tests that do not require real Claude, Codex, or Gemini installations.

Use temporary fake executables named:

```text
claude
codex
gemini
```

Add their temporary directory to `PATH`.

Fake executables should return predefined outputs based on their arguments/input.

This must allow testing full end-to-end scenarios locally and in CI.

Required scenarios:

The decision comes from a fake Jev (`httptest`, answering by question id) or, for an LLM
decision provider, from the same fake executables.

### Scenario 1 — successful bugfix

```text
decision
→ investigate
→ implement
→ tests pass
→ review pass
→ completed
```

### Scenario 2 — review loop

```text
review fail
→ implement
→ tests pass
→ review pass
```

### Scenario 3 — test loop

```text
tests fail
→ implement
→ tests pass
→ review
→ completed
```

### Scenario 4 — missing Claude

Config validation fails before workflow execution.

### Scenario 5 — missing Codex

Config validation fails before workflow execution.

### Scenario 6 — missing Gemini

Config validation fails before workflow execution.

### Scenario 7 — provider runtime failure

Run ends as failed with provider context.

### Scenario 8 — invalid decision response

Run fails before repository-changing actions.

### Scenario 9 — max iterations reached

Workflow stops and does not invoke additional agents.

### Scenario 10 — interactive manual run

No `--mode`: the workflow is approved, `manual` is chosen, every step is confirmed;
`run.json` records mode `manual` and status `completed`.

### Scenario 11 — provider error on stdout only

The CLI exits non-zero writing its error to stdout (as `claude --print` does): the message
carries the stdout text, `failure: exit`, and `stderr:` renders `(empty)` (§23).

### Scenario 12 — LLM decision provider failure

The decision CLI fails: `logs/decide-context.*.log` is written, the error is a
`DecisionError` wrapping a `ProviderError` with action `decide-context`, exit 3.

### Scenario 13 — timeout while deciding

A decision CLI that hangs past a short `execution.timeout` fails promptly with
`failure: timeout` (§25), instead of hanging.

### Scenario 14 — unenforceable authority

An action routed to an installed provider with an unsupported authority fails the fail-fast
chain (exit 2) before any Jev request or CLI call (§7, §38).

### Scenario 15 — worktree guard

A dirty tree refuses a workflow with implement, but not a read-only one or one with
`security.require_clean_worktree: false`; a clean run records its restore point; an
implement that changes a protected path stops the run before `test` runs anything (§38.3).

### Scenario 16 — sandboxed test step

With `test.sandbox: bwrap` - or no key at all, the default - the test command runs under
bwrap after a probe in the fail-fast chain; an unusable bwrap fails fast naming
`test.sandbox: none`, and an explicit `none` is announced on every run (§38.3, §41.3).

### Scenario 20 — chosen stages

`test/integration/scenario20_stage_select_test.go` (§40): review alone on a dirty tree with
no decision layer; an empty diff fails before the model is called; flag order is
irrelevant, a test failure fails the run and a review failure completes it; development
needs a clean tree and a refused run is resumable once it is clean; the fix loop is bounded
by `max_fix_iterations`; investigation alone answers, and development without it warns; both
spellings of a stage are one step; `--jira` makes the Jira GET the only request; resume
continues a manual run and decides nothing; a missing executable fails first and `--stage`
belongs to `run` alone; plan mode shows the stages and stops.

### Resume scenarios

`test/integration/scenario17_resume_inputs_test.go` and
`scenario18_resume_*_test.go` (§37): a failed step is retried with its restored context;
limits count across resumes; a plan-mode run is executed later; an interrupted run
continues; a protected-path change, a planted `core.fsmonitor` (no `git status` runs) and
a moved HEAD are refused; a tampered `run.json` or artifact is `corrupt`; implement cannot
rewrite resume inputs; a completed run is reported; run-ID prefixes resolve or are
refused; a locked run is `in_use`. No refusal calls a provider or changes `run.json`.

### Scenario 21 — Jev model check

`test/integration/scenario21_jev_model_check_test.go` (§7): an unserved model is exit 2
with no run, no decision round and no CLI call; a listed alias and an unlisted pinned
version pass; an unreachable models list warns with `on_failure: ask` and stops with
`fail` (exit 3, retried once, no run); `validate` prints the model and rejects an
unserved one; `run --stage` asks nothing. A rejected key is caught by this check, before
any decision round (`TestDecision_JevAuthFailure`).

### Scenario 22 — decisions report

`test/integration/scenario22_decisions_report_test.go` (§4): with no runs the report says
so; after an agreeing run, a disagreeing run and a `--stage` run it counts 3 traces, 2
compared, 1 skipped, 50% agreement and lists the disagreement - without a config file,
and without calling a CLI or Jev.

### Hardening scenarios

§41: scenario 16c/16d (the sandbox is the default; `none` is announced), 23 (a 300 KiB task
reaches the CLI on stdin), 24 (an unverified CLI is refused before any call; the override
runs and records), 25 (the test command's allowlisted environment; a credential-like
`env_pass` is refused), 26 (the pre-run test: a red suite stops the run, `--allow-red-tests`,
green, `off`, the guard, resume), 27 (the secret scan: stop, warn with redaction, a key
written by the tests, a dirty-start baseline, off), 28 (what review and a retried implement
are shown: new files, lock files, the deny list, both budgets).

### Jira scenarios

`test/integration/scenario19_jira_*_test.go` (§39), against a fake Jira on loopback: the
ticket is fetched once and becomes the task of every stage and of the decision, with
`task.source` and `ticket.json` written; operator notes come last; a retried 503; a
missing issue, a missing token and a foreign-host URL are exit 2 with no run and no AI
call (the last two with no request); a resume makes no request and needs no Jira
variables; neither the test command nor a provider CLI inherits `JIRA_API_TOKEN`,
`JIRA_EMAIL` or `JEV_API_KEY`; a `jira` section without `--jira` changes nothing.

The listener (§51) has its own scenarios (`test/integration/listen_test.go`,
`listener_task_test.go`) against a fake Jira, extended with a search, a
comments endpoint, `/myself` and transitions, and a fake GitLab remote: a labelled ticket is
claimed, cloned, run as a child and ends in a comment and the `done` transition; a ticket from a
reporter outside `query.reporters`, or of a foreign project, is never dispatched; an unknown or
ambiguous service and an unlisted group are `blocked` with a comment and no run; a clarify stop
is `blocked` with the numbered points, a red run `failed`; a provider failure is `retry` with a
backoff and no attempt counted; a spent ticket is commented once; a restart resumes a `running`
ticket from the state; a second listener in the same directory refuses; the GitLab token appears
in no argv, URL, `.git/config`, log, comment or child environment.

### Decision scenarios

`specs/JEV integration/TASKS.md` T13: project context without secrets, depth none
without context, forced stages, a failing Context Manager, translation and its failure,
Jev down with a manual choice, Jev down in `--mode auto`, a rejected key, a missing key,
choice and score disagreeing, and an LLM decision provider.

### Clarify scenarios

`test/integration/clarify_test.go` and `clarify_scenarios_test.go` (§49), with the fake claude
CLI as the decision provider so a re-check can be scripted (`decide-clarity_<N>.json`): a clear
task makes no clarify call and records no `task.clarification`; an unclear task at a simulated
terminal is clarified by the operator (Enter keeps a docs answer, text answers a point) and the
investigate prompt holds what was agreed; `--mode auto` with documentation that answers the
question goes on, and without it stops `task needs input` with exit 4, no `decision.json`, the
open points in `summary.md`, and a `resume` that refuses; a failed clarify call fails the run;
`clarify.enabled: false` makes no clarify call whatever the probability and records
`clarity.status: disabled`; `run --stage development` on an unclear task runs clarify and stays
`source: manual`; a story task never calls clarify; a config that still names `analysis` or
`planning` is exit 2 before any call and creates no run; the clarify call has an empty
`--tools` list (authority `none`).

---

# 30. Out of Scope

Do not implement:

```text
HTTP provider APIs (except the Jev decision API, reading Jira issues, and the Jira calls of
  the listener, §2, §51)
OpenRouter
LiteLLM
model benchmarking
automatic model selection
cost optimization
automatic provider fallback
parallel workflows (a story runs up to `story.max_parallel` ready tasks at once, each its own
  run, and one run's workflow stays sequential, §48.6)
multi-agent parallel execution
Web UI
REST API
IDE plugin
GitHub integration; GitLab beyond clone, fetch and the push of the run's branch, which opens
  one merge request (§50.6): no API client, and a token only for the git credential helper of
  the listener (§51.6, §50.13); Jira beyond reading one issue for `run --jira` (§39), the child
  issues of an approved story (§48.7), a comment and a transition after a merge request
  (§39.12), and, for `jira listen` only (§51), a JQL search, reading comments and `/myself`,
  and comments and transitions without a merge request (no links or worklogs)
remote repository execution
RAG
persistent conversational memory
database
custom dynamically defined action types
arbitrary user-defined workflow language (a story's spec is a list of tasks, not one: it names
what to change and where, never stages, models or tools, §48)
a mode that bypasses provider permission checks
per-action configurable authority or a permission policy language
per-tool-call approval during a step
OS-level sandboxing of provider CLIs (the test step's optional bwrap sandbox, §38.3, is
the one exception)
```

The one thing near "model benchmarking" that exists lives in `bench/`, outside the core: a
separate Go module (`orchbench`, `bench/README.md`, design in `specs/benchmark/`) that runs
`orchestrator` and bare Claude Code as black boxes over replayed commits and reports pass
rate, cost and time. It imports nothing of the core, reads only what a run writes (`run.json`,
`decision.json`), and never selects, routes or changes a model; the core records what a run
cost and how it ended (§21) because an operator needs that too.

Avoid introducing abstractions only required for these future features.

---

# 31. Important Design Decisions

Keep these decisions unless implementation reveals a concrete blocker.

### 31.1 Static provider routing

Models are selected from config:

```yaml
implement:
  provider: claude
  model: opus
```

Do not implement automatic "best model" routing. The decision provider is chosen the same
way (`decision.provider`); a failing provider leads to the manual choice (§8.6), never to
another provider. The clarify step has a static route of its own (`clarify.provider/model`)
and a failure of it fails the run (§49.3).

A static table in config, keyed by the decided complexity, is still static routing:
`actions.<name>.by_complexity` maps `simple | medium | complex` to a model and/or effort of the
same provider, Go picks the entry once per step from `decision.complexity`, and the same
decision always gives the same models (§44.8). A run started with `--stage` has no complexity
and uses the action's own model. Nothing picks a model from cost so far or a model's own
answer, and there is no fallback.

The one model a failure changes is `actions.implement.escalate: true` (opt-in,
`specs/decision-shape/SPEC.md` §8): an `implement` that fixes a failed test or review - the
last step before it that is not an implement is a test or a review, read from `run.json` -
runs on the `by_complexity` entry one level above the decided complexity, recorded as
`model_source: escalated.<level>`. It is the same static table, keyed by the attempt as well
as the level: the failure is the test command's exit code or the review verdict, never a
model's opinion, the provider never changes, and a resume derives the same route from the
same history. Complex, a level above without an entry and `--stage` keep their route.
A `by_complexity` entry of implement can name its own fix route instead, `escalate: {model,
effort}` (`specs/decision-shape/SPEC.md` §8.1): a fix at that level runs on the entry with the
escalate's fields over it, recorded as `model_source: by_complexity.<level>.escalate`, without
`escalate: true` and for complex too. It is the same table with one more column.

### 31.2 Deterministic workflows

The decision provider returns probabilities.

Go code - the policy and the workflow builder - decides and builds the workflow.

Do not let an LLM or a decision provider invent workflow stages or their order.

### 31.3 AI clients modify repository

Claude/Codex/Gemini coding clients may operate on the repository according to their normal CLI capabilities.

The orchestrator does not need to create patches itself.

### 31.4 Real test execution

Never trust text like:

```text
"all tests should pass"
```

Only actual process exit code determines test status.

Test names parsed out of a failed run's output (§44.7), and the split of them into failures
that were there before the run and new ones, are a hint in a prompt. They never decide
pass/fail, a transition or a stop. The in-session `run_tests` of the verify tools (§44.3)
is a hint to the model too: the `test` step stays the source of truth.

### 31.5 Review is independent validation

Review must inspect the current Git diff rather than only implementation summaries.

### 31.6 Fail fast on missing clients

Missing configured CLI executable is a configuration/dependency error and must prevent the entire run from starting.

---

# 32. Implementation Sequence

*Historical: the original delivery sequence. The classifier (step 11) and the fixed
workflow table (step 12) were later replaced by the decision flow; its sequence is in
`specs/JEV integration/TASKS.md`.*

Implement in vertical slices.

Recommended order:

```text
1. bootstrap Go CLI

2. config structures + YAML loader

3. configuration validation

4. configured-provider CLI dependency validation

5. generic process executor

6. common AI Client interface

7. provider registry

8. Claude adapter

9. Codex adapter

10. Gemini adapter

11. classifier + strict structured output parsing

12. deterministic workflow builder

13. workflow state machine

14. repository Git integration

15. action prompt builders

16. structured artifacts

17. implementation action

18. test runner

19. review action

20. test/review backward transitions

21. workflow limits

22. filesystem run persistence

23. CLI progress output

24. integration test harness with fake executables
```

Do not build every abstraction first and connect them at the end.

After steps 1–12, ensure there is already an executable vertical flow.

---

# 33. First End-to-End Milestone

Prioritize this exact scenario first:

```text
User task
 ↓
configuration validation
 ↓
classification
 ↓
investigate
 ↓
implement
 ↓
go test ./...
 ↓
review
 ↓
done
```

Then add:

```text
review FAIL
 ↓
implement
 ↓
test
 ↓
review
```

and:

```text
test FAIL
 ↓
implement
 ↓
test
```

Only after these flows work should you spend time improving secondary UX.

---

# 34. Definition of Done

*Historical: the original MVP checklist. The classifier items are superseded by §8–§9.*

The MVP is complete when all of the following are true:

* [ ] application is implemented in Go;
* [ ] `go build ./...` passes;
* [ ] `go test ./...` passes;
* [ ] CLI supports `run`, `validate`, and `version`;
* [ ] YAML configuration is supported;
* [ ] only Claude, Codex, and Gemini providers are accepted;
* [ ] providers/models can be assigned per action;
* [ ] classifier provider/model is configurable;
* [ ] all configured provider executables are validated before any AI call;
* [ ] missing Claude executable produces configuration/dependency error;
* [ ] missing Codex executable produces configuration/dependency error;
* [ ] missing Gemini executable produces configuration/dependency error;
* [ ] dependency validation is unit-testable without changing the real machine `PATH`;
* [ ] classifier returns and validates structured task classification;
* [ ] workflow is constructed deterministically in Go;
* [ ] workflow engine supports forward transitions;
* [ ] workflow engine supports `test → implement`;
* [ ] workflow engine supports `review → implement`;
* [ ] workflow loops are limited;
* [ ] Claude adapter implements common provider interface;
* [ ] Codex adapter implements common provider interface;
* [ ] Gemini adapter implements common provider interface;
* [ ] provider process calls use context and safe argument passing;
* [ ] project tests are actually executed by the orchestrator;
* [ ] test success is based on process exit code;
* [ ] review uses current Git diff;
* [ ] review response is structured and validated;
* [ ] critical/high review issues trigger implementation retry;
* [ ] medium/low review issues are retained as warnings;
* [ ] existing uncommitted Git changes are never automatically reverted;
* [ ] every run has an ID;
* [ ] run state and artifacts are persisted locally;
* [ ] failures contain actionable provider/action context;
* [ ] SIGINT/SIGTERM cancel active execution;
* [ ] end-to-end tests use fake Claude/Codex/Gemini executables;
* [ ] no real AI provider access is required for automated tests.

---

# 35. Expected Delivery

Produce:

```text
working Go source code
go.mod / go.sum
example configuration
README with setup and usage
unit tests
integration tests
```

Include an example config such as:

```text
orchestrator.example.yaml
```

README must explain:

1. prerequisites;
2. how Claude/Codex/Gemini CLI discovery works;
3. how to configure providers per action;
4. how to run `validate`;
5. how to run a task;
6. where Run artifacts are stored;
7. MVP limitations.

---

# 36. Implementation Guidelines

Follow idiomatic Go.

Prefer:

```text
small explicit interfaces
dependency injection at process/filesystem boundaries
errors.Is/errors.As compatible wrapping
table-driven tests
context propagation
simple domain models
```

Avoid:

```text
large generic framework abstractions
reflection-heavy designs
global mutable state
provider-specific conditionals spread through workflow code
shell command construction through string concatenation
premature plugin architecture
```

When requirements are ambiguous, choose the simplest implementation consistent with this specification.

Do not expand MVP scope without a concrete technical necessity.

The priority order is:

```text
correct workflow behavior
>
safe/fail-fast execution
>
testability
>
clear architecture
>
CLI polish
```

---

# 37. Resume by Run ID

Normative. Design record, gaps found, decisions and the plan as built: `specs/resume/`
(`SPEC.md`, `tasks/`).

A run that failed, was stopped (Ctrl-C, a manual-gate abort, a limit, a cancel at
approval), was only planned (`--mode plan`) or whose process died can continue where it
stopped. Resume **continues and never re-decides**. It reuses the persisted decision and
workflow, derives where to continue from the history and the existing transition table,
rebuilds the prompt context from the artifacts, and runs the same engine. Changing the
task, decision or workflow means a new `run`.

## 37.1 CLI

```bash
orchestrator resume <run-id>          # the full ID, or a unique prefix of at least 8 characters
orchestrator [--config p] [--mode m] [--verbose] resume <run-id>
```

`--config`, `--mode` and `--verbose` behave as for `run` (§4). The **current** config
applies to everything that runs from now on: routing, models, timeouts, limits, test
command, sandbox. Decision thresholds are irrelevant, because nothing is decided. Each step
records its own `provider`/`model`/`authority`, so a routing change is visible in
`run.json`. Anything other than exactly one argument is a usage error (exit 2).

## 37.2 Order of operations

`app.resumeTask` (`internal/app/resume.go`). No translator, decision provider or
project-context manager is constructed. Until the operator approves, the persisted run is
left byte-for-byte as it was found, so no refusal, cancel or plan-mode answer changes it.

```text
fail-fast chain (§7), with the current config
→ repository.Open                                    (rev-parse only)
→ Store.ResolveRunID                                 exact ID, else unique prefix ≥ 8
→ Store.Lock                                         flock on the run directory
→ Store.LoadRun                                      strict validation (§37.4)
→ workflow.Verify                                    workflow == Build(decision.Stages)
→ completed: Dispatcher.Restore, "already completed", the answer again, exit 0
→ a step has run: resume_baseline present and well-formed
→ no step has run: git status, clean-worktree rule (§38.3), entry = first step
  otherwise:
    workflow.Entry                                   StateDone → marked completed, exit 0
    → action.CheckBaseline                           git fingerprint FIRST, then protected paths
    → HEAD == head_ref
    → pre_run_ref is a commit                        (Repository.CommitExists)
    → workflow.CheckLimits                           limit pre-check (§37.6)
    → Dispatcher.Restore                             prompt context from the artifacts (§37.5)
    → Repository.StatusSummary                       working tree, for the overview
→ overview: header, history, working tree, next step, remaining workflow (§27)
→ executeWorkflow (shared with run): authority line, restore point,
  approval + mode (§37.7)
  → append a Resume record, status running, failure_reason cleared, save
  → no step has run: restore point, head_ref, resume_baseline recorded (§38.3)
  → restore point pinned under refs/orchestrator/runs/<id> (§38.3)
  → execution.timeout starts, sandbox, step guard
  → no step has run: pre-run test (§41.5), secret-scan baseline (§41.6)
  → Engine.Run
```

`run` and `resume` share everything after the decision (`executeWorkflow` in
`internal/app/app.go`), so a resumed step runs under exactly the checks of a fresh one.
Nothing on the resume path calls an AI provider or makes an HTTP request before approval.

## 37.3 Resumability

| persisted state | resume does |
|---|---|
| `completed` | reports it, prints the final answer again if there is one, exit 0, no provider call |
| no workflow (the decision failed, or the run was cancelled before one existed) | refuses: `not_resumable`, start a new run |
| `stopped` with `task needs input` (§49.6) | refuses: `not_resumable`, "nothing to resume: the run stopped before its workflow"; the missing details go into a new run's task |
| a workflow with `analyze` or `plan`, or a decision with `analysis` or `planning` (a run started before `specs/clarify`) | refuses: "this run was started before specs/clarify; it cannot be resumed by this version" (`not_resumable`, not `corrupt`: the files are intact); finish such runs with the version that started them |
| a workflow, no executions: `pending` (plan mode, or crashed before the first step), `stopped` "cancelled by user", `failed` for a dirty tree | the tail of a fresh run: clean-worktree rule, restore point, baseline, approval, engine from the first step |
| `failed` / `stopped`, at least one execution | continues at the entry state (§37.5) |
| `running`, lock free | the previous process died: handled like `failed`, shown as "interrupted" |
| `running`, lock held | refuses: `in_use` |

`run` saves `run.json` (with the workflow), `decision.json` and `project_context.json`
before the dirty-tree refusal, so a refused run is resumable once the tree is clean.

## 37.4 Loading and validation

The run directory is inside the worktree, and a model's step can write there, so what is
read back is input, not trusted state. `persistence.Store.LoadRun` refuses (`corrupt`),
and never repairs:

* unknown JSON fields, trailing data, unknown status/mode/action/authority values;
* an `id` that differs from the directory name;
* `executions[i].index != i`, an execution that is not `completed`/`failed`, a negative
  `attempt`;
* an `artifact_path` other than the derived `artifacts/<NNN>-<action>.json`. Paths are
  always derived from index and action; the stored one is only compared.

`workflow.Verify` then refuses a workflow that is not `workflow.Build(decision.Stages)`,
or an execution of an action outside it. A run with executions but no `resume_baseline`
predates resume support and is `not_resumable`. A `pre_run_ref` that is not a full commit
SHA naming a commit in the repository is `corrupt`; it is checked only after the
fingerprint check. Run IDs are hex and dashes only, so an argument can never name a path
outside `.orchestrator/runs/`.

A run with a `worktree` record (§47) is loaded the same way, and the record is validated
strictly: `branch` is `orchestrator/<run-id>`, `base_ref` a full SHA, `admin_dir` lies directly
under `<common dir>/worktrees/`. The worktree's path is derived from the run ID (§47.2); the
stored `path` is only compared, never read as a path to use. `options.isolation` decides the
mode, not the current config.

## 37.5 Entry state and prompt context

`workflow.Entry(run)` is a pure function of the history. `Engine.Run` always enters
through it; a fresh run is the empty-history case.

| last execution | entry |
|---|---|
| none | the first step ("start") |
| `failed` (a hard error: provider, timeout, malformed output, guard) | the same action again ("retry <action> (failed: …)") |
| `completed`, `Next` gives a step | that step ("continue after <action>", or the transition's reason, e.g. "tests failed, retrying implement") |
| `completed`, `Next` gives done | nothing left: the run is marked completed, exit 0 |
| `completed` test that failed, no implement in the workflow | that test again (the operator presumably fixed something) |

A completed step's outcome comes only from trusted persisted results: `test_result.success`
(the exit code) and `ReviewResult.Decide()`, never the model's own `status` (§19).
A review that passes with a `medium` issue is a third outcome, `fixable`, derived from the
review result, the run's recorded `options.review_fix_medium` (never the current config) and
the history; it goes to implement if the workflow has it (§44.4). A run `stopped` for a reason
that starts with `no progress:` resumes into implement, and so does one whose approved resume
cleared that reason (`Resume.FromReason`): the operator choosing to resume means "try again",
and `max_fix_iterations` still applies (§44.6). `workflow.NoProgress` reads only the persisted
executions (`tree_after`, `failed_tests`, review issues), so a live run and a resume agree.

`(*action.Dispatcher).Restore(run, store)` replays the context changes of every completed
execution in order, through the same `apply*` methods the live steps use: research and
investigation artifacts; implement sets the last implementation and clears the
pending retry context; a failed test becomes the pending test failure; a failed review
leaves its issues pending. Failed executions change nothing, as live: a failed implement
did not consume the retry context, and its retry gets the same one. Research, investigate
and implement artifacts are required and decoded strictly. The clarification is read back from
`run.json` (`task.clarification`, validated strictly) and given to the stages as a fresh run
gives it; nothing is asked or called again. Test and review results
come from `run.json`, and their artifact, when present, must agree. Anything missing or
inconsistent is `corrupt`. A resume never continues with a thinner prompt.

## 37.6 Limits and timeout

* `max_steps` and `max_fix_iterations` (§12) count the **whole** history, across resumes.
  `workflow.Counters` derives `total_steps` (`len(executions)`) and `fix_iterations`
  (completed tests/reviews that failed, in a workflow with implement, and a `fixable` review
  that went back to implement) from the history. The persisted counters are not trusted.
* `limits.max_cost_usd` (§12, §45.2) is checked against the cost of every persisted execution,
  so a resume counts what was already spent; a run stopped by the budget continues once the
  budget is raised.
* `workflow.CheckLimits` refuses before the approval prompt when the entry would exceed a
  limit (`LimitExceededError`, exit 4). Its message states the used and configured
  values, and the config key to raise. The engine applies the same check when it starts
  with a history. A limit-stopped run therefore continues only after the limit is raised.
* `execution.timeout` is a fresh budget for the resuming process. It starts after
  approval, when the engine phase begins. `actions.<name>.timeout` applies per step as
  always.

## 37.7 Mode

`--mode` wins. Without it, the approval prompt is shown, then the mode prompt. An empty
answer there picks the run's persisted mode, unless that is `plan` or empty. A persisted
`auto` is offered, never applied silently: an unattended resume needs `--mode auto`,
because the operator may be elsewhere now. In `manual` the gate applies to every
remaining step. Choosing `plan` on resume prints "Plan approved. Not executing" and
returns, without changing the run. Authority never depends on the mode (§38).

## 37.8 Safety on resume

Before anything runs on a tree a model last touched (§38.3):

1. **Git fingerprint first.** `GitFingerprint` (rev-parse and file reads only) is compared
   with `resume_baseline`'s. Until it matches, nothing but `rev-parse` runs in the
   repository, because `git status` would execute a planted `core.fsmonitor`.
2. **Protected paths.** They are compared with `resume_baseline`, using the patterns the
   baseline was taken with. Any difference refuses the resume, naming the paths and the
   restore point. A step the guard failed left its files on disk, and a crash
   mid-`implement` or mid-`test` skipped the guard. Only a tree back at its run-start
   protected state may continue. To keep such a change, start a new run.
3. **HEAD** must equal `head_ref`, because review diffs against HEAD. A moved HEAD is
   refused, and the message shows both SHAs (`git reset --soft <head_ref>` restores it
   with the changes kept).

The clean-worktree rule applies only to a run that has not executed a step. After that,
the tree is dirty by design, and the baseline protects it. Changes to non-protected files
between the stop and the resume are allowed. The overview lists the working tree, and
review reads the real diff, so manual fixes are reviewed like the model's.

**A worktree run** (§47) resumes in its worktree, and the three checks above run there. Before
any git command in it, the `.git` pointer is read in Go and compared with `admin_dir`, and
`git worktree list --porcelain` in the operator's repository must list the derived path on
`refs/heads/orchestrator/<run-id>`. A missing worktree is a `RunStateError`: `the run's worktree
<path> is gone; start a new run`. The operator's tree may be dirty and on any branch: it plays
no part. `worktree.setup` never runs on a run that has executions.

## 37.9 Locking

`persistence.Store.Lock` takes `flock(LOCK_EX|LOCK_NB)` on the run directory's own file
descriptor, held until the process ends and released by the kernel on a crash. There is no
lock file to delete. `run` takes it right after creating the run, and `resume` takes it
before loading it.

## 37.10 Persistence

`run.json` gains (all omitted when empty):

```text
head_ref          HEAD when the first step started
resume_baseline   the guard's state then: {patterns, git_fingerprint, protected}
resumes[]         append-only: at, from_status ("running" = interrupted), from_reason,
                  entry, why, mode
executions[].attempt   0 for the original process, n for the n-th resume
options.path_rules     policy.paths as the run started (§45.1); a resume applies these
org_config        {path, sha256} of the org file the run started under; each resumes[]
                  entry has its own (§45.3)
```

A resume reads `options.path_rules` and never the current `policy.paths`. It loads the config
through the org layer like a run does; if the org file differs from the one the run started
under, it warns (`org config changed since the run started`) and continues under the current
one.

`executions` and `resumes` are append-only. A resume clears `failure_reason` when it
starts; the previous reason is kept in its `resumes` record.

## 37.11 Errors

`apperr.RunStateError{Kind, RunID, Msg, Err}` (§23), exit 1:

| kind | when |
|---|---|
| `not_found` | no run matches, a prefix under 8 characters, or not a run ID |
| `ambiguous` | the prefix matches several runs (all are listed) |
| `in_use` | another process holds the lock |
| `not_resumable` | no workflow, including a run that stopped `task needs input`; a run predating resume support; a run started before `specs/clarify` |
| `corrupt` | §37.4 validation, or a missing or invalid artifact |

Protected-state and HEAD refusals are `WorkflowError`, and the limit pre-check is
`LimitExceededError`. Both are exit 4, the same category as at run time. Config and
dependency failures are exit 2, as for `run`.

## 37.12 Not covered

* A run with no workflow is a new `run`. Editing the task, decision or workflow on resume
  is also a new `run`.
* No migration: runs written without `resume_baseline` are refused, not converted.
* A step interrupted mid-call starts again from the beginning. Provider sessions are not
  resumed.
* Deferred (`specs/resume/README.md`): a checkpoint per step to show "changed since the
  stop", review against `head_ref` so a moved HEAD is resumable, and `runs` / `show`
  commands.

---

# 38. Execution Authority

Normative. Design record, findings, rejected alternatives and live checks:
`specs/permissions/` (`SPEC.md`, `DECISIONS.md`).

## 38.1 Authorities

Every provider call runs with exactly one **authority** - a fixed set of tools, chosen by
Go code per call site, never by a model, a provider default or config:

```text
none   no tools: text in, text out
web    web search and fetch; no file access at all
read   read and search files under the working directory
edit   read + create and modify files under the working directory
```

No authority includes a shell. A step is restricted by the tools it is given, not by rules
about how it may use them; rules that constrain a shell tool's arguments are never a
boundary here.

| call | authority | why |
|---|---|---|
| translation, decide-context, decide-classify, decide-clarity | `none` | work on the request text in the prompt |
| clarify | `none` | works on the request and the instruction excerpts Go put in the prompt; with no tools it cannot open a file, so "do not read the code" is enforced by the authority and not by the prompt; passed explicitly like the translator's, never through `AuthorityFor` |
| research | `web` | external knowledge; its prompt carries only the task, so no repository data can leak |
| investigate, review | `read` | inspect the repository; review also gets the diff in its prompt |
| implement | `edit` | the only step that changes code; compiling and testing is the test step's job (opt-in: the two verify tools below) |
| test | - | no AI call: the orchestrator runs `test.command` itself |

No call has both repository access and a way out (network, shell). An action without an
entry in `domain.AuthorityFor` gets `none`. The clarify call has no repository access and no
way out: it runs with `execution.ScrubbedEnv()`, the prompt on stdin, and its `apiKeySource`
is checked like every claude call (`FailureAuth` is fatal). The text it reads is chosen by Go
(§49.4), with the deny list applied to every path segment before anything is read, and the
decision provider never sees it.

**Replace (always on, §44.3, D18).** Under `edit`, when the adapter speaks MCP (claude, codex),
the session also gets `mcp__orch__replace` from the same `orch` server: one call rewrites a
literal string across the files git lists, never a deny-listed, protected, ignored, binary or
large file and never outside the repository. It runs no process; the guard checks the
before-state before every call.

**Verify tools (opt-in, `actions.implement.verify: true`, §44.3).** Under `edit` the session
gets two more tools from an MCP server named `orch` that the orchestrator itself serves,
`mcp__orch__run_tests` and `mcp__orch__run_check`. This is not a shell and does not change the
authority: the adapter's `--tools` stays `Read,Grep,Glob,Edit,Write` and only the two MCP names
join `--allowed-tools`. The command is `test.verify_command` (else `test.command`) or a
configured check, chosen in Go, never by the model; the model picks only whether to run it, which configured check (a closed
list) and a filter string that cannot hold shell syntax and reaches the process only as
`ORCH_TEST_FILTER`. Every run is under bwrap (`test.sandbox: none` with verify is a config
error), preceded by a guard comparison, and a `violation` marker fails the step. Decision D16
in `specs/permissions/DECISIONS.md` records why this keeps "no authority has a shell".

Modes (§4) do not change authority. Every authority denies whatever it does not grant
without prompting, so every mode is safe to run unattended; modes differ only in what the
human sees and confirms between steps.

## 38.2 Enforcement

* Adapters declare `Supports(authority)` and translate an authority into CLI flags only in
  `buildArgs`. An authority is declared supported only after a live check against the real
  CLI shows the permitted action works and the forbidden one is refused
  (`go test -tags security ./test/security/...`).
* claude (verified on 2.1.283): `--print --restricted --permission-mode dontAsk
  --permission-prompts none --strict-mcp-config --setting-sources ""`, the authority's
  `--tools` and `--allowed-tools`, `--settings` with `Read(...)` deny rules for the secret
  deny list (§8.3's list, one source: `projectctx.DenyPatterns`), `--input-format text`,
  then `--model`; the prompt goes on stdin (§41.1). The target repository's settings, hooks
  and MCP servers never load. The CLI's version must be inside the range these flags were
  verified on (§41.4).
* codex maps `read` to `exec --sandbox read-only` and `edit` to `exec --sandbox
  workspace-write`, with `--ask-for-approval never`; `--json` supplies usage, activity,
  tool calls and structured rate-limit errors. `none` and `web` remain unsupported because
  Codex exec always exposes its coding tools. Gemini support remains disabled until its
  flags are verified.
* `config.ValidateAuthorities` refuses an unenforceable route before any AI call (§7);
  `Execute` refuses a missing or unsupported authority without spawning anything.
* There is no bypass: the codebase never passes a permission-skipping mode.
* The runs directory (`.orchestrator/runs`, relative to the repository root) is added to
  every workflow call's deny list (`Dispatcher.RunsDir`). In claude a `Read(...)` deny rule
  also blocks Edit and Write on that path. This keeps other runs' prompts and context away
  from the file tools, and keeps a step from writing what a resume reads back (§37).
  Verified live on claude 2.1.285.
* Provider CLIs get `execution.ScrubbedEnv()`: the inherited
  environment minus the variables through which a launching agent session reaches its
  children (`CLAUDECODE`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`,
  `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`), and minus the
  credential variables the config names: `decision.jev.api_key_env` (whatever the decision
  provider) and, with a `jira` section, `jira.token_env` and `jira.email_env`
  (`Config.CredentialEnvNames`, registered through `execution.ScrubCredentials` right
  after `Validate`). The orchestrator reads those itself; no child needs them. Names are
  exact, never prefixes. The test command, which runs code a model wrote, gets the narrower
  `execution.TestEnv()` allowlist instead (§41.3). git keeps the full environment.

## 38.3 Guards around implement

* **Clean start.** In `inplace` mode (§47.1) a workflow containing `implement` refuses a
  dirty worktree (exit 2, before any approval prompt) unless
  `security.require_clean_worktree: false`. In worktree mode the key does not apply: the
  run's tree is a fresh checkout of a commit, clean by construction, and a dirty operator tree
  only gets a warning.
* **The `.git` pointer.** In a linked worktree `.git` is a file that decides which git
  directory every git call reads. It must read exactly `gitdir: <admin_dir>\n`; it is read in
  Go, never through git, and compared before the fingerprint in every guard comparison. A
  mismatch is a violation named `.git (worktree pointer)`.
* **Restore point.** Before the first step `run.json` records `pre_run_ref`: HEAD, or on a
  dirty tree a snapshot commit of the whole tree (untracked files included) written through
  a throwaway index - the working tree, index and stash list are untouched. Once the run is
  approved, the restore point is pinned under `refs/orchestrator/runs/<run-id>` (`git
  update-ref`), the only ref the orchestrator ever writes: outside `refs/heads` and
  `refs/tags`, so no branch, tag or default push sees it, and `git gc` cannot prune the
  commit. An approved resume pins it too, so a run recorded before pinning existed gets
  its ref. A failed pin is a warning, not a failure. The refs are never deleted
  automatically: `git for-each-ref refs/orchestrator` lists them and
  `git update-ref -d refs/orchestrator/runs/<run-id>` drops one.
* **Protected paths.** Around every `implement` call, a snapshot before is compared with
  one after, before the outcome is accepted and so before `test` runs anything: files that
  differ from HEAD or are git-ignored and match `security.protected_paths` or the config file
  when it lives in the worktree (by content hash); and
  a fingerprint of the repository's git config and effective hooks directory. A change
  fails the step (`WorkflowError`, exit 4) naming the paths and the restore point; the
  changes stay on disk. Without a guard, or when its snapshot fails, `implement` refuses
  before the provider call.
  `security.protected_paths` is the whole list: nothing is built in, and the key is required
  (a config without it is a config error before any call; `[]` written out protects nothing, and
  like `test.sandbox: none` every run with implement says so: `Protected paths: none: …` - a
  config written before, when `[]` meant the built-in list, shows there).
  `orchestrator.example.yaml` writes the list that was built in before: `.claude/**`,
  `.mcp.json`, `.github/workflows/**`, `.gitlab-ci.yml`, `.envrc`, `.vscode/**`. A pattern
  matches at any depth (`.claude/**` also covers `sub/.claude/…`); one with a leading `/`
  matches only from the repository root (`/.gitlab-ci.yml` is not `examples/ci/.gitlab-ci.yml`),
  in every place that takes this syntax (`secret_scan.allow_paths`, `policy.paths`). The config
  file stays protected in Go: it is the one path whose change would let the next run or resume
  read a weaker guard. The org layer's `protected_paths` are added, and the repository cannot
  remove them (§45).
* **Resume inputs.** The step guard also protects the run directory's resume inputs:
  `run.json`, `workflow.json`, `decision.json` and `artifacts/**` of **every** run under
  the runs directory (`action.RunDirPatterns`). Logs are output and never read back, so
  they are not protected. The orchestrator itself writes nothing to the run directory
  during a guarded call: implement's artifact is saved only after the guard's check.
* **Run-start baseline.** Where the restore point is recorded, `run.json` also records
  `head_ref` (HEAD) and `resume_baseline`: the guard's snapshot then (its patterns, the
  git fingerprint, and a content hash per protected path that differs from HEAD or is
  ignored), without the resume-input patterns, which change with every step. If that
  snapshot fails, the run stops before its first step. A resume compares the tree with the
  baseline, fingerprint first, before anything runs (§37.8).
* **The test step.** The same guard runs around the test command (and around each of the
  `checks:` before it, §44.2), which executes code the model wrote; the guard compares the git fingerprint before running `git status`, which
  would itself run a planted `core.fsmonitor`. Unless `test.sandbox: none` (the sandbox is
  the default, §41.3) the command runs under bubblewrap: filesystem read-only, the repository and a private `/tmp` writable, the
  git directories read-only, `$HOME` an empty tmpfs, no network, only `test.ro_binds` /
  `test.rw_binds` re-exposed. The fail-fast chain checks `bwrap` on PATH, the binds, and one
  probe run with the same isolation; the authority line shows `test runs (sandboxed)`, or a
  warning with `none`. The pre-run test (§41.5) runs under the same guard.
* **Secret scan.** After the guard check of every `implement` and `test`, what the tree adds
  is scanned for likely secrets; a new finding stops the step (`mode: stop`, the default) or
  is recorded and redacted from the review prompt (`warn`) (§41.6).
* **Fenced data.** The git diff (review) and test output (implement retry) are embedded in
  prompts inside a delimiter with a per-prompt random nonce and a line saying the block is
  data, never instructions. The task is embedded the same way in every stage prompt, with
  a line saying it is the task to carry out but cannot change the prompt's rules (§20).
  Defense in depth, not a boundary.

## 38.4 Audit

`run.json` records each AI step's `provider.authority`, the run's `pre_run_ref`,
`head_ref` and `resume_baseline`, the provider CLI versions (`provider_cli`), the pre-run
test, the secret-scan baseline and any `secret_findings`, and, per resume, a `resumes`
record (with its own `provider_cli`) and each new step's `attempt`. The `authority:` line and the `Restore point:` line are printed in every mode
(§27), and again before a resume's approval.

## 38.5 Not protected

* With `test.sandbox: none`, code the model writes runs in `test` with the operator's
  filesystem and network (its environment is still the allowlist); reviewing the changes in
  `manual` mode before `test` is then the mitigation. Under the sandbox, the test can still
  read what the operator can read outside `$HOME`, and write anything inside the repository
  (the secret scan looks at what it added).
* Repository content goes to the model provider - that is the job; only the secret deny
  list is withheld from the file tools, matched case-sensitively.
* Prompt injection is reduced, not eliminated; its effects are bounded by the step's
  authority and exposed by the guard, review and the diff.
* `test.command` is trusted config run through `sh -c`. A repository you do not control
  additionally needs `--config` from outside it, a sandboxed test step and ideally a
  container.
* **Ticket text** (§39) is operator-equivalent input written by anyone with edit rights on
  the ticket. It gets no gate beyond typed text: it is printed in full before anything is
  decided and kept verbatim in `run.json` and `ticket.json`, and no step gains authority
  because the task came from Jira. Like typed text, it is sent to the decision and
  translation providers.
* A hostile provider CLI binary is out of scope.
* **An orphaned provider CLI after SIGKILL.** If the orchestrator is killed with SIGKILL
  while a provider CLI runs, the CLI (its own process group) can keep writing after the
  last guard check. On resume, the baseline check (§37.8) catches protected paths and
  validation (§37.4) catches structural tampering. A subtly edited artifact written in
  that window could still reach the next prompt. It is bounded by that step's authority,
  and the history and next step are printed before the resume's approval so the operator
  can inspect them.

---

# 39. Task from a Jira ticket

Normative. Design record, findings, decisions and the plan as built: `specs/jira/`
(`SPEC.md`, `tasks/`).

`orchestrator run --jira <KEY|URL> ["notes"]` reads one Jira issue and renders it into the
task text. The ticket is only a way to write the task: the text becomes
`Task.Description`, and translation, decision, workflow, prompts and resume carry on
exactly as if it had been typed. No stage learns where the task came from.

## 39.1 CLI

```bash
orchestrator run --jira PMC-2789
orchestrator run --jira pmc-2789 "only the FE part; leave pim-qa alone"
orchestrator run --jira https://yourcompany.atlassian.net/browse/PMC-2789 --mode plan
orchestrator run --jira=PMC-2789
```

With `--jira`, positional text is optional and becomes **operator notes**. Without it,
`run` is unchanged. `--jira` with another command, twice, or empty is a usage error
(`ConfigError`, exit 2).

`jira.ParseRef` resolves the value after the fail-fast chain and before any request. Every
failure is a `ConfigError` (exit 2), and its text shows hosts only, never a whole URL:

| value | result |
|---|---|
| `PMC-2789`, ` pmc-2789 ` | `PMC-2789` (trimmed, upper-cased) |
| `https://<base host>/browse/PMC-2789`, with an optional trailing `/`, query or fragment | `PMC-2789` |
| a URL on a Data Center base with a context path: `https://jira.corp/jira/browse/PMC-1` | `PMC-1` |
| another hostname, scheme or port (default ports made explicit) | `--jira URL host "x.example" does not match jira.base_url "…"` (or the scheme) |
| a URL with a username or password | refused |
| any other path (boards, `selectedIssue=`, JQL) | `pass the issue key or its /browse/<KEY> URL` |
| not `^[A-Z][A-Z0-9_]+-[1-9][0-9]*$` after upper-casing (`domain.ValidTicketKey`) | refused |

The URL form only names the key. The request always goes to `jira.base_url`.

## 39.2 Configuration and credentials

The optional `jira` section (§5), validated whenever present (§6). Absent keys default to
`timeout: 10s`, `max_retries: 1`, `max_bytes: 32768`, and a trailing `/` on `base_url` is
dropped.

* `auth: basic`: Jira Cloud, `email_env` + `token_env` (an Atlassian API token), sent as
  `Authorization: Basic base64(email:token)`.
* `auth: bearer`: Data Center, `token_env` (a personal access token), sent as
  `Authorization: Bearer <token>`.

The values come only from the environment. They are read in one place
(`app.taskFromJira`), passed only to `jira.NewCredentials`, and sent only to
`jira.base_url`'s host. `jira.Credentials` prints as `[REDACTED]` and holds its values
behind a pointer.

`config.ValidateDependencies(cfg, checker, DependencyOptions{Jira})` checks the section
and the variables only when asked (§7): `run --jira` asks, `validate` asks when the
section is present, and `run` without `--jira` and `resume` never ask.

## 39.3 The request

```text
GET {base_url}/rest/api/2/issue/{KEY}?fields=summary,description,issuetype,parent,components,<fields[].id...>
Accept: application/json
User-Agent: orchestrator/<version>
Authorization: Basic … | Bearer …
```

* REST v2 on both Cloud and Data Center: `description` and text custom fields are wiki
  markup strings, so there is no ADF renderer.
* No redirect is followed (`http.ErrUseLastResponse`). A 3xx is an error that shows only
  the `Location` host.
* The body is read through a 1 MiB cap; a larger body is `invalid_response`.
* Each attempt is bounded by `jira.timeout`, and the whole fetch by the run's
  `execution.timeout`.
* 408, 429, 500, 502, 503, 504 and transport errors are retried up to `max_retries`.
  `Retry-After` (seconds or HTTP date) is honoured, but not one longer than `timeout`.
  Otherwise the backoff is 250 ms doubling, capped at `timeout`. Cancellation and the
  run's deadline are never retried.

The listener (§51) adds three more requests with the same client rules (no redirect, 1 MiB per
page, the same retries and redaction): the JQL search (`GET /rest/api/2/search/jql` with
`nextPageToken` on Cloud, `GET /rest/api/2/search` with `startAt` on Data Center), the comments
(`GET /rest/api/2/issue/{key}/comment?orderBy=created`, paged, at most 100) and `GET
/rest/api/2/myself` (the bot's own account, once at start). `jira.Fetch` then also asks for
`status`, `labels` and `reporter`. `run --jira` makes none of them.

## 39.4 Response validation

Strict. Anything else is `invalid_response` naming the JSON path. Unknown keys are
ignored.

| field | rule |
|---|---|
| `key` | an issue key. A different one than requested (a moved issue) is accepted, both are recorded, and a warning is printed |
| `fields.summary` | non-empty string |
| `fields.description` | string, `null` or absent |
| `fields.issuetype.name` | non-empty string |
| `fields.parent` | absent/`null`, or `{key, fields.summary}` with a valid key and a string summary |
| `fields.components` | absent, `null` or an array of at most 50 objects with a non-empty one-line `name` (at most 255 bytes); only the names are kept, in `ticket.json`, for `story plan` to find services by; never part of the task text |
| `fields.<customfield_N>` | absent, `null` or string; any other type: `not a text field; remove it from jira.fields` |

`\r\n` becomes `\n` in every text value. A blank custom field is left out.

For the listener (§51) the same validation covers `fields.status.name` (non-empty string),
`fields.labels` (an array of at most 100 strings) and `fields.reporter` (`{accountId}` on Cloud,
`{name}` on Data Center), and each search page and comment page is validated as strictly.

## 39.5 Rendering

`jira.Render(ticket, notes, max_bytes)`, deterministic:

```text
<KEY>: <summary>
Type: <issue type>
Parent: <parent key> — <parent summary>
Source: <base_url>/browse/<KEY>

Description:
<description>

<field label>:
<field value>                 (each non-empty configured field, in config order)

Operator notes:
<notes>
```

Sections with no content are left out. Values are verbatim wiki markup, except that each
line's trailing whitespace and blank lines at both ends of a section are removed. The
notes are trimmed and come last, so the operator's own words are the final thing each stage
reads. The text ends with one newline. Longer than `max_bytes` is refused, never
truncated (`ConfigError`, exit 2: `PMC-2789 renders to 51203 bytes, more than
jira.max_bytes (32768); raise the limit or trim the ticket`).

For the listener's tasks only (§51.7) there is one more section, after the description:

```text
Discussion since the orchestrator's questions:
--- orchestrator, 2026-10-29 10:02
<the bot's last question comment, as posted>
--- comment, 2026-10-29 11:40
<a person's answer>
```

It holds the bot's **last** comment that asked questions (first line `[orchestrator]`, the bot
being the account `/myself` answered) and every comment after it; without such a comment there
is no section. Authors are not named. `run --jira` renders no comments.

## 39.6 Order of operations

```text
parseArgs                              --jira value; notes = positional text
loadAndValidateConfig(Jira: true)      the whole fail-fast chain, variables included
jira.ParseRef                          still no network
decideCtx starts (execution.timeout)
checkJevModel                          GET /v1/models, real Jev only (§7)
repository.Open, IsDirty
FetchingTicket → Fetch → TicketFetched (→ TicketMoved)
jira.Render → TaskBlock                the full task, printed before anything is decided
store.NewRun(Task{Description, Source}) → Lock → SaveTicket
… unchanged: run header, decision, workflow, approval, executeWorkflow
```

A fetch or render failure is returned directly: no run directory is created, and no
provider or decision call is made.

## 39.7 Persistence and resume

`run.json`'s task gains an optional `source`:

```text
task.source.kind            "jira"
task.source.key             the key the issue answered under
task.source.requested_key   the key asked for, only when the issue has moved
task.source.url             <base_url>/browse/<key>
task.source.fetched_at
task.source.notes           operator notes were appended
task.source.listener        true for a task `jira listen` handed to the child (§51.7)
```

`task.description` stays the complete rendered text. Loading validates the source like
the rest of `run.json` (§37.4): kind `jira`, valid keys, an absolute http(s) URL, a
non-zero `fetched_at`. Otherwise it is `corrupt`. Runs without a source load as before.

`ticket.json` is the validated issue, written once after the run is created. It is an
audit record that nothing reads back, so it is not a resume input (§38.3).

`resume` never contacts Jira: it builds no client, and needs neither the section nor the
variables. A ticket edited after the run started does not change the run. The resume
header shows the source line (§27).

## 39.8 Security

* **Credentials:** env only, a literal secret key in the file is rejected, and they are
  sent only to `jira.base_url`, which is `https` except on loopback, with no redirects.
  Every error text has the token, the email and the base64 `email:token` removed, before
  a body excerpt is cut to 512 bytes. They are scrubbed from every child process (§38.2).
* **Ticket text** is treated as typed text (§38.5): printed in full, persisted verbatim,
  sent to the decision and translation providers, and no step gains authority from it.
  Wiki mentions render as opaque `[~accountid:…]` ids.
* **Oversized or hostile answers** are bounded by the 1 MiB cap, strict decoding and
  `max_bytes`.

## 39.9 Errors and exit codes

`apperr.JiraError` (§23):

| kind | cause | message (abridged) | exit |
|---|---|---|---|
| `auth` | 401 | `Jira rejected the credentials in JIRA_EMAIL/JIRA_API_TOKEN` | 2 |
| `not_found` | 404, or 403 | `PMC-2789 does not exist, or the account behind JIRA_EMAIL cannot see it` | 2 |
| `redirect` | any 3xx | `Jira answered with a redirect to <host>; check jira.base_url` | 2 |
| `timeout` | attempt timeout, 408, run deadline | `Jira did not answer within 10s` | 3 |
| `connection` | dial, DNS, reset | `Jira could not be reached` | 3 |
| `rate_limited` | 429 | `Jira is rate limiting requests` | 3 |
| `unavailable` | 500, 502, 503, 504 | `Jira is unavailable (HTTP 503 after 2 attempts)` | 3 |
| `invalid_response` | §39.4, a body over 1 MiB, any other status | names the JSON path or the status | 3 |
| `cancelled` | Ctrl-C | `fetching PMC-2789 from Jira was cancelled` | 5 |

## 39.10 Not covered

* Writing to Jira other than the child issues of an approved story (§39.11, §48.7) and the
  comment and transition after a merge request (§39.12) and, for the listener only, its
  comments and transitions without a merge request (§51.8): worklogs, links, assigning a
  ticket, a comment on a `run --jira` run without a merge request.
* Linked issues, sub-tasks, attachments, images; boards, sprints. Comments and JQL belong to
  the listener alone (§51): `run --jira` reads neither.
* The Data Center "Epic Link" field: on Data Center only a sub-task's parent is shown.
* Converting wiki markup to Markdown; ADF (REST v3); OAuth or any interactive login.
* `--jira` on `resume` or `validate`.
* Deferred (`specs/jira/SPEC.md` §19): an outcome comment or transition at the end of a
  run, comments and linked issues as sections. (Fencing the task block in prompts is done:
  §20.)

## 39.11 Story: the issue type and the children

Normative; the story itself is §48.

* **A story issue type.** `story.jira_issue_types` (default none) lists issue types that are
  not one task. `run --jira KEY` on an issue of such a type (compared case-insensitively)
  refuses before creating a run: `PAY-1 is an Epic (story.jira_issue_types): plan it with
  orchestrator story plan --jira PAY-1`, a `ConfigError`. `story plan --jira` accepts any type.
  The ticket's `components` (names only, §39.4) are kept in `ticket.json` for `story plan` to
  find services by; they are never part of the task text.
* **Child issues.** With `jira.create` and a story planned from a Jira issue, `story run` creates
  one issue per task without a key, after the operator's approval (§48.7). This is the first
  write to Jira, and the only one: `POST /rest/api/2/issue` to `jira.base_url`, with the same
  credential handling as the read (§39.8), no redirects, and idempotent (an intent is recorded
  before each request, a request that may have reached Jira is never repeated).

## 39.12 Write-back after a merge request

Normative; the run that produces the merge request is §50.

```yaml
jira:
  write:
    comment: true             # default false
    transition: "In Review"   # optional; the transition's name or its target status name
```

* Only for a task with a Jira key (`run --jira`: the issue; a story task: its child issue,
  §48.7) and only after this run's push opened a merge request (`mr.state: created`). The same
  `jira.base_url`, credentials, redaction, no-redirect and strict-decoding rules as §39.8 and
  §39.11 apply; the token is the one the read uses.
* **Comment**: `POST /rest/api/2/issue/{key}/comment`, wiki markup rendered by Go:
  `Merge request [!12|<url>]: <commit header>`, then `Review: pass · Tests: passed · Cost:
  $0.84 · Run <run-id>`. An intent is recorded before the request, the id after the 201, and the
  request is never repeated once it may have reached Jira.
* **Transition**: `GET /rest/api/2/issue/{key}/transitions`, the one whose `name` or `to.name`
  equals the configured value (case-insensitive), then `POST …/transitions
  {"transition":{"id":…}}` (204). None matches: `Warning: PAY-12 has no transition "In Review"
  (available: Start, Done)`. The issue already in that status: nothing is sent.
* A failure is a warning; the run's status and exit code do not change (the merge request is the
  result).
* `jira.write` is copied into `options.jira_write` at run start and a resume uses that copy.
  Resume still never *reads* Jira (§39.7): it builds a Jira client only for this write.
* `run.json: jira_write {comment: {state, id}, transition: {name, state: done|missing|failed|
  unchanged}}`.

---

# 40. Run chosen stages

Normative. Design record, decisions and the plan as built: `specs/stage-select/`
(`SPEC.md`, `tasks/`).

`orchestrator run --stage <name> [--stage <name> ...] "<task>"` lets the operator name the
stages instead of asking a decision provider. Which stages run is the operator's choice;
their order, the transitions, authorities, the guard and resume are unchanged.

## 40.1 CLI

```bash
orchestrator run --stage review "review my change"
orchestrator run --stage testing --stage review "check the hand edit"
orchestrator run --stage investigation "investigate the cache"
orchestrator run --stage review --jira PMC-2789
```

* `--stage X` and `--stage=X`, in any position, repeatable; one stage per flag (a comma is a
  usage error). Valid only with `run` (`--stage is only valid with run`, exit 2).
* A task is required, as for any `run`; with `--jira` the ticket is the task.
* Names are the five stages (`research`, `investigation`, `development`, `testing`,
  `review`) and the action names as aliases (`investigate`, `implement`, `test`),
  case-insensitive. An unknown name is a usage error listing the valid ones. Duplicates
  collapse. `domain.ParseStageFlag` parses; `ParseStage` stays strict for persisted data.
  The old names `analysis`, `analyze`, `planning` and `plan` are refused with a message that
  says analysis became the clarify step and planning became investigation, and names
  `specs/clarify`; they are never read as the new ones.

## 40.2 The decision

`domain.ManualDecision` builds `Decision{Source: manual}` from the flags: context depth
`none`, no complexity, one outcome per stage in `StageOrder` (`selected with --stage` /
`not selected`) with no probability or threshold. `policy.stages.force` is not applied.
`workflow.Build` runs on it as usual, and `workflow.Verify` accepts it on resume.

No translator or Context Manager is constructed and no project context is collected. The
decision provider is asked one thing: the clarity question, alone, on the request as typed
(`decision.Provider.Clarity`, §49.2). Below `policy.clarify.threshold` (and with
`clarify.enabled: true`) the clarify step runs exactly as for a decided run, the stages stay the
operator's, and `task.clarification` is recorded in `run.json` beside the decision. A
clarity question that cannot be answered (a provider failure) is a warning on stderr and the
run goes on with no clarity trace and no clarification; a clarify call that fails fails the run, a
task that stays unclear stops it `task needs input`, as for a decided run. The fail-fast chain
(§7) is unchanged and still checks every executable the config declares, including the decision
provider's and the Jev key. `decision.json` has `source: manual`, the request, the stages and
the workflow, the `clarity` trace and the clarity and clarify usage when they were made, and
empty provider, model and latencies; `project_context.json` is not written.

## 40.3 Console and gap warnings

Instead of the decision box the console prints `Stages (manual): testing → review`, then
the authority line and the mode prompt. A stage whose usual input was not selected gets a
one-line warning on stderr, never a refusal (`workflow.Gaps`): development without an
investigation ("development runs without an investigation; it works from the task text
alone"). Testing and review never warn: testing runs the command on the current tree and
review reads the real diff.

## 40.4 Review

`review` reads the working tree against `HEAD`, excluding the orchestrator's own runs
directory. If that diff is empty the step fails the run before the model is called:
`nothing to review: the working tree has no changes against HEAD`. This applies to every
run, not only `--stage`.

## 40.5 Unchanged rules

* A selection with `development` needs a clean worktree (§38.3); one without never does.
* Retries follow §11: a failing test or review goes back to `implement` only if it is in the
  workflow; otherwise a test failure fails the run and a review failure completes it with
  the verdict. `max_steps` and `max_fix_iterations` bound loops.
* `--mode plan` works as for a decided run. `resume` continues the persisted stages and
  builds no decision provider.

## 40.6 Not covered

* A stage order or repeat count chosen by the operator; `--skip-stage`.
* `--base <ref>` and reviewing committed history.
* Per-stage provider or model flags; a config key for default stages.
* A partial decision (stages from flags, complexity from a provider).

---

# 41. Hardening: reliable prompts and safe defaults

Normative. Design record, the owner's answers (Q1-Q12), defaults and the plan as built:
`specs/hardening/` (`SPEC.md`, `tasks/`). Nothing here widens an authority; every rule
narrows or bounds.

## 41.1 Prompt delivery

The claude and codex adapters pass every prompt on stdin, never in argv:
Linux limits one argument to 128 KiB, and a prompt carrying a diff or a test log can exceed
it. Claude uses `--input-format text`; Codex uses `exec ... --json -`. A `ProviderError` shows the argv and
`prompt: N bytes on stdin`, never the text. Gemini support remains disabled.

## 41.2 Bounded prompt content

```yaml
limits:
  prompt:                        # optional; artifacts and logs always keep everything
    max_test_output_bytes: 32768 # 4096..1048576
    max_diff_bytes: 262144       # 16384..4194304
    diff_summarize: []           # extra globs shown to review as a stat line only
```

* **Test output** in the implement retry prompt (and a red pre-run test's, §41.5) is cut by
  `action.BoundTestOutput`: stdout and stderr share the budget, a stream within its share is
  unchanged, a longer one keeps its beginning, the lines that look like failures (`--- FAIL`,
  `panic:`, `Error`, pytest `E   `, jest `●`, `Traceback`, `x_test.go:42:`, ...) and its end,
  on line boundaries, with `[... N lines, B bytes omitted ...]` at each cut. The prompt says
  it was shortened. The cut depends only on the input, so a resume rebuilds the same prompt.
* **The review diff** is `Repository.ReviewDiff`: a list of every changed file with
  `+added -deleted` and how it is shown, then the full diffs in git's order while they fit
  `max_diff_bytes`. New (untracked, not ignored) files are shown with their content. A file
  matching the secret deny list (§8.3, tracked or not) is named only; binaries and new
  symlinks are named; lock and minified files (`go.sum`, `package-lock.json`, `yarn.lock`,
  `pnpm-lock.yaml`, `Cargo.lock`, `poetry.lock`, `*.min.js`, ... plus `diff_summarize`) get
  a stat line - manifests such as `go.mod` stay visible; a file over the budget gets a stat
  line and later, smaller ones still get their chance; the list itself takes at most half
  the budget and more than 500 untracked files are counted, not listed. git runs with
  `--no-ext-diff --no-textconv`. The prompt tells the reviewer which files it may Read
  itself. The review result records `diff: {bytes, files, shown, summarized, withheld,
  truncated}`; a `diff` the model writes is discarded.

## 41.3 The test command's environment and sandbox

* Every process that runs `test.command` (the test step, the pre-run test, the bwrap probe)
  gets `execution.TestEnv()`, an allowlist: `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, the
  locale variables, `TERM`, `TZ`, `TMPDIR`, `CI`, and toolchain variables (`GOPATH`,
  `GOROOT`, `GOFLAGS`, `GOCACHE`, `GOMODCACHE`, `GOTOOLCHAIN`, `CGO_ENABLED`, `CARGO_HOME`,
  `RUSTUP_HOME`, `JAVA_HOME`, `VIRTUAL_ENV`, `PYTHONPATH`, `NODE_OPTIONS`,
  `XDG_CACHE_HOME`). Proxies are not in it: a proxy URL can carry credentials.
  `test.env_pass` adds exact names; a name that looks like a credential (`TOKEN`, `SECRET`,
  `PASSWORD`, `PASSWD`, `CREDENTIAL`, `APIKEY`, `PRIVATEKEY` anywhere, or a whole `AUTH`,
  `KEY`, `KEYS`, `PASS`, `COOKIE` segment), a declared credential or a parent-session
  variable is a validation error, with no override. Provider CLIs keep
  `execution.ScrubbedEnv()` (§38.2).
* In a worktree run (§47) the test sandbox binds the worktree writable and its `.git` file
  read-only (a file bind after the worktree bind); the git directories are outside it and
  read-only. `worktree.setup` runs under the same mounts, but **with the network** (§47.4).
* `test.sandbox` absent means `bwrap` (§38.3). `none` must be written out; the authority
  line then ends with `— NOT sandboxed: network, $HOME and your files are reachable` on
  every run. A host without a working bwrap fails fast, naming both remedies.

## 41.4 Provider CLI version

Each adapter that supports an authority declares the CLI versions its flags were verified
on (`provider.VersionVerifier`); claude: `>= 2.1.283, < 2.2.0`. Right after
`ValidateAuthorities`, for every provider the config routes (actions, translator, LLM
decision provider), the fail-fast chain runs `<cli> --version` (10 s). A version outside
the range, or one that cannot be parsed, stops before any call (exit 2) naming the range,
the live suite to run, and `security.allow_unverified_cli: true`, which turns the refusal
into a red warning in every run header and is recorded. A CLI that cannot be executed is
refused regardless. `run.json` records `provider_cli: {<provider>: {version, verified}}`
and each resume its own in `resumes[].provider_cli`; `validate` prints the version and the
range. Extending the range needs a green `go test -tags security ./test/security/...` on
that version and an entry in `specs/permissions/DECISIONS.md`.

## 41.5 Pre-run test

When the workflow contains both `implement` and `test`, `test.baseline` is not `off`
(`require_green` is the default) and no step has run yet, the test command runs once after
approval, right before the engine - same runner, sandbox, environment and guard as the test
step, inside `execution.timeout`. It is not an execution. With `checks:` (§44.2) it is the
same sequence as the test step's: each check in order, then `test.command`, stopping at the
first non-zero exit code. A red check refuses the run like a red test (its output is in
`logs/pre-run-test.<check>.{stdout,stderr}.log`), and `pre_run_test` records `checks` and
`failed_check`. `test.baseline: off` skips the whole sequence.

* Exit 0: `run.json: pre_run_test` (the whole `TestResult`), console `pre-run test: passed`.
* Non-zero: the run fails before any AI step (exit 4), naming the log
  (`logs/pre-run-test.{stdout,stderr}.log`) and the ways out: fix the tests, start the run
  with `--allow-red-tests`, or set `test.baseline: off`. A resume re-runs it.
* `run --allow-red-tests` (valid with `run` only, recorded as `allow_red_tests`): the task is
  to make failing tests pass. A red pre-run test is recorded and the run goes on; the first
  implement prompt gets its bounded output ("already FAILS before any change, and the
  operator expects that") until the first test step has run, also after a resume. The run
  still ends green or not by the test step's exit code. When the pre-run test and a failed
  test step both have parsed failing test names in the same format, the retry prompt splits
  them into failures from before the run and new ones - a hint only (§44.7).

## 41.6 Secret scan

```yaml
security:
  secret_scan:
    mode: stop            # stop (default) | warn | off
    allow_paths: []       # protected_paths-style patterns, e.g. [testdata/**]
```

* `internal/secretscan` (standard library only) finds cloud and service tokens (AWS, GitHub,
  GitLab, Slack, Anthropic, OpenAI, Stripe), private keys, JWTs, passwords in URLs and
  high-entropy assignments to secret-like names, and skips documentation examples,
  placeholders and environment lookups. A finding is a rule, a line and a fingerprint
  (`sha256(rule, path, sha256(value))`) - never the value.
* After every `implement` and `test` step (after its guard check), what the working tree
  adds against HEAD is scanned (`Repository.AddedContent`): the `+` lines of tracked files
  and the whole of untracked, not ignored files up to 1 MiB, the runs directory excluded,
  no deny list (the scan is local). Findings under `allow_paths` are dropped.
* Before the first step (after the pre-run test) the same scan records
  `run.json: secret_scan_baseline`; only findings outside it, and not already recorded,
  count. A resume keeps the baseline.
* `stop`: the step fails (exit 4) with `file:line (rule)` per finding, the restore point and
  both ways out; nothing has sent the value on. `warn`: each new finding is printed and
  appended to `secret_findings: [{step_index, file, line, rule, fingerprint}]`. `off`: the run
  says `Secret scan: off`.
* While the scan is on, the review diff goes through `secretscan.Redact`: every matched value
  becomes `[REDACTED:<rule>]` (a private key as a whole block), baseline findings included.

## 41.7 Persistence

`run.json` gains, each validated strictly on load (§37.4): `provider_cli`,
`resumes[].provider_cli`, `allow_red_tests`, `pre_run_test` (`success` must match
`exit_code == 0`), `secret_scan_baseline` (hex sha256, at most 10 000),
`secret_findings` (known rule, clean relative path, line ≥ 1, step in range). The review
result gains `diff`. Logs gain `logs/pre-run-test.{stdout,stderr}.log`.

## 41.8 Not covered

* Secrets in model prose (artifacts, provider output logs), in ignored files, split across
  lines or encoded.
* `PATH` entries inside the repository; macOS sandboxing; codex/gemini stdin delivery.
* Comparing failing test names between the pre-run test and the test step: a red suite is
  either refused or the task.
* Files the tests create that are not ignored appear in the review diff.

# 42. Commit at the end of a run

Normative. A completed run's changes stay in the working tree until the operator commits
them; at the end of the run the orchestrator offers to do it, and commits only on an
explicit yes - asked at the end, or given in advance with `auto_commit: true` (§42.6).

## 42.1 When it is offered

After the engine has finished and the final answer (if any) is printed, `run` and `resume`
(the shared tail, `executeWorkflow`) offer a commit when all of these hold:

* the operator is at the terminal: `--mode auto` was not given and stdin is a terminal.
  `--mode auto` never asks, and commits only with `auto_commit` (§42.6);
* the run is `completed` and its workflow contains `implement`;
* the run started from a clean commit: `pre_run_ref` is set and equals `head_ref`. A run
  started on uncommitted changes (`security.require_clean_worktree: false`) would commit
  them together with its own, so it is told to commit by hand instead;
* the repository still matches the run-start baseline (`action.CheckBaseline` over
  `resume_baseline`): the git fingerprint first, before any `git status`, then the
  protected paths. `git commit` runs the repository's hooks, so they must be the ones the
  run started with;
* `git status` (the runs directory excluded) shows changes. Otherwise it says
  `No changes to commit.`

Anything that stops the offer is printed as `Not offering a commit: <why>`; none of it
changes the run's status or exit code.

In a worktree run (§47) the commit is made on the run's own branch (§50.3) and is the first
step of the chain of §50.5: under `--mode auto` without this question and under the §42.6
rules, in every other mode with the question below, which is then the first of three.

## 42.2 The question

```
Changes:
   M fixture.go
Warning: the review did not pass: <summary>
Warning: a likely secret was added at cfg.go:3 (aws-access-key)

Commit message:
  Fix Add to use +

  Orchestrator-Run: <run-id>

Commit to <branch>? [y]es / [N]o / [e]dit message:
```

* Warnings: a failing last review verdict (a run that completes with the verdict,
  §11; its `summary_short`, else its `summary`) and every `secret_findings` entry (`security.secret_scan.mode: warn`). Never a value.
* The message is written by Go, with no provider call: the header is the `summary_short`
  of the run's first implementation that has one - a fix iteration's describes only the
  fix, not the change being committed - else of the last implementation (restored from
  the artifacts on resume), cut to its first
  line and 72 runes (`...` marks a cut), prefixed with `KEY: ` for a
  `run --jira` task. What the cut left out goes into the body; the body ends with the
  `Orchestrator-Run: <run-id>` trailer. The commit of a story task (§48.5) also gets the trailer
  `Orchestrator-Story: <slug>/<ID>`, and its header the task's Jira key when it has one.
* **The type.** The implement answer may carry `commit_type`, one of `feat`, `fix`, `refactor`,
  `perf`, `test`, `docs`, `build`, `ci`, `chore`. Go reads it from the same implementation the
  `summary_short` comes from, trims and lower-cases it and accepts it only from that closed list;
  anything else, or no field, means no type and the artifact is never failed for it. The header
  before the 72-rune cut is `<type>: <KEY> <summary>` with a type and a key, `<type>: <summary>`
  with a type only, `<KEY>: <summary>` with a key only, the summary alone otherwise. `e` replaces
  the whole header as typed.
* **The decision trailer.** After `Orchestrator-Run` and before `Orchestrator-Story`:
  `Orchestrator-Decision: <complexity>/<enabled stages in stage order>`, `manual/<stages>` for
  `run --stage` and a review run. Go builds it from `run.Decision`.
* Without a `summary_short` (the model left it out, or no implementation artifact is
  left) no message is proposed: the orchestrator says
  `The implementation proposed no commit message.` and asks for one line; an empty answer
  commits nothing, a written one is shown with the trailer and still needs the `y`.
* When the last implement matched `policy.paths` rules with `approval: manual` (§43.3,
  `executions[].path_rules`), the question lists them first, with their files, under
  `policy.paths requires your approval for:`. The answer is the explicit yes; nothing else
  changes.
* The default (an empty answer) is no. `e` replaces the header and asks again. No answer
  (stdin closed) is no.

## 42.3 The commit

`Repository.CommitAll` stages the whole tree — tracked changes, deletions and untracked
files, minus ignored ones and the runs directory — with `git add -A`, and runs
`git commit --file=-` (the message on stdin) on the current branch, as the user's own git
identity and with the repository's hooks. It is the one call that writes the user's index
and a branch. If the commit fails (a hook refuses, no identity), the changes stay staged and
the error is printed; the run stays `completed`.

## 42.4 Persistence

`run.json` gains `commit`: the full SHA of the commit, set only after it succeeded.
Validated on load: a full lowercase SHA, on a `completed` run only. A completed run is not
resumable (§37), so the moved HEAD never meets a resume.

## 42.5 Not covered

* Pushing and merge requests are §50, for a worktree run only: an `inplace` run commits on the
  current branch (the question or `auto_commit`), pushes nothing and opens nothing.
* A Conventional Commit scope or breaking-change marker: only the type (§42.2).
* Committing a part of the changes: the operator commits by hand when they want that.

## 42.6 `auto_commit`

`auto_commit: true` (top level, default false) is the operator's yes given in advance. It
does not apply to `--mode manual`, where the person confirms every step: that run ends with
the question of §42.2. In every other mode the
commit is then made without the question (`--mode auto` included) and
without a terminal, after every check of §42.1 but the first. It is not made, and
`Not committing automatically: <why>` is printed, when the question would have shown a
warning (§42.2) or there is no proposed message (no `summary_short`): nobody is there to
read the one or write the other. It is not made either when the last implement matched a
`policy.paths` rule with `approval: manual`: the yes given in advance did not know which
files would change. It prints `Not committing automatically: policy.paths requires manual
approval: <match> (<n> files); commit what you want yourself.` and the changes stay in the
working tree; `outcome.approval_required` (§43.3) is true. The message is the one §42.2 proposes; the commit is
§42.3's and is recorded as in §42.4. `Committed <sha> to <branch>: <header>` is printed. `auto_commit` has no effect in a worktree
run (§47): `--mode auto` is the yes given in advance for the commit, the push and the merge
request, and every other mode asks (§50.5). The §42.6 refusals above apply to a worktree run
only under `--mode auto`, where nobody can read a warning.

---

# 43. Run summary and step metrics

Normative. Every run that ends says, per step and in total, how long it took, what it cost,
which model answered, how big its prompt was and whether anything in it was cut, which
tools the model used, and what an implement step changed. `run.json` records it (the
source of truth); `summary.md` renders it for a reader. Nothing decides anything from these
numbers: they are a record, like `outcome` (§21).

## 43.1 Where it comes from

| What | Source | Trust |
| --- | --- | --- |
| Step times | the engine's clock around each step (`started_at`, `finished_at`) | exact |
| Tokens, cost, turns | the provider CLI's final report (claude: the `result` event) | as reported |
| Models, API time | the same report (`modelUsage`, `duration_api_ms`) | as reported |
| Tool calls | the model's `tool_use` blocks in the CLI's stream | as reported |
| Prompt size, cuts | the orchestrator, from the prompt it sent | exact |
| Code change | `git diff --numstat` between trees, after the guard passed the step | exact |

Claude and Codex report models and tool calls; Codex does not currently report API time or
cost. A call that ends before its final report (a timeout, a crash, a kill) is still
counted: its usage is summed from the assistant messages it streamed until then and marked
`partial` (§43.3).

## 43.2 summary.md

Written to `.orchestrator/runs/<run-id>/summary.md` whenever a run reaches a terminal
status (`completed`, `failed`, `stopped`), together with `outcome`; a resume that ends
rewrites it over the whole history. It is rendered by Go from `run.json` and
`decision.json` with no provider call, is never read back, and is not a resume input.
Failing to write it fails nothing. Values that do not apply or are not known are `—`.
`summary.md` is for the metrics; the reviewer-facing `report.md`, written next to it by the same
rules, is §50.4.

### Totals

| Field | Meaning |
| --- | --- |
| Task | The first line of the task, at most 120 characters. |
| Source | Where a `run --jira` task came from: kind, issue key, URL. Absent for a typed task. |
| Started | When the run was created (`created_at`), before the decision. |
| Status | The run's final status; for a failed or stopped run, followed by the reason's telling line (a provider error's `failure:` line, otherwise its first line). |
| Workflow | The workflow's actions in order. |
| Worktree | Only for a worktree run (§47): the branch, the base (`<branch>@<short sha>`), the commit, `conflicts: <files>` when the merge check found some, and `kept at <path>` when the worktree was not removed. |
| Claude auth | Only when the run recorded one (§46.6): `api key (ANTHROPIC_API_KEY, auto)`, `CLI login (…)`; on a resumed run the mode of each part if they differ. In `login` the Cost row is marked as an estimate not billed per token. |
| Decision | Who decided (`jev`, `llm`, `user`; `manual (run --stage)`), the decision provider and model that answered, the complexity, and whether the operator edited the decision on the review screen. |
| Steps | Executions recorded (every attempt, retries and resumes included) and the fix iterations. |
| Wall time | `outcome.duration_ms`: from the first step's start to the last step's finish, pauses between a stop and its resume included. The decision and the approval are not in it. |
| Active time | `outcome.active_ms`: the sum of the steps' own durations, without those pauses. |
| Decision time | `outcome.decision_overhead.ms`: the provider-bound decision steps (translation, pre-check, clarify, clarity re-checks, context, classification), never the operator's thinking time. |
| Cost | `outcome.usage.cost_usd`: every provider call of the run, the decision calls included. With partial calls it reads `$X or more: N calls ended without a report`, since their cost is unknown. |
| Decision cost | The part of the cost spent deciding the workflow (the clarify call and the clarity re-checks included), and how many decision calls reported usage. |
| Tokens | Summed over every call: `prompt` is every input token (`input` uncached + `cache read` + `cache write`); `output` the generated tokens. |
| Provider calls | Calls that reported usage (`outcome.usage.calls`); each extra reviewer's call (§44.4) counts. |
| Verify | Only when the implement session made `run_tests` or `run_check` calls (§44.3): how many and how many of them ran and passed; a refused call never passed. |
| Replace | Only when the implement session called `replace` (§44.3): how many calls, and the occurrences and files they rewrote (dry runs and refused calls rewrote nothing). |
| Tests (last run) | The last test step's result: `passed`, `failed`, `failed (check <name>)` when a check failed and the tests did not run (§44.2), or `not run`. |
| Failing tests | Only when the run started red (`--allow-red-tests`) and the last failed test step and the pre-run test have parsed names in the same format: `N failing before the run, M new` (§44.7). A hint. |
| Review verdict | The last review's authoritative verdict (`ReviewResult.Decide`: any critical or high issue, or any unmet acceptance criterion, fails). |
| Reviewers | Only when the last review had extra reviewers (§44.4): the models whose answers were merged, base model first, e.g. `sonnet, opus`. |
| Code change | `outcome.diff`: the run's net change, i.e. the working tree after the last implement against the restore point `pre_run_ref` — files (binary ones in parentheses), inserted and deleted lines. |
| Approval | Only when `outcome.approval_required`: `Commit needs manual approval (policy.paths)`. |
| Commit | The commit the operator approved at the end (§42), when there is one. |
| Merge request | Only for a worktree run that reached the end chain (§50.5): the URL (`!12: <url>`), or `pushed, no merge request`, or the skip or the error (`mr.skipped`, `mr.error`; `skipped: push disabled (story.push: false)` for a story task that does not push). |

### Clarification

Only when `task.clarification` exists (§49.9), between the totals and the steps. A line saying
whether the task was `clear` or `unclear` with every check's P(clear) against the threshold
(`0.42 → 0.55 against the threshold 0.80`), for a run that stopped `task needs input` the
sentence that the run needs input, and the clarification as the stages read it
(`domain.Clarification.Render`) as a block quote: each point with its answer and who gave it,
the open points as `Open: …`, and the operator's additions. A run stopped `task needs input`
has this section and no steps.

### Steps

One row per execution, in order.

| Column | Meaning |
| --- | --- |
| # | The execution's index; it is also the `NNN` of its artifact and log files. |
| Step | The action, with `(resume N)` when the N-th resume ran it. |
| Result | `completed` or `failed`; a test's `passed` / `failed (exit N)` / `failed (check <name>, exit N)`; a review's verdict and issue count, with `N/M criteria met` when the investigation had criteria. |
| Time | The step's own duration, git and guard checks included. |
| API time | `call.api_ms`: how long the call waited on the model API. Time minus API time went to tools, the CLI and the orchestrator's checks. |
| Cost | `usage.cost_usd`; `unknown` for a partial call. |
| Prompt tokens | Every input token of the call: uncached, cache read and cache write together. |
| Output tokens | Generated tokens; `≥ N` for a partial call, whose output count is a lower bound. |
| Turns | API round trips the CLI made (`num_turns`; for a partial call, the messages streamed). |
| Model | The model(s) that answered (`call.models`), e.g. `claude-sonnet-5-5` for the configured `sonnet`; for a run recorded before §43, the configured name followed by `(configured)`. |
| Prompt | The size of the prompt sent on stdin, with `cut: <parts>` when `limits.prompt` shortened something (§41.2): `test_output` (a failing test's output in the implement prompt), `diff` (the review diff, where a file's diff did not fit or a file was not listed). |

### Tool calls

One row per step whose model called tools: the total and the count per tool, the most used
first. MCP tools appear under their full name (`mcp__<server>__<tool>`).

### Code changes

One row per implement step with a diff.

| Column | Meaning |
| --- | --- |
| This step | What the step changed: the working tree after the call against the tree before it. |
| Run so far | The working tree after the step against `pre_run_ref`. |
| Changed, not in changed_files | Files git shows the step changed that the artifact's `changed_files` and `tests_added` do not name. |
| In changed_files, unchanged | Files the artifact names that the step did not change (a rename shows its new name in git, so an artifact naming the old one lands here). |

Both lists are empty when the artifact has no `changed_files` at all (the model did not
answer in JSON), and hold at most 50 paths each.

### Path rules

Only when the run has `policy.paths` rules and the last implement matched at least one. One
row per matched rule of the last implement (`executions[].path_rules`); an earlier
implement's matches are not shown.

| Column | Meaning |
| --- | --- |
| Rule | The rule's `match` pattern. |
| Files | The files of the run's net diff the rule matched, in git's order, at most 50. |
| Requires | The rule's `require_stages`; `—` when it has none. |
| Missing | The required stages the workflow does not run; `—` when it runs them all. A step with a missing stage failed. |
| Approval | `manual` when the rule asks for a person's yes before the commit; `—` otherwise. |

### Notes

One line per partial call (what its numbers mean), per step that ran without an MCP server
it was given (`call.mcp_failed`), and per failed step (its telling error line). The full
error is in `run.json` and the step's logs.

## 43.3 run.json fields

Added to each execution, and to `outcome`. All are optional on load, so a run written
before §43 still loads; each is validated strictly when present (counters not negative,
costs finite, names printable and bounded, paths clean and repository-relative, `diff` on an
implement step only).

| Field | Meaning |
| --- | --- |
| `executions[].usage.partial` | The call ended before the CLI's final report: the counters are summed from the messages it streamed, each message once at its largest values. Input and cache counters are each message's own; `output_tokens` is a lower bound (the stream reports it as a message starts); `cost_usd` is unknown and 0. |
| `executions[].call.prompt_bytes` | The size of the prompt sent on stdin, extensions hint included. |
| `executions[].call.truncated` | The prompt parts `limits.prompt` cut: `test_output`, `diff`. Absent when nothing was. |
| `executions[].call.models[]` | `{model, cost_usd}` for each model that answered, sorted by name; `cost_usd` is absent when not reported. |
| `executions[].call.api_ms` | Time spent waiting on the model API (claude's `duration_api_ms`). |
| `executions[].call.tools` | Tool name → number of calls. |
| `executions[].call.mcp_failed` | The `extensions.mcp` servers the CLI reported as failed to start, sorted; the step ran without their tools. Absent when all started. |
| `executions[].diff.step` | `{files, insertions, deletions, binary}` of the step's own change. Binary files count in `files` and `binary`, never in the line counts. |
| `executions[].diff.run` | The same, for the working tree after the step against `pre_run_ref`; absent without a restore point. |
| `executions[].diff.not_reported`, `.reported_unchanged` | As in §43.2's Code changes. |
| `outcome.duration_ms` | Wall time of the steps, as above. |
| `outcome.active_ms` | Sum of the steps' durations. |
| `outcome.usage.partial_calls` | How many of `outcome.usage.calls` were partial; when not 0, `cost_usd` is a lower bound. |
| `outcome.diff` | The run's net change: the last implement's `diff.run`. |
| `outcome.verify_calls` | The calls of the whole run to the `orch` tools, replace included (§44.3), 0 and absent when there were none. |
| `outcome.approval_required` | `true` when the last implement matched a `policy.paths` rule with `approval: manual` (an earlier implement's matches do not count); absent otherwise. A CI job can read it: the change was not committed by the orchestrator. |
| `options.path_rules` | A copy of `policy.paths` made when the run is created: `{match, require_stages, approval}` in file order. A resume applies these and never the current config's; if the config's rules differ, it prints `Warning: policy.paths changed since the run started; this run keeps the rules it started with`. Absent without rules. Each rule is validated as in the config. |
| `executions[].path_rules` | On an implement that passed the guard and the secret scan: `{match, files, require_stages, missing, approval}` for each rule that matched a file of the run's net diff (`pre_run_ref` against the tree after the step; the step's own diff without a restore point; the runs directory excluded). `files` holds at most 50 clean repository-relative paths; `missing` is always present and a subset of `require_stages`. Recorded also when the step failed because of it. On an implement step only. |
| `org_config`, `resumes[].org_config` | `{path, sha256}` of the org file the run (or that resume) ran under (§45.3): `path` absolute, `sha256` 64 hex. Absent without an org file. |
| `task.clarification` | `{threshold, checks[], status, rounds[]}`, written once before the decision and never changed (§49.9). Absent when the first check cleared the threshold, with `clarify.enabled: false`, for a story task and after a failed pre-check. Validated strictly on load. |

`policy.paths` rules (specs/governance/SPEC.md §4) are checked on that net diff after each
implement, with git's own file list and never a model's `changed_files`. A matching rule whose
`require_stages` the workflow does not contain fails the step with `policy.paths "<match>"
requires <stages>, which this workflow does not run (changed: <files>)`; the workflow is never
rebuilt. If rules exist and git could not list the changed files, the step fails with
`policy.paths cannot be checked: git could not list the changed files`. At the start of a run
whose workflow has implement, each rule the workflow cannot satisfy prints a warning on stderr
(also after an edit on the review screen). `deny_change` is rejected in the config in favor of
`security.protected_paths`.

`decision.json` gains two fields (§45.4). `policy_hash` is the sha256 (64 hex) of the
`DecisionPolicy` the decision used; it is written with every provider or user decision and
absent for `source: manual`. `stages[].explored` is `true` for a stage `policy.explore` turned
on; it is valid only on a stage that is `enabled` and not `forced`. Both are optional on load.

`specs/decision-shape` adds three (§9): `complexity.sources.<src>.size` (in [0, 2]),
`complexity.cuts` (`0 < medium < complex <= 2`) and `questions_hash` (the sha256, 64 hex, of the
shared questions as asked; calibration groups sizes by it). All are absent for
`source: manual` and in traces written before them, and a stage that was not asked
(investigation, testing, review) has no `probability` or `threshold`. `specs/clarify` adds
`clarity` (`{threshold, checks[], status, reason}`, §49.9) and `usage.clarify[]` and
`usage.clarity_recheck[]`; all are optional on load, so a trace written before them still
loads. A trace written before `specs/clarify` may still name the stages `analysis` and
`planning`; `decisions report` and `decisions calibrate` read it as it is, and a resume
refuses the run (§37.3).

The fields of the quality loop (checks, verify, criteria, `by_complexity`, no progress) are
listed in §44.9.

A step's `diff` is recorded only for an implement whose call succeeded and whose changes
the guard passed (§38.3): git runs on the working tree only after the fingerprint and the
protected paths are checked. The trees are written with a throwaway index, so the working
tree, the user's index and every ref are untouched. A failed implement, whose files may
have changed, records no diff; the next completed implement's `diff.run` covers them.

## 43.4 Not covered

* An estimate of a partial call's cost or output tokens: no prices are kept in the
  orchestrator, and a guess would look like a measurement.
* API time and cost for Codex, and provider metrics for Gemini: those CLIs do not report
  them in a form the adapters read.
* The changes a test command makes, and a diff for research/investigate (read-only).
* A machine-readable summary beyond `run.json` itself; `orchestrator runs report`
  aggregates over many runs.

---

# 44. Quality loop

Normative. Design record, decisions and what was built differently: `specs/quality/`
(`SPEC.md`, `README.md`, `tasks/`). Each stage is a separate cold call, so the result is only as
good as what a stage is told and how soon the run learns that it is wrong. This section makes
the stages pass on more of what they found, adds deterministic gates, lets `implement` run the
tests during its session without a shell, checks the investigation's criteria in review, ends a fix loop
that goes nowhere and lets a config table pick the model per decided complexity.

Rules that hold for all of it:

* **Only exit codes decide** (§31.4). Checks are exit codes. Parsed test names only name tests in
  a prompt. The in-session `run_tests` is a hint; the `test` step is the truth.
* **No shell for `implement`** (§38). The verify tools run a command from config, never from the
  model, in the sandbox, after a guard comparison.
* **Go decides, from config and the persisted run.** No model picks a model, a stage, a
  transition or a stop. Every transition is derivable on resume from `run.json` (§37).
* **Opt-in where it costs money or changes behavior:** `actions.implement.verify`,
  `actions.<name>.by_complexity`, `review.fix_medium` and `actions.review.models` are off
  unless configured. The richer prompts, `checks` (once listed) and the no-progress stop are on.
* Every new config key is declared in the schema (strict parsing, no version bump) and every
  new `run.json` field is validated strictly on load.

## 44.1 Stage context

What each stage's prompt carries (§17, §20):

| stage | gets, besides what it always had |
|---|---|
| investigate | research's findings, and the clarification (§49.4) |
| implement | the investigation's `affected_files` and `risks`, `steps` and `acceptance_criteria`; the clarification |
| implement (retry) | the git diff --stat of what the run changed so far, against `pre_run_ref` |
| review | the investigation's `steps`, `risks` and numbered `acceptance_criteria`; the clarification |

Lists are `- item` lines under a heading; an empty list is left out. The investigation's JSON
(the plan, when development follows) carries
`"acceptance_criteria": ["..."]`: at most 8 observable, checkable statements about the result.
The parser stays lenient (a missing field is an empty list); Go keeps the first 8 non-empty
entries and cuts each to 300 bytes on a UTF-8 boundary, so a resume reads the same list from
the artifact. The retry's diff stat is rendered by Go from `Repository.DiffNumstat` between the
tree of `pre_run_ref` and the working tree, fenced (`action.fence`) and bounded by
`limits.prompt.max_diff_bytes` (the first files, the totals line, then `… N more files`). It is
computed when the prompt is built and never stored. If git cannot tell, the step goes on
without it and a warning is shown. The six stage prompts have golden files under
`internal/action/testdata/prompts/` (`go test ./internal/action -update`).

## 44.2 Checks

```yaml
checks:                      # optional, at most 16
  - {name: build, command: "go build ./..."}
  - {name: vet,   command: "go vet ./..."}
  - {name: lint,  command: "golangci-lint run --new-from-rev=$ORCH_BASE_REF"}
test:
  command: "go test ./..."   # required when checks is set
```

* `name` matches `^[a-z0-9][a-z0-9_-]{0,31}$` and is unique; `command` is not empty. A check is
  trusted operator config like `test.command` and runs through `sh -c`, the documented
  exception to argv-only execution.
* Checks are **not a new action type** (§30). The `test` step runs every check in order, then
  `test.command`, and stops at the first non-zero exit code. A workflow without a `testing`
  stage never runs them; testing comes with every workflow that has development (§9).
* Every command runs through the same `testexec.Runner`, under the same sandbox and with the
  same `execution.TestEnv()` allowlist plus `test.env_pass`, with `ORCH_BASE_REF=<pre_run_ref>`
  added (empty when no restore point was recorded; set for `test.command` too). The guard takes
  one snapshot before the first command and compares after **each** one, fingerprint first: a
  violation stops the step and the next command never runs. The secret scan runs once after
  the step.
* `TestResult` gains `checks` (every command that ran: `name`, `exit_code`, `success`,
  `duration`) and `failed_check`. When a check fails, the top-level `command`, `exit_code`,
  `stdout` and `stderr` are the failing check's; when every check passes they are
  `test.command`'s. `success` is true only if every command exited 0.
* A retry after a failed check says which check failed, shows its command, exit code and
  fenced, bounded output, and says the tests did not run.
* The pre-run test (§41.5) runs the same sequence.
* Each check's full output goes to `logs/<NNN>-test.<check>.{stdout,stderr}.log`, and the
  pre-run test's to `logs/pre-run-test.<check>.{stdout,stderr}.log`.

## 44.3 Verify inside implement

```yaml
actions:
  implement:
    provider: claude
    verify: true             # default false; implement only
limits:
  verify:
    max_calls_per_step: 8    # default 8, 1..50; both tools together, refused calls included
    call_timeout: 10m        # default 10m; one run of the test command or a check
```

With `verify: true` the implement session gets two tools from an MCP server named `orch`,
which the orchestrator serves itself (`orchestrator mcp-verify --dir <dir>`, a hidden
subcommand claude starts over stdio):

| tool | arguments | runs |
|---|---|---|
| `mcp__orch__run_tests` | `filter` (optional string) | `test.verify_command`, else `test.command`; returns the exit code and bounded output |
| `mcp__orch__run_check` | `name` (one of the configured check names, an `enum`) | that one check; offered only when `checks` is not empty |

* **Not a shell** (D16, §38.1). The command comes from config; the model supplies only whether to
  run, which configured check, and a `filter` matching `^[A-Za-z0-9_./:-]{1,200}$`. Anything
  else (`;`, a space, `$(…)`, a quote, an empty string, 201 bytes) is refused and runs
  nothing. A valid filter is passed only as `ORCH_TEST_FILTER` of that one run, never put
  into a command line; whether `test.command` uses it is the operator's choice
  (`'go test ./... ${ORCH_TEST_FILTER:+-run "$ORCH_TEST_FILTER"}'`). The `test` step never sets it.
* **`test.verify_command`** (optional, only with `verify: true`, else a config error) is what
  `run_tests` runs in place of `test.command`: a script that narrows the tests to what the run
  changed, which it finds itself with `git diff --name-only $ORCH_BASE_REF` and
  `git ls-files -o --exclude-standard` (the sandbox binds the git directories read-only). Which
  tests that is stays the operator's script, not Go and not the model; the filter can only
  narrow it further. The `test` step and the pre-run test always run `test.command`, so the
  narrowing is a hint only (§31.4). This repository's is `scripts/check-changed.sh`.
* **Fail fast** (`Validate`, `ValidateDependencies`): `verify: true` needs `test.command`,
  `test.sandbox` other than `none` and provider `claude`; `verify` on any other action is an
  unknown key; `extensions.mcp.orch` and any server name containing `__` are refused.
* **Wiring.** The dispatcher creates a private `orch-verify-*` directory (0700) outside the
  repository, writes `verify.json` (version 1: the repository root, `test.command`, the checks,
  `pre_run_ref`, the sandbox, the computed test environment, the guard's before-state, the
  limits) and merges the `orch` server into the step's `--mcp-config` with the two tool
  names in `--allowed-tools`; the adapter's `--tools` is unchanged. The server reads no input
  but `verify.json` and its stdin (replace, below, reads and writes the repository's files), opens no run directory, makes no network call, and writes only
  `calls.jsonl`, `violation` and per-call logs inside the directory. The directory is removed
  when the step ends, and a start of `run` or `resume` removes your own `orch-verify-*`
  directories older than a day.
* **Guard before every run.** The server compares the repository with the step's before-state
  (fingerprint first) before each call. A change, or a guard error, makes it run nothing, write
  `violation`, answer `isError: true` and refuse every later call in the step. The `violation`
  marker fails the step even if the tree was restored afterwards; the dispatcher's own guard
  comparison after the call is a second signal. The runs happen only in bwrap, with the git
  directories read-only, no network and a tmpfs `$HOME`.
* **Limits.** `max_calls_per_step` counts every call of the two tools, refused ones included;
  past it calls are refused with "limit reached". `call_timeout` kills the process group and
  answers exit code -1 with "timed out". The implement step's own timeout
  (`execution.timeout`, `actions.implement.timeout`) bounds the total, so raise it when verify
  is on.
* **Record.** `executions[].verify_calls` and `outcome.verify_calls` (§44.9), full output in
  `logs/<NNN>-implement.verify-<K>.{stdout,stderr}.log`, and the `Verify` row of `summary.md`.
  The implement prompt gains one paragraph about the tools only when verify is on, and says
  the orchestrator runs the tests again and only that run counts.
* The server speaks a JSON-RPC 2.0 subset over newline-delimited stdio (`initialize`,
  `notifications/initialized`, `tools/list`, `tools/call`, `ping`), with the standard library only.

**Replace (always on).** Every implement session whose adapter speaks MCP (claude, codex) gets
`mcp__orch__replace` from the same `orch` server, with or without `verify` and with no config
key; a provider without MCP support gets no server. Arguments: `old` and `new` (literal,
at most 1000 bytes), `word` (whole identifiers only), `paths` (files, directories or globs;
`*.go` matches file names) and `dry_run`. It rewrites, in Go and without starting a process,
the files git lists (ignored ones never), skipping the secret deny list and the protected
paths unread, and symlinks, binary files, files over 1 MiB and anything resolving outside the
repository. The guard compares the before-state before each call as for the test tools; at
most 50 replace calls per step, apart from `max_calls_per_step`. Each call is a
`verify_calls` entry with `files` and `replacements`, never the text, and `summary.md` shows
it in a `Replace` row. When replace alone cannot be set up, the step warns and runs without
it. The implement prompt names the tool. Design: `specs/quality/SPEC.md` §6.11, D18.

## 44.4 Review

* **A verdict per criterion.** When the investigation has `acceptance_criteria`, the review
  answer has
  `criteria: [{index, met, evidence}]`: exactly one entry per criterion, indexes `1…N` each once,
  `evidence` of 1 to 500 bytes; anything else is a malformed review (a hard step error). Go
  fills `criterion` from the investigation (a story task's `acceptance` items come first,
  §48.5). An unmet criterion counts as a `high` issue, so
  `ReviewResult.Decide()` fails the review, and the retried implement is told which criteria
  were unmet, with the evidence, before the issues. Without criteria the answer has none.
* **Read the surroundings.** The review prompt says the reviewer has Read, Grep and Glob and
  should read the callers, the interfaces and the tests around each changed function; an issue
  about code outside the diff is valid if the change causes it.
* **`review.fix_medium`** (default `false`). A review that passes (no critical or high issue,
  every criterion met) but reports a `medium` issue sends the run back to implement once, with
  the medium issues in the retry prompt. It happens only if `fix_iterations <
  max_fix_iterations` and at most once per run; it is a fix iteration. Then test and review run
  as usual, and the second review decides as always. The workflow outcome is `fixable`
  (§37.5). The option is recorded when the run starts (`options.review_fix_medium`) and read
  from there, so a resume with a changed config follows the original run.
* **Extra reviewers** (`actions.review.models`, default `[]`, review only). A list of up to 4
  model names on the action's provider, never repeating the base model, a `by_complexity`
  model of review or an earlier entry. The step calls the base model, then each listed model,
  **sequentially**, with the same prompt, the same authority (`read`) and the base effort
  (`by_complexity` does not apply to them). Each answer is parsed and validated strictly on its
  own; one malformed answer fails the step. Go merges them (`mergeReviews`): issues are the
  union, de-duplicated by (severity, file, normalised description); a criterion is met only if
  every reviewer says so, with the evidence of the first that does not; `Decide()` runs on the
  merged result. The review artifact and `run.json` keep every reviewer's answer
  (`review_result.reviewers: [{model, result}]`, the base model first); `executions[].usage`
  sums the calls and `call.extra_calls` holds one compact entry per extra reviewer. This is one
  step with calls in a fixed order and a merge Go decides, not a parallel or multi-agent
  workflow (§30).

## 44.5 Configuration summary

```yaml
checks:                          # §44.2
  - {name: vet, command: "go vet ./..."}
test:
  verify_command: ""             # §44.3, run_tests in place of test.command; needs verify
actions:
  implement:
    verify: false                # §44.3, implement only
    by_complexity: {}            # §44.8, any action with a provider
  review:
    models: []                   # §44.4, review only
review:
  fix_medium: false              # §44.4
limits:
  stop_without_progress: true    # §44.6
  verify:                        # §44.3
    max_calls_per_step: 8
    call_timeout: 10m
```

Every key is optional. For a config that sets none of them the behavior changes only where it
is on by default: the richer prompts (§44.1) and the no-progress stop (§44.6). Invalid values
are config errors (exit 2) before any call: a bad `checks` entry, `checks` without
`test.command`, `verify` without a sandbox or on another action, `test.verify_command` without
`verify`, a reserved or ambiguous MCP
server name, an invalid `by_complexity`, `models` on a non-review action.

## 44.6 Stop without progress

With `limits.stop_without_progress` (default `true`) the engine, after a step that sends the run
back to implement, compares the last completed execution with the previous failure of the same
kind in this run (`workflow.NoProgress`, persisted data only, no model call):

| signal | compared with | reason |
|---|---|---|
| implement's `tree_after` (a git tree id) | the tree after the previous implement | `no progress: implement changed nothing` |
| a failed test: `failed_check`, exit code and parsed failing names, else a hash of the bounded output with digits replaced by `#` | the previous failed test | `no progress: the same tests fail` |
| a failed review: every critical/high issue and unmet criterion matches one of the previous failed review | the previous failed review | `no progress: review repeats the same issues` |

"Matches" for an issue: the same `file` and descriptions whose word sets (lower case, letters
and digits, words of 3+ characters) have a Jaccard similarity of at least 0.6; an unmet
criterion matches on its text. A test or review that passed between the two failures means the
run moved, so no signal fires. The run stops with status `stopped`, exit code 4, and spends no
more iterations. `max_fix_iterations` is read first, so a run that exhausted it keeps its
usual reason. A resume of such a run goes into implement (§37.5), and `max_fix_iterations`
still applies. `false` restores spending every iteration.

## 44.7 Failing test names

`internal/testreport` (standard library and `domain`) reads failing test names out of a failed
test step's stdout and stderr for `go test -json`, `go test` text, pytest (`FAILED` lines
of the short summary) and jest (`--json` or the text tree). The result is `{format, failed}`
with sorted, de-duplicated names, at most 200, each at most 300 bytes. An unrecognised format
gives no format and no names and changes nothing. `TestResult` gains `failed_tests` and
`report_format` (and so does `pre_run_test`); the implement retry prompt adds `Failing tests
(parsed from the output): a, b, c` (at most 30, then `, … and N more`) above the fenced output,
so the names survive when the output is cut.

When the run started red (`run --allow-red-tests`) and both the pre-run test and the failed
test step have names in the same format, the retry prompt splits them: `Failing before this run
started (the task may be to fix them): …` and `New failures since the run started: …`, and
`summary.md` shows `N failing before the run, M new`. Both are hints: the step still fails on a
non-zero exit code even if every failure is old, and nothing in `outcomeOf`, `Next` or a stop
depends on a name except the no-progress signal above, which compares them with each other.

## 44.8 Complexity chooses the model

```yaml
actions:
  implement:
    provider: claude
    model: sonnet
    effort: medium
    by_complexity:
      simple:  {model: haiku, effort: low}
      complex: {model: opus,  effort: high}
```

* `by_complexity` is optional on every action that has a provider (research, investigate,
  implement, review; the clarify step runs before there is a complexity and has none). Its keys are `simple`, `medium` and `complex`; each value has `model`
  and/or `effort`, and a missing field falls back to the action's base value. There is no
  `provider` key (the strict parser rejects it): the provider is fixed per action, so the
  dependency and authority checks of §7 still cover every call. `effort` follows the usual
  rules and applies only to providers that accept it (claude).
* Go picks once per step: if the run's decision has a complexity and the table has an entry
  for it, that entry; otherwise the base. A manual decision (`run --stage`) has none and gets
  the base. Extra reviewers (§44.4) are not routed.
* **Escalation** (`actions.implement.escalate: true`, implement only, needs `by_complexity`,
  default off): an implement that fixes a failed test or review - the last execution before
  it that is not an implement is a test or a review - runs on the entry one level above the
  decided complexity (simple → medium → complex; complex, a level above without an entry and
  `--stage` keep their route), recorded as `model_source: escalated.<level>`. One level,
  however many fix iterations follow. A resume derives the same route from the history.
  `decision.json: models` does not list it. An entry's own `escalate: {model, effort}` (implement
  only, needs no `escalate: true`) takes precedence for its level, complex included: the fix
  runs on the entry with the escalate's non-empty fields over it, recorded as
  `model_source: by_complexity.<level>.escalate`. With a plan to follow, the first implement can
  run on a cheaper model and a failed check moves the fix to the strong one.
* **Record.** `executions[].provider.model_source` is `base` or `by_complexity.<level>`, and
  `decision.json: models` lists, for every action of the workflow that makes a call, the
  provider, model, effort and source the decision implies, written when the decision is made.
  A resume recomputes the route from the persisted decision and the current config and prints
  a `Warning:` line for each remaining action whose model or effort differs from
  `decision.json: models` (`implement model changed since the run started: opus → sonnet`),
  for any model change, not only for `by_complexity`.

## 44.9 Field reference

`run.json`, each validated strictly on load (`persistence/validate.go`):

| field | where | meaning and validation |
|---|---|---|
| `options.review_fix_medium` | run | the `review.fix_medium` of the config when the run started; absent when every option is off |
| `checks[]` | `test_result`, `pre_run_test` | `{name, exit_code, success, duration}` of each command that ran; names valid per §44.2; a failed check is the last entry |
| `failed_check` | `test_result`, `pre_run_test` | the name of the failing `checks[]` entry; absent when every check passed |
| `failed_tests`, `report_format` | `test_result`, `pre_run_test` | §44.7; a failed result only, a known format, at most 200 names of 1 to 300 bytes without control characters |
| `verify_calls[]` | implement executions | `{tool, check, filter, exit_code, success, duration, output_cut, refused, files, replacements, dry_run}`; `tool` is `run_tests`, `run_check` or `replace`; `check` only on `run_check` (and only valid names); `filter` only on `run_tests` and matching the filter rule; `files`, `replacements` (at least `files`) and `dry_run` only on `replace`, whose `exit_code` is 0, or 1 for a write that failed part way; a refused call has a short one-line `refused` and no result, a call that ran has `success == (exit_code == 0)`; at most 100 |
| `tree_after` | implement executions | the git tree of the working tree after the step; 40 or 64 hex characters |
| `provider.model_source` | executions | `base`, `by_complexity.{simple,medium,complex}`, `by_complexity.<level>.escalate` or `escalated.{medium,complex}` (§44.8) |
| `review_result.criteria[]` | review executions | `{index, criterion, met, evidence}`; indexes `1…N` unique; evidence at most 500 bytes |
| `review_result.reviewers[]` | review executions | `[{model, result}]`, the base model first, absent for a single reviewer; a reviewer has no reviewers of its own |
| `call.extra_calls[]` | review executions | `{model, api_ms, input_tokens, …}` per extra reviewer; at most 4 |
| `outcome.verify_calls` | outcome | the verify calls of the whole run, not negative |

`decision.json` gains `models` (§44.8). Artifacts: the investigation (the plan) gains `acceptance_criteria`, the
review artifact `reviewers`. Logs gain `<NNN>-test.<check>.*`, `pre-run-test.<check>.*` and
`<NNN>-implement.verify-<K>.*`. `summary.md` gains the rows `Verify`, `Failing tests` and
`Reviewers` and the `failed (check …)` and `N/M criteria met` results (§43.2).

## 44.10 Not covered

* **Worktree isolation** (TODO Q4) and **the cost of the decision rounds** (TODO Q8): later,
  with their own specs.
* **Parsed names as a gate.** Letting a run pass while "only old" tests fail would make success
  depend on parsed text (§31.4).
* **A shell, parallel verify calls or any tool beyond `run_tests`, `run_check` and `replace`**, and verify
  for Gemini (Codex uses its configured MCP tool bridge; Gemini has no verified adapter).
* **Model choice by anything but the decided complexity** (cost so far, failures).

---

# 45. Governance

Control for teams (phase 4: path rules, the org layer, the budget, calibration and
exploration). This section is normative; the design record is `specs/governance/`. The
sections it touches say the same in their own place: §7 (fail fast), §9 (exploration,
`policy_hash`), §12 (the budget), §37 (resume), §42 (approval), §43 (summary and fields).

## 45.1 Path rules

```yaml
policy:
  paths:
    - match: "migrations/**"
      require_stages: [review, testing]
      approval: manual
    - match: "**/*.sql"
      approval: manual
```

* `match` is one pattern in `security.protected_paths` syntax. `require_stages` names stages
  (`research`, `investigation`, `development`, `testing`, `review`) without duplicates.
  `approval` is `manual` or absent. A rule needs `require_stages`, `approval`, or both.
* Rules apply in file order and every matching rule applies; requirements add up.
* `deny_change` is rejected with a message pointing at `security.protected_paths`: the guard
  already refuses any change there, git-ignored files included.
* **The diff is the truth.** After each implement that passed the guard and the secret scan,
  the rules match the run's net diff (`pre_run_ref` against the tree; the step's own diff
  without a restore point; the runs directory excluded), never a model's `changed_files`. A
  file one attempt changed and a later attempt reverted does not match. Git-ignored files are
  not in the diff.
* **No workflow rebuilt mid-run.** A matching rule whose `require_stages` the workflow lacks
  fails the step (`policy.paths "<match>" requires <stages>, which this workflow does not
  run (changed: <files>)`); a resume would stop again, so start a new run with `--stage`. At
  the start of a run whose workflow has implement, each rule the workflow cannot satisfy
  prints a warning, because stopping is still cheap then.
* **Approval** changes only the commit offer (§42): the question lists the matched rules and
  files; `auto_commit: true` does not commit. The run's status and exit code are unchanged;
  `outcome.approval_required` is true, which a CI job can read.
* **Resume** applies `options.path_rules`, the rules recorded at run start, and warns when
  the current config's differ.

## 45.2 The budget

```yaml
limits:
  max_cost_usd: 5.00
```

A positive, finite number; absent means no budget.

* **Fail fast (§7).** The budget is enforceable only when every provider call it covers
  reports a cost: the actions' routes, every extra reviewer, every `by_complexity` model and,
with `clarify.enabled: true`, the clarify route.
  `provider.CostReporter` (`ReportsCost() bool`) is implemented by the claude adapter only;
  any other routed provider is a config error, `limits.max_cost_usd cannot be enforced:
  codex (actions.review) reports no cost`. A silently unenforced budget is refused.
* **Check.** Before each step the engine sums `usage.cost_usd` over the persisted
  executions. At or over the budget, the run is `stopped` with `budget exceeded: $5.12 of
  $5.00`, exit code 4. A resume counts what was already spent and continues once the budget is
  raised.
* **Limits, not hidden.** A step that starts under budget can end over it by one step's cost.
  The translation, pre-check and classification calls are not counted (`decision_overhead`
  reports them); the clarify call and the clarity re-checks are, and so is the usage a resume
  reads back from `decision.json` (`usage.clarify`, `usage.clarity_recheck`). A call cut off before
  its final report has no cost, so the sum is a lower bound and the reason says so
  (`(lower bound: 1 calls without a final report)`). Verify calls run the test command, not a
  provider, and cost nothing here.
* There is no `pricing:` and no `max_tokens`: the CLI reports the cost.

## 45.3 The org config layer

The org file is `/etc/orchestrator/org.yaml`. `ORCHESTRATOR_ORG_CONFIG` names another path
only when that file does not exist, so an environment variable cannot point a machine-wide
policy away. With neither, there is no org layer. A file that exists but cannot be read or
parsed is a config error, never "no policy". `orchestrator.example.yaml` has a sibling,
`org.example.yaml`.

The org file has its own strict schema (`config.OrgConfig`, `version: 1`); any other key is
an error. What the repository config may then do:

| org key | meaning | the repository config |
|---|---|---|
| `security.require_clean_worktree: true` | floor | may not set `false` |
| `security.protected_paths` | added to the repository's list | only adds |
| `security.allow_unverified_cli: false` | floor | may not set `true` |
| `security.secret_scan.mode` | floor, `off` < `warn` < `stop` | equal or stricter |
| `security.secret_scan.allow_paths` | owned by the org once it sets `secret_scan` | may not set its own |
| `test.sandbox: bwrap` | floor | may not set `none` |
| `policy.paths` | org rules first, then the repository's | only adds |
| `policy.stages.force` | added to the repository's, per complexity | only adds |
| `limits.max_cost_usd` | ceiling | lower only, never higher or absent |
| `allowed_models` (org only) | the models a route may name | routes only to these |

`allowed_models` covers every model the run can call: each action's base model and
`by_complexity` models, extra reviewers, the translator, the clarify step (when enabled) and the
LLM decision provider. A
route with no model is an error under it. Thresholds (`policy.clarify.threshold` among them: no
floor, `specs/clarify` A4), `auto_commit` and the decision, Jev and Jira settings are not in the
org schema on purpose. An org `policy.stages.force` naming `analysis` or `planning` is refused
with the replacement, like the repository's.

`config.LoadLayered(repoPath, orgPath)` replaces `config.Load` in the fail-fast chain (§7), so
`Validate` and everything after see the merged config. Every conflict is reported at once,
naming both files. `validate` and `run` print the org source. `run.json` records
`org_config: {path, sha256}`, and each `resumes[]` entry its own; a resume under a changed
org file warns and continues under the current one, while the rules a run recorded at its
start still win (§45.1).

## 45.4 Calibration and exploration

* **`policy_hash`.** `decision.json` records the sha256 of the canonical JSON of the
  `DecisionPolicy` the decision used (absent for `source: manual`). It changes exactly when a
  threshold does. `decisions report` and `decisions calibrate` group by it. `policy.explore`
  is not part of it.
* **`decisions calibrate [--runs <dir>] [--min-runs N] [--size-target S/M/C] [--simple-max-hard F]`** (read-only, `internal/report`)
  prints, per decision provider, model and `policy_hash`, the runs where testing or review
  ran in probability buckets of 0.1 and how often the stage paid off (testing: a test failed
  after an implement; review: a failing verdict followed by an implement). A threshold is
  raised to the lowest bucket edge above it such that the stage paid off in none of at least
  `--min-runs` (default 10) runs below it; it is lowered only with exploration data, to the
  lowest edge below which explored runs paid off at least as often as the runs above. A
  bucket with fewer runs prints `not enough data`. Research and investigation get a
  comparison table and no proposal; a table per complexity and model (runs, completed share,
  mean `fix_iterations`, mean cost) has none either. Manual, chosen-by-hand and mock
  decisions are skipped. The output ends with a YAML diff of `policy.stages.thresholds` and
  the rows behind each line. Testing and review have probabilities only in decisions made
  before `specs/decision-shape`, so their proposals apply to those groups.
* **Size and cuts** (`specs/decision-shape/SPEC.md` §7). Per decision model and
  `questions_hash` (not `policy_hash`: size does not depend on the cuts), calibrate prints the
  spread of size with a histogram, the levels decided, each stage's correlation with size
  (r ≥ 0.8 marked `redundant with size`), the cuts for a target share (`--size-target`,
  default 50/35/15; equal sizes stay in one level), and the hard runs (a fix iteration, or
  stopped by a limit; a failed run with no fix is left out) per size bin. The medium cut by
  outcome is the highest edge below which at least `--min-runs` runs were at most
  `--simple-max-hard` (default 0.2) hard; it lowers the target's medium cut, unless the runs
  below the median size were hard at least as often as the rest. An interquartile range of
  size under 0.2 refuses both. A YAML diff of `policy.complexity.cuts` follows the thresholds'
  diff. **Calibration only proposes**: nothing is written and it never
  runs as part of `run`.
* **Clarity** (`specs/clarify`, §49.9). Per decision model and `questions_hash`, calibrate
  prints the distribution of the first check's `P(clear)` in buckets of 0.1 (from
  `decision.json: clarity.checks[0]`), the threshold the runs recorded, how many runs had a
  re-check and how the last check ended (`clear`, `unclear`, `disabled`), and, over all the runs
  read, the share that stopped `task needs input` with the distribution of their last
  `P(clear)` (such a run has no `decision.json`; its checks are in `run.json:
  task.clarification`). The section is left out when no run was asked the question and none
  stopped. **It proposes no clarity threshold** (A1): `policy.clarify.threshold: 0.80` is a
  starting value, and a share of stopped runs that is too high says to lower it, a distribution
  with no mass near it says it cuts nothing.
* **`policy.explore`** (opt-in, absent by default):

  ```yaml
  policy:
    explore:
      band: 0.05   # (0, 0.2]
      rate: 0.1    # (0, 0.5]
  ```

  For a stage the policy left off, not forced, with P within `band` below its threshold,
  `fnv64a(run_id + "/" + stage) mod 1000 < rate × 1000` turns it on. It only turns stages
  on, so a run costs more and is never less checked. The stage is recorded as
  `stages[].explored: true` with the reason `explored (P 0.47, threshold 0.50)`. The same
  decision plus the same run id gives the same workflow; a resume reads the persisted
  workflow and never explores; `--stage` never explores. `policy.Explore` is a pure function
  in `internal/policy`.

## 45.5 Honesty note

On a developer's machine every rule here holds only while the developer cooperates: they own
the machine, the files and the CLI, and root can delete the org file. The rules are a real
boundary where the operator does not control the machine (CI, a managed host). The org layer
protects against a repository's config, not against the machine's owner.

## 45.6 Not covered

* **Audit export** (TODO G4): dropped. A local CLI gives no central trail, a hosted runner
  already keeps the run directory, and a hash chain in a local file protects nothing.
* A config written back by calibration; automatic thresholds for research or investigation or
  for `policy.clarify.threshold`; exploring models; a `--stage` run that still records probabilities.
* Per-user or per-team policy beyond one org file, fetching the org config over the
  network, and enforcing anything against the machine's owner.

# 46. Claude credentials

Every call to the `claude` CLI (workflow steps, the translator, the clarify step, the LLM
decision provider) runs
under one credential mode per process. Design record and the owner's answers:
`specs/claude-auth/`.

## 46.1 Modes and resolution

* **`api`**: a Claude Console API key from the environment variable
  `providers.claude.api_key_env` (default `ANTHROPIC_API_KEY`), billed per token.
* **`login`**: whatever the CLI itself is logged in with (a claude.ai subscription,
  `CLAUDE_CODE_OAUTH_TOKEN`, a Console login, a cloud provider, a gateway). The orchestrator
  never gives it an API key.

`requested` is `--claude-auth` if given, else `providers.claude.auth`, else `auto`.
`config.ResolveClaudeAuth` is one function over the config, the flag and an environment lookup,
called in the fail-fast chain (§7) so `validate` and `run` answer alike:

| requested | key variable | effective mode | console |
|---|---|---|---|
| `api` | set, valid | `api` | `claude auth: api key ($VAR)` |
| `api` | unset, blank or invalid | config error, no run | |
| `auto` | set, valid | `api` | `claude auth: api key ($VAR, auto)` |
| `auto` | unset | `login` | `claude auth: CLI login (auto: $VAR not set)` |
| `auto` | set but blank | `login` | warning: `$VAR is set but empty; using the CLI login` |
| `auto` | set, invalid | config error, no run | |
| `login` | anything | `login` | if set: `$VAR is set but not used (--claude-auth login)` |

The result `{requested, mode, decided_by: flag|config|default, key_env}` is set on the claude
adapter by `app` before the first call and persisted (§46.6). The mode is static: a failure
never switches it (§31.1).

## 46.2 Configuration and CLI

`providers.claude.auth` (`auto | api | login`) and `providers.claude.api_key_env` are parsed
strictly; `providers.claude.api_key` (a literal) is declared only to be rejected ("name an
environment variable via providers.claude.api_key_env instead"). `api_key_env` must be an
environment-variable name, not a parent-session variable, and joins
`Config.CredentialEnvNames()`: every child process loses it through `ScrubbedEnv()` and
`test.env_pass` refuses it; the matching adapter puts the value back under the provider's
canonical variable. `providers.gemini` remains unsupported and there is no org-layer floor
for provider credentials.

`providers.claude.api_env` is a map of variables for claude in `api` mode (§46.3), for a key
whose endpoint is a gateway with its own model names:

```yaml
providers:
  claude:
    api_env:
      ANTHROPIC_BASE_URL: https://ai-gateway.example.internal
      AWS_PROFILE: claude-bedrock
      ANTHROPIC_DEFAULT_OPUS_MODEL: bedrock/eu.anthropic.claude-opus-5-5
```

Routes keep `opus`/`sonnet`; the CLI resolves the alias. `Validate` refuses a name that is not
an environment-variable name, `ANTHROPIC_API_KEY` or `api_key_env`, a variable `api` removes
(`CLAUDE_CODE_USE_*` included), a parent-session variable, a name in
`Config.CredentialEnvNames()` or a credential-like name (the `test.env_pass` rule). It refuses
an empty value, a control character, a URL with userinfo, and a value `internal/secretscan`
flags. Every message names the variable, never the value. With the org's `allowed_models`
(§45), the values of `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`,
`ANTHROPIC_CUSTOM_MODEL_OPTION` and `ANTHROPIC_DEFAULT_*_MODEL` must be on the list, like a
route's model.

`--claude-auth auto|api|login` is valid with `run`, `resume` and `validate` in any position,
`--claude-auth X` or `--claude-auth=X`, once; anything else is a usage error.

## 46.3 The child's environment

Built in `internal/provider/claude` from `execution.ScrubbedEnv()`, also for the
`claude --version` probe:

| variable | `api` | `login` |
|---|---|---|
| `ANTHROPIC_API_KEY` | set to the key | removed |
| `ANTHROPIC_AUTH_TOKEN` | removed | inherited |
| `CLAUDE_CODE_USE_BEDROCK`, `_VERTEX`, `_FOUNDRY` | removed | inherited |
| `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_PROFILE`, `ANTHROPIC_FEDERATION_RULE_ID` | removed | inherited |
| `ANTHROPIC_BASE_URL` | inherited, warned once (host only) | inherited |
| each name in `providers.claude.api_env` | set to the configured value, replacing an inherited one | removed |
| everything else | inherited | inherited |

The variables a mode removes that were set are named once on the console (`ignored in api
mode: …`, `ignored in login mode: …`), never their values. In `api`, `ANTHROPIC_BASE_URL` gets
one warning with its host and its source, `(providers.claude.api_env)` or `(inherited from the
environment)`. With `api_env` the console also prints `claude env (api): ANTHROPIC_BASE_URL
(host), AWS_PROFILE, …`, showing no value except that host. No other process gets `api_env`:
not the test command, git, the verify server's test processes, codex or gemini. The key
is read once in `app`, trimmed; whitespace or a control character inside it, is read once in `app`, trimmed; whitespace or a control character inside it,
or the prefix `sk-ant-oat` (a `setup-token` subscription token), is a config error. It is
passed only through the adapter's `Env`, never argv, config, `run.json`, logs or prompts, and
is redacted from every error and output. The `orch` verify server never passes it on.

## 46.4 The `apiKeySource` check

`parseStream` reads `apiKeySource` from the stream-json `system/init` event. After the call:

| mode | `apiKeySource` | result |
|---|---|---|
| `api` | `ANTHROPIC_API_KEY` | ok |
| `api` | anything else | the call fails, `FailureAuth` |
| `login` | `ANTHROPIC_API_KEY` | the call fails, `FailureAuth` (defensive) |
| `login` | anything else | ok, recorded |
| any | no init event (a test double, plain text) | not checked, recorded as `""` |

The check runs after the process ends, so the call that reveals a mismatch has already run.
No `--bare` is used in any mode: it would drop CLAUDE.md and user settings, so the same task
would get a different context depending on who pays.

## 46.5 `FailureAuth`

The `apperr` failure kind `FailureAuth` (exit code 2) covers the mismatch of §46.4 and a call
whose stream carries `system/api_retry`, or an assistant API-error message, with `error` in
`authentication_failed`, `oauth_org_not_allowed`, `account_on_hold` or `billing_error`. It is
fatal everywhere: the
translator does not fall back to the original text, the LLM decision provider does not lead to
the manual choice whatever `on_failure` says, and a workflow step fails the run with no retry
or fix loop. The message names the mode and what to try. Rate limits are not `FailureAuth`.
The CLI retries a refused credential for minutes (claude 2.1.300: a 401 up to ten times with
backoff), so the adapter watches the live stream and stops the call at the first
auth-category `api_retry`. The detail then ends with `stopped at the CLI's first retry`.

## 46.6 Persistence and resume

* `run.json`: `claude_auth: {requested, mode, decided_by, key_env, api_env}` at run start, and
  one in each `resumes[]` entry. Never the key, its length or a hash. `api_env` (omitted when
  empty) holds the sorted names of `providers.claude.api_env` that were set in `api`, or
  removed because they were set in `login`. It never holds a value.
* Each execution's `call.api_key_source`; the `usage` blocks of `decision.json` carry the same
  field.
* `summary.md`: the `Claude auth` row (§43.2), with `, env: N variables` in `api` when
  `api_env` is set; `outcome.usage.cost_usd` stays the CLI's
  estimate in both modes and is marked as not billed per token in `login`.
* `resume` resolves the mode again from its own flag, the current config and environment,
  records it, and never reads `claude_auth` back to decide anything. A resume that changes the
  mode prints `claude auth changed since the run started: login → api`. The supported story: a
  run stops on the subscription's usage window or a `FailureAuth`, and
  `orchestrator resume --claude-auth api <id>` finishes it on the key.

## 46.7 Codex credentials and CLI

Codex workflow calls use `codex exec` with one credential mode per process. `api` reads a
key from `providers.codex.api_key_env` (default `OPENAI_API_KEY`) and passes it to the child
as `CODEX_API_KEY`. `login` removes API key environment variables and uses the Codex CLI's
saved ChatGPT subscription login. `auto` selects `api` when the configured key is non-empty,
otherwise `login`; `--codex-auth` overrides the config. The resolved mode and key variable
name are recorded in `run.json` and each resume, never the key.

Codex receives the prompt on stdin. Read and edit calls use `--sandbox read-only` and
`--sandbox workspace-write` with `--ask-for-approval never`; `none` and `web` are refused
because `codex exec` exposes its shell tool in those modes. A direct Codex API client is
planned separately in TODO P13.

## 46.8 Not covered

* A direct Codex API client in Go (TODO P13): it needs a change to §30.
* Gateway, Bedrock, Vertex and Foundry as modes of their own: they work under `login`.
* An HTTP preflight of the key, a mode per action, and an org-layer floor for the mode.
* `bench/` runs `claude` for its baseline itself with the operator's environment as is
  (`bench/README.md`).

---

# 47. The worktree run

Normative. A workflow with `implement` runs in its own git worktree on its own branch and
leaves the operator's working tree, index and branch untouched. The design record, with the
reasoning and the owner's decisions, is `specs/worktree/SPEC.md`.

## 47.1 Modes

```yaml
execution:
  isolation: worktree   # worktree (default) | inplace
worktree:
  setup: "npm ci"       # optional; trusted operator config, like test.command
  branch: {...}         # optional; how the branch is named (§50.3)
```

* `inplace` is the behaviour of §38.3 and §42 unchanged.
* `worktree` (the default) applies to workflows that contain `implement`, decided on the
  workflow the operator approved. A workflow without `implement` runs in place: `run --stage
  review` still reviews the operator's uncommitted work (§40.4).
* `run --inplace`, or `isolation: inplace` written out, opts out; `run --worktree` overrides an
  `inplace` config. Given together the flags are a usage error. (Changed 2026-10-18: the default
  moved to `inplace`; changed back 2026-10-23, when a worktree run came to end with a push and a
  merge request, §50.)
* The choice is recorded in `run.json: options.isolation`; a resume continues in that mode.
  A run started without `--inplace` and without `isolation: inplace` records `worktree`.
* `worktree.setup` with `isolation: inplace` written out is a config error; with the key absent
  it runs in every worktree run. `isolation` accepts the two values only.
* A dirty operator tree gets one warning, not a refusal: `The run starts from HEAD <short>;
  <n> uncommitted changes in your tree are not part of it (commit them first, or run with
  --inplace).` An unborn HEAD is a config error naming `--inplace`.

## 47.2 Creation and identity

After approval and before the restore point: `git worktree add --quiet -b
<branch> <path> <base_ref>`, where `base_ref` is the commit HEAD names and `<branch>` is named
by the repository's instructions, then the config template, then `orchestrator/<run-id>` (§50.3).
The path is `<state>/orchestrator/worktrees/<repo-id>/<run-id>`, `<state>` being
`$XDG_STATE_HOME` or `~/.local/state` and `<repo-id>` the first 16 hex digits of the sha256 of
the common git directory's resolved path. It is outside the repository and not configurable.
The path comes from the run ID alone; the branch is no longer derived from it, so a resume
checks the recorded name against §50.3's character and format rules and against `git worktree
list` showing the derived path on exactly that branch. The orchestrator refuses rather than
reuse either.
Every `git worktree add`, `list`, `remove` and `prune` the orchestrator runs in a repository holds
an exclusive flock on `<state>/orchestrator/worktrees/<repo-id>.lock` and waits for it: git 2.43
that reads a worktree another git is still creating dies ("failed to read
.git/worktrees/<id>/commondir") after `add -b` has made its branch, and two story tasks of one
repository start at once (§48.6). A failed `add` that leaves the branch reports git's error with
the refusal.
`run.json: worktree` is saved at once: `path`, `branch`, `base_ref`, `base_branch` (empty when
detached), `admin_dir`, `setup_done`, `conflicts`, `removed`. From then on every step, guard,
sandbox, scan, review diff and commit uses the worktree; the store, the run directory and its
lock stay in the operator's repository. The restore point is `base_ref`, pinned as in §38.3.
Console: `Worktree: <path> (<branch> from <base branch>@<short>)`.

## 47.3 The `.git` pointer

The worktree's `.git` file must read exactly `gitdir: <admin_dir>\n` (§38.3). The file
fingerprint also covers `<admin_dir>/config.worktree` and the `admin_dir` path. The file
tools of the provider are confined to the working directory, so a step cannot reach the
operator's tree or the runs directory; live checks V-wt-1…3 (`go test -tags security
./test/security/...`) verify that.

## 47.4 Setup

`worktree.setup` is a shell string run with `sh -c` in the worktree, once, right after
creation, before the restore point, the baseline and the pre-run test - never after a step has
run. A resume of a run with executions never runs it; a resume of a run with none and
`setup_done: false` does. It runs under the test sandbox's mounts (§41.3) **with the
network**, with `execution.TestEnv()` plus `test.env_pass`; with `test.sandbox: none` it runs
unsandboxed. The git fingerprint and the pointer are compared after it: the git config and
hooks are shared with the operator's repository, so a change fails the run. A non-zero exit or
a timeout fails the run before any step (`worktree setup failed (exit <n>)`), output in
`logs/setup.{stdout,stderr}.log`. Private registry credentials are not covered.

## 47.5 The end of the run

A completed run with `implement` ends with the chain of §50.5: commit on the run's branch, the
conflict check below, push, merge request, worktree removal (for a story task with `story.push: false`: no
push and no merge request, §50.5). Every mode but `--mode auto` asks
(`Commit to <branch>? [y]es / [N]o / [e]dit message`, §42.2, then the push and the merge request,
§50.5); `--mode auto` commits without the question under the §42.6 rules (no commit when the
question would show a warning, when there is no `summary_short`, or when the last implement
matched a `policy.paths` rule with `approval: manual`: the changes stay in the worktree and its
path is printed, exit code 6). `auto_commit` has no effect here. Before the commit the pointer
and `CheckBaseline` are checked in the worktree. Output: `Committed <sha> to <branch>: <header>`,
then `Based on <base branch>@<short>. To take it: git merge <branch>`.

When `base_branch` is set, `git merge-tree --write-tree --name-only --no-messages <branch>
<commit>` reports conflicts with the operator's branch as it is now (git ≥ 2.38; older git
skips the check with a note). The files are warned about and recorded in
`worktree.conflicts`; nothing is resolved. After a successful commit the worktree is removed
with `git worktree remove` (never `--force`; a worktree git refuses to remove is kept and its
path printed). The branch stays: it is the result, and the removal follows a successful commit
whatever the push and the merge request did (§50.5). Without a commit (a failed run, a refused
commit, a no) the worktree stays. The `summary.md` has a Worktree row (§43.2).

A story task (§48.5) is the same run from a base that the story gives it (a given commit)
instead of HEAD, on a branch named by its repository's rules, `story/<slug>/<ID>` only the
fallback (§50.8); its commit message carries the `Orchestrator-Story` trailer (§42.2).

## 47.6 `orchestrator runs clean [--older-than 7d | --unreadable] [--dry-run]`

For every run older than the limit whose worktree is not removed and whose lock is free,
removes the worktree with `git worktree remove` (a worktree with changes is kept and listed),
then `git worktree prune`, and sets `worktree.removed`. It never deletes a branch, a ref or a
run directory. A run it cannot read is skipped, with a pointer to `--unreadable`.

`runs clean --unreadable [--dry-run]` (not with `--older-than`) removes, whatever their age, the
run directories whose `run.json` this version reads but cannot decode or validate - a run started
before a layout change (`specs/clarify`), a damaged file - and nothing else: a missing or
unreadable file is no reason. A run whose lock is held is skipped. A worktree at the run's
derived path is removed first, without `--force`; git refusing it keeps the worktree and the run.
No branch is deleted.

## 47.7 Not covered

* Parallel runs (§30); pushing and merge requests are §50 (GitHub is not covered there).
* Copying ignored files (`.env`, `node_modules`) from the operator's tree: setup installs what
  it needs.
* Submodules (use `inplace`), warm worktree pools, an org floor for `execution.isolation`,
  macOS.

---

# 48. The story

Normative. A task that is not one change - it spans services, or is safe only as a sequence of
steps that each leave the system working - is split into a specification and ordered tasks, and
each task is an ordinary run (§47) in its own repository. The design record, the owner's
decisions and the plan as built are `specs/story/` (`SPEC.md`, `tasks/`).

**A model proposes; Go validates and orders; the operator approves.** The decomposition is
written to files. Nothing runs until the operator runs `story run` on them, and no model ever
picks what runs next, where, on which branch or with which stages. A story task is never a story.
A spec is a list of tasks, not a workflow language (§30). Ready tasks run at once, up to
`story.max_parallel` (§48.6); each is its own run, and a run's workflow stays sequential (§30).
The design record of the parallel story is `specs/parallel-story/`.

## 48.1 Vocabulary and configuration

The **story home** is the repository where `story plan` / `story run` are invoked; it holds the
spec (`<spec_dir>/<slug>/`) and the state (`.orchestrator/stories/<slug>/`). A **service** is a
git checkout directly under one of `story.roots`, named by its directory; the home is a service
under its own directory name.

```yaml
story:
  roots: [..]                     # absolute, ~/-prefixed, or relative to the story home
  max_services: 5                 # 1..20
  spec_dir: specs                 # in the story home
  max_tasks: 12                   # 2..30
  max_cost_usd: 20                # optional; the whole story, every call and run
  max_parallel: 1                 # 1..8; ready tasks of different services run at once; --parallel N overrides
  max_acceptance_chars: 300       # 50..2000; one acceptance item of a task file story run reads
  jira_issue_types: [Epic]        # run --jira on these refuses (§39.11); default none
  push: false                     # default true; false: every task ends at its commit, the result lands on the base branch (§50.8)
jira:
  create:                         # absent: story run creates nothing in Jira
    issue_type: Story
    link: parent                  # parent | field
    # field: customfield_10014    # required with link: field
    labels: [orchestrator]
    components: {billing: Billing}  # optional, by service name
```

Every key is in the strict schema; `jira.create` without `jira`, and `link: field` without
`field`, are config errors. Each service keeps its own `orchestrator.yaml`: its test command,
checks, policy, routes and limits apply to its tasks; the home's config contributes only `story`
and `jira`. A service's story base is the branch checked out in it when the story first starts.

## 48.2 `orchestrator story plan "<request>" [--jira KEY] [--repo NAME]...`

1. The home config's fail-fast chain; with `--jira`, one GET of the issue (§39), rendered as the
   request.
2. **Services**, found by Go with no model call: the candidates are the immediate subdirectories
   of the roots with a `.git` entry and a name matching `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`,
   plus the home. A candidate is named when its name occurs in the request as a whole word
   (case-insensitively), or equals a Jira component of the ticket; `--repo` names the services
   instead. None named, more than `max_services`, a name found under two roots, or a directory
   whose `git rev-parse --show-toplevel` is not itself, stops the plan before any call.
   Printed as `Services: billing (<path>), gateway (<path>)`.
3. **Survey**: one call per service, `read` authority, working directory = that checkout, in
   place, with that repository's deny list, on its `actions.investigate` route. The answer
   (summary, relevant files, interfaces, constraints) is strictly validated and bounded.
4. **Specify**: one call with `none` authority on the home's `actions.investigate` route, from the
   request, the surveys (fenced) and the closed list of services. A task is a vertical slice
   one run can implement, test and review, leaves its repository's tests passing, updates the
   documentation its own change makes out of date (no task is set aside for the others'
   documentation), changes one repository, and lists in `depends_on` what it needs. The answer is validated strictly: slug,
   prefix `[A-Z]{2}`, task IDs `<prefix><NN>`, `repo` a found service, size S/M/L, at most
   `max_tasks` tasks, bounded texts, the graph rules of §48.4. Any violation is an
   `InvalidResponse`: nothing is written under `spec_dir` and the answer is kept in the story's
   `artifacts/`.
5. **One task is not a story**: nothing is written under `spec_dir`; the task and the command
   that runs it are printed.
6. Otherwise Go writes `<spec_dir>/<slug>/` (§48.3) and stops. A plan that reaches no spec keeps
   its directory as `.orchestrator/stories/.plan-<id>`.

Both calls are outside the workflow, like the translator: their authority is fixed in Go, they
are not in the authority table (§38.1), and their prompts, answers and output are recorded under
the story's `artifacts/` and `logs/`.

## 48.3 The spec on disk

```
<spec_dir>/<slug>/
  README.md        generated: title, status, the task table, how to run
  SPEC.md          problem, approach, contracts, out of scope, risks
  tasks/TASK-<ID>-<title-slug>.md
```

A task file has a front matter with the closed keys `id`, `repo`, `depends_on`, `jira` (a key
written by Go, or `none` by the operator); anything else is an error and the file name must carry
the ID. The body - goal, `## Acceptance` items, notes - is the task's text, as the operator left
it; the task text is bounded to 16 KiB. `## As built` and everything after it is Go's, appended
when the task is done. `README.md` is regenerated from the task files and the state whenever a
status changes. A spec written by hand runs too.

## 48.4 Order, bases, branches

* **Order**: Kahn's algorithm over `depends_on`, the smallest ID first among the ready tasks. A
  cycle, a missing dependency, or a `repo` that is not a service is a spec error naming the tasks.
* **Parents** of task T in repository R: of T's ancestors (transitively, across repositories)
  that are in R, those no other such ancestor depends on. None: T starts from R's story base, the
  commit of the branch checked out in R at the first start (`bases.<repo>.sha`), not from the
  spec branch's tip, so its base does not depend on which merges landed first. One: that
  parent's commit, as recorded in `story.json` when it was done, so T is stacked on it. Several:
  their commits are merged on the spec branch (below) and T starts from its tip. A dependency
  in another repository gives order and context, not code.
* **The spec branch** `story/<slug>/spec` is the story's own branch in each repository the story
  touches. Go creates it at the first start, at the story base commit, after the approval and
  before the Jira children and the first dispatch, and records `bases.<repo>.spec_branch`; a
  continued story whose record lacks it gets it at `bases.<repo>.sha` before anything is
  dispatched, and a recorded branch that git lacks stops the story. Go never writes the
  operator's branch (the story base branch), except the land of a service that does not push
  (§50.8). `spec` is not a task ID, so the name cannot clash
  with `story/<slug>/<ID>`; the unknown-branch check of §48.6 knows it.
* **The merge** of several parents is made by `story run` in R's checkout, just before T is
  dispatched, one merge at a time, the parents in ID order, each: already an ancestor of the spec
  tip: nothing; the spec tip an ancestor of the parent's commit: fast-forward with `git
  update-ref refs/heads/<spec> <new> <old>`; otherwise `git merge-tree --write-tree` (objects
  only: no checkout, no hooks), and when it is clean `git commit-tree <tree> -p <tip> -p
  <parent>` with the message `Merge <parent's branch> for <ID>`, the trailer
  `Orchestrator-Story: <slug>/<ID>` and the operator's git identity (without one the story stops
  with git's message), then the same `update-ref`. Before the first `merge-tree` in a repository
  in one `story run` its git fingerprint (§47.5) must equal the one the pre-run checks took:
  configured merge drivers run code. T's `base_ref` is the spec tip after the merges; its
  `base_branch` stays the story base branch. The As built and the plan name the base as
  `story/<slug>/spec (RF01 + RF02)`.
* **Conflict.** When a merge does not go through, nothing is written; T is `conflict`, not
  dispatched (no attempt, no run), and the story is `blocked`. `story.json` records
  `tasks.<ID>.conflict {parents, files}` (at most 50 repository-relative paths). Go prints the
  files and the manual merge: a worktree of the spec branch, `git merge` of the parents'
  branches, a commit, `git worktree remove`, then `orchestrator story run <dir>` again. The next
  run, before the dispatch, requires the spec tip to descend from `bases.<repo>.sha`; if every
  parent's recorded commit is then an ancestor of the tip (the operator merged), T is dispatched
  from that tip; otherwise the merge is tried again and conflicts again. Go resolves nothing.
* **Branch**: named by the task's repository's rules when its worktree is created (§50.8): the
  instructions, then `worktree.branch.template`, then the fallback `story/<slug>/<ID>`, a retry
  adding `.<n>`. The child records the name in its `run.json: worktree.branch`, and `story run`
  copies it into `story.json: tasks.<ID>.branch` when it reads the child's run (after the child
  ends, or when it settles the run of a `story run` that died); before that the field is absent.
  The branch a task's merge request targets: with one parent, the parent's recorded branch; with
  none or several, the spec branch, which the parent `story run` pushes (§50.8). Rebasing a
  stack after review is the operator's, with git.

## 48.5 The child run

`orchestrator run --mode auto --story-task <file>` (hidden; excludes `--stage`, `--jira`,
`--inplace` and positional text) runs a task. The file is the parent's, strictly validated:
story, task, attempt, `title` (the task file's heading: one line, 1-200 runes, printable; the
`{slug}` and the task line of the branch-naming call, §50.8), `branch` (the fallback name:
`^story/[a-z0-9-]+/[A-Z]{2}[0-9]{2}(\.[0-9]+)?$`; it must not exist when it is the name used),
`base_ref` (a commit of this repository), `base_branch` (the repository's story base, for
the conflict check of §47.5), `jira`, `acceptance`, `text`. The text (at most 32 KiB) is the
story's title and the task's place in it, the task's body, the spec's approach and contracts as
context, and the As built line of every done task it depends on; it becomes `Task.Description`.

* Always a worktree run (§47), whatever `execution.isolation` says, from the given base, on a
  branch named by its repository's rules with the given `branch` as the fallback (§50.8). It runs
  under `--mode auto`, so it ends with the chain of §50.5 without questions.
* **Development is always on** (reason `story task` in `decision.json`); research and
  investigation are off (reason `story task: the task file is the plan`); review is the policy's.
* **No clarify step** (§49, D12): the task file is the plan, so the run neither records a
  `clarity` trace nor writes `task.clarification`, whatever `clarify:` says and whatever the
  pre-check's one call answered about the task; nothing is asked of an operator, and no
  clarify call is made. `run --stage` is different: it runs the step (§40.2).
* The scope question is not asked (§48.9).
* The task's `acceptance` items are review criteria ahead of the investigation's own (§44.4), recorded in
  `run.json: task.acceptance` so a resume uses the same ones; `task.source` (`kind: story`, story,
  task, attempt, jira) and the branch, base and base branch are recorded there too.
* The commit message starts with `<jira>: ` when the task has a key and gains the trailer
  `Orchestrator-Story: <slug>/<ID>` (§42.2).

## 48.6 `orchestrator story run <spec_dir>/<slug>` and `story status`

`story run` both starts and continues a story. The directory decides the state, not the working
directory; the state is flocked by the one `story run` that owns it.

1. **Before anything runs**: the home config's fail-fast chain (with the Jira dependencies when
   `jira.create` is set); every task file parsed and validated; on a continued story each
   dispatched task's file (up to `## As built`) must match its recorded sha256; for every service
   the pending tasks name - a git checkout with a commit and no `.gitmodules`, the story base
   branch present, no unknown `story/<slug>/<ID>` branch (`story/<slug>/spec` is known), and
   `orchestrator validate` exiting 0 there; `story.max_cost_usd` not reached. Before all of
   it: a task recorded as `running` whose run directory is locked by a live process (a child of
   an earlier `story run` still working) stops the story run (`RF02: run <id> is still
   running`); any other `running` task is settled from its run, as after a crash.
2. **First start**: the bases are recorded and the plan (order, repository, base, branch, Jira
   key) printed; at a terminal `Run 6 tasks in 2 repositories? [Y/n]` is asked once. `--mode
   auto` and a non-terminal stdin go on. That answer and the command are the approval
   (`approved_at`). Then the spec branches are created (§48.4).
3. **Children in Jira** (§48.7), then the tasks, in Go's order, **up to `slots` at once**, where
   `slots` is `--parallel N` else `story.max_parallel` (1..8, default 1). A task is **ready**
   when every `depends_on` task is done and it is itself pending, failed or stopped with a run,
   blocked or `conflict`; done and running tasks are not. Tasks of one service (its `repo`) run one
   at a time: a ready task whose service has a task running waits, and the slot goes to the next
   ready task of another service; their tests would share the service's ports, databases and files
   outside the worktree, and their changes meet in its merge. Whenever a slot is free the next ready
   task in the story's order whose service is free is started: done → skipped; failed or stopped with a run → that run
   resumed (`orchestrator resume <id> --mode auto` in its repository); blocked → looked at again
   (step 4); `conflict` → its parents merged again (§48.4); pending → its parents merged on the
   spec branch if there are several (§48.4), the task input written to `tasks/<ID>.json`, the
   file's sha256 recorded (the task is frozen from now) and `<orchestrator> run --mode auto
   --story-task <file>` run as a child process in the task's repository. While children run no
   git call of `story run` writes the shared `.git/config` or the hooks, because each child's
   guard (§47.5) fingerprints them. The child's run is found by its `task.source`; none found
   (the child failed its own fail-fast chain) means the task `failed`.
   * **One state owner.** Only the `story run` goroutine reads and writes `story.json`, the
     README, the task files and the console; each child is a goroutine that runs the process and
     reports its exit, and the loop settles it, one at a time, as a lone child would be. A run
     directory another process holds locked is a sibling in progress: settling and the budget
     lookup skip it without a warning.
   * **One slot**: the child has the terminal and its output is printed under `── RF02 · gateway
     · 2/6 ──`, exactly as when tasks ran one at a time.
   * **More than one slot**: a child has no stdin (`/dev/null`; children run `--mode auto`) and
     its stdout and stderr go to `.orchestrator/stories/<slug>/logs/task-<ID>.<attempt>.log`
     (0600, appended when the run is resumed). The console gets `▶ RF02 · gateway · 2/6 · log
     <path>` when the task starts, and its result line (`RF02: done`) when it ends.
   * **Ctrl-C**: every running child gets SIGINT, each is waited for and settled, and the story
     is `stopped`.
4. A task is **done** when its run is `completed` and has a `commit`. `completed` without a commit
   (a failing review, a secret warning, `approval: manual`, or an implement that changed no file)
   makes the task and the story `blocked`: the operator commits in the kept worktree or drops the
   work, and runs `story run` again, which counts the task done once its branch has a commit
   beyond its base. The hints say which case it is, read from the run: for a change held back,
   why and the commit to make in the worktree; for no change, the empty commit that marks the
   task done (`git commit-tree` on the branch when the worktree is gone) or `--retry` after the
   task file says what is missing. `failed` or
   `stopped` stops the story. `story run --retry RF02` abandons a failed task's run (its branch
   stays) and dispatches the task again on `story/<slug>/RF02.<n>`, re-reading its file: the one
   way to edit a dispatched task. A task whose parents do not merge is `conflict` (§48.4).
   **When a task does not end done** (failed, stopped, blocked or `conflict`) nothing new is
   started; the children still running are not killed, they run to their end and are settled;
   then the story stops with the status of the first task in the story's order that is not done,
   and the hints of every such task are printed: for a run, its status, the step that failed and
   the reason the run recorded, then `Led to it:` - the run's last red test (its failed check or
   failing tests, and the log) or failed review (its blocking issues and unmet criteria, and the
   artifact) - and where the summary and the step's logs are. Tasks that were ready and not
   started are named on one line (`Note: not started because of the stop: …`) and wait for the
   next `story run`. A run directory of a service that this version cannot read is warned about
   at once only when its `task.source` names this story; the others are counted, one line per
   service at the end, with `runs clean --unreadable` (§47.6).
5. **After each task** Go appends `## As built (<date>)` to the task file - run, outcome, branch
   and base, commit, cost, the run's `summary_short`, conflicts with the base branch - and
   rewrites `README.md`. Nothing a model wrote goes there except `summary_short`, as data.
6. **Budget**: before every dispatch `story.max_cost_usd` is checked against the recorded total
   (the planning calls and every child's `outcome.usage`) plus what each running child's run has
   cost so far (the executions in its `run.json`, found by `task.source`), failing closed; when
   it is not under the limit nothing new is started, the running children are drained and the
   story stops, so the overshoot is at most the steps in flight. A finished run without an
   outcome stops the story when a budget is set.
7. Every task done: the story is `completed`; per task the repository, branch, the branch its
   merge request targets (§48.4), commit, Jira key and conflicts are printed. Pushing and merge requests
   are TODO W2. A `story run` that dies between a child's end and the save of its result is
   settled by the next one before anything is dispatched, from the run it finds.

`orchestrator story status <spec_dir>/<slug>` prints the README and writes nothing.

## 48.7 Child issues in Jira

Only for a story planned from a Jira issue (`jira_parent`) and only with `jira.create`; a story
without a parent creates nothing and says so. After the approval and before the first dispatch,
`story run` sends one `POST /rest/api/2/issue` per task without a key, in the story's order:
project from the parent's key, `issue_type` and `labels` from config, `summary` `<ID> · <title>`,
a wiki-markup description rendered by Go from the task's sections (every character wiki would
read as markup is escaped), the parent in `parent` or in the configured `field`, components by
service name. Requests go to `jira.base_url` only, with no redirects, credentials from the
environment, redacted from errors and scrubbed from children (§39.8).

**Idempotent.** An intent is saved in `story.json` before each POST; after a 201 whose `key` is
`<project>-<n>` the key is saved in `story.json` and written into the task file's front matter,
and the intent cleared. A request is never retried after it may have reached Jira (a transport
error after sending, a timeout, a 5xx other than 503); only a 429 or 503 with `Retry-After` is.
An intent without a key on the next start stops the story: `RF02: a Jira issue may already
exist; set jira: <KEY> or jira: none in its front matter`. A failure stops the story before any
dispatch. Issue links between children, transitions and comments are not created.

## 48.8 Persistence and security

`.orchestrator/stories/<slug>/`: `story.json` (slug, spec dir, `jira_parent`, `approved_at`,
`services`, `bases` (per repository: `branch`, `sha`, `spec_branch`), `status`, per task: status
(`conflict` among them, with `conflict {parents, files}` present only then), `file_sha256`,
attempt, `run_id`, repo, branch, `base_ref`, `commit`, `jira`, `jira_intent`, `cost_usd`,
`abandoned_cost_usd`; the planning calls' usage), `ticket.json`, `artifacts/`, `logs/` (with
`task-<ID>.<attempt>.log` of the children when several run at once), `tasks/<ID>.json`. All of it
is validated strictly on load: paths are derived, never read; run IDs and SHAs by format; a
task's `run_id` only from its repository's runs directory; a dispatched task needs
`approved_at`; `spec_branch` is exactly `story/<slug>/spec`; a merged task's `base_ref` is a
commit on the spec branch.

* Authorities stay fixed in Go: survey `read` in one repository, specify `none`, a child run as
  any run. No call reads two repositories.
* The spec is model-written data that becomes a task only after the operator's approval. It
  reaches a child as `Task.Description` through the task block, like a ticket (§39.8); it cannot
  name a stage, a model, a tool, an authority or a command, and `repo` must be a found service.
* Slug, prefix, IDs and branch names are checked by pattern before they reach git; the base by
  SHA. Child processes are the orchestrator binary itself (`os.Executable`, symlinks resolved)
  with argv only, each running its own fail-fast chain, guard and sandbox.
* The operator's checkouts and branches are never touched: every task runs in a worktree, and
  the spec branch is merged with `merge-tree` and `commit-tree`, never in a tree.

## 48.9 The scope question (shadow)

The classification call (§8.2) asks a fourth yes/no question, `scope_story`, from Jev and the
LLM provider alike. Its probability is recorded as `scope.story` in `decision.json` and decides
nothing. It is data for a later calibration against the runs that turned out too large (failed
on `max_fix_iterations`, budget, a large diff). It changes `questions_hash`, so `decisions
calibrate` keeps old and new decisions apart, and the §9.1 cuts and thresholds are to be
re-checked once enough new decisions exist.

## 48.10 Not covered

* Merge requests of anything but a task's own branch (the spec branch gets none, §50.8); Jira
  issue links (a comment and a transition are §39.12).
* Resolving a merge conflict automatically; more than 8
  slots, a limit per repository, killing running siblings after a failure, isolating the test
  resources of tasks that run at once; rebasing a stack after review; re-planning; a model
  editing the spec after approval.
* Nested stories; a story task without a code change; submodules (as for §47).
* The benchmark: story tasks are not in `bench/`.
* Calibrating and using the scope probability (a later spec, on data).

---

# 49. Clarify

Normative. A task that does not say what it wants is made clear before anything is decided, and
the two read-only stages that did the same work twice are one. The design record, the owner's
decisions (D1–D14) and the defaults (A1, A4–A6) are `specs/clarify/` (`SPEC.md`, `tasks/`);
this section is what the code does. The sections it touches say the same in their own place:
§6 (validation), §7 (fail fast), §8 (the flow), §9 (the stages), §10, §17, §20 (actions,
artifacts, prompts), §21, §43 (records), §37 (resume), §38.1 (authority), §40 (`--stage`),
§44.4 (criteria), §45.4 (calibration) and §48 (a story task).

Two names changed, and neither is reused for the old meaning:

| before | after | kind | authority |
|---|---|---|---|
| stage `analysis`, action `analyze` | the **clarify** step | before the decision; never a stage, never in a workflow | `none` |
| stage `planning`, action `plan` | stage **`investigation`**, action **`investigate`** | workflow stage | `read` |

`analysis` is not reused for the merged stage: in every `decision.json` written before this
section it means "the cause or the place to change is unknown", and `decisions calibrate` would
mix the two meanings. The stage order is `research → investigation → development → testing →
review`.

## 49.1 The clarity question

`decision.ClarityQuestion()` (`internal/decision/questions.go`, in `QuestionsHash`) is a yes/no
question with the ID `clarity`:

* instructions: "Is `request` clear enough to act on: is it stated what has to be achieved,
  where, and how to tell that it is done?"
* yes: "A competent engineer new to the task could start without asking anything: the goal, the
  part of the system it concerns and the expected result are stated or follow unambiguously."
* no: "Something essential is missing or ambiguous: the goal, the affected part, the expected
  behaviour or result, or a choice the request leaves open."

`ContextDepthDecision` carries `PClear`. `decision.Provider` has `ContextDepth` (the pre-check:
`context_depth` and `clarity`, one call, against the request alone) and `Clarity(ctx, request,
clarification string) (domain.ClarityDecision, error)` (the re-check: `clarity` alone, against
`decision.ClarityState{Request, Clarification}`, text only). Jev's pre-check request asks
`clarity` beside `context_depth`; the LLM provider renders the re-check with first line `ACTION:
decide-clarity`. The re-check records its usage like the pre-check (`usage.clarity_recheck[]`) and
its failure takes the existing provider-failure path (§8.6). `policy.ClearEnough(p, pol)` is
`p >= policy.clarify.threshold`, inclusive; the decision provider only returns the probability.

**The decision provider sees text only** (D4): the request and, after a clarification, the
request followed by the rendered clarification (§49.4). It never sees an instruction file;
Jev has a small context, and the instructions go only to the clarify call (§49.3).

## 49.2 The flow

```text
[--jira] → fetch the issue (unchanged)
request → translate (unchanged)
        → pre-check: P(none), P(project), P(clear)        one call (decision.Provider.ContextDepth)
        → Go: P(clear) >= policy.clarify.threshold ?
            yes → go on
            no  → clarify step (§49.3)                        LLM CLI, authority none, instructions
                → re-check: P(clear) of request + clarification   (decision.Provider.Clarity)
                → below: interactive → the operator adds to the task → re-check, up to clarify.max_rounds
                         auto, or rounds spent → the run stops: task needs input (§49.6)
        → policy: project if P(project) >= project_threshold (unchanged)
        → Context Manager (query: request + clarification)
        → classification: complexity + research + development (+ scope_story)
        → policy → workflow
```

* `clarity` is asked in the pre-check's call, against the pre-check's state: a clear task costs
  no extra call.
* After a clarification the text the decision provider sees is the request followed by the
  rendered clarification. Translation applies to that text by the usual rule (§8.1); a second
  translation call is made only when a clarification was added. The Context Manager builds its
  query from the same text.
* **Interactive** is the existing rule, computed before the decision: `--mode` is not `auto` and
  stdin is a terminal. `--mode plan` at a terminal is interactive (A6).
* **`run --stage`** (§40.2): `decision.Provider.Clarity` is asked on the request, alone, with no
  translation and no project context; the clarify step runs as above; the stages are the
  operator's, the decision stays `source: manual`, and the clarity trace and
  `task.clarification` are recorded beside it. A clarity question that fails is a warning and
  the run goes on without the step.
* **`run --story-task`** (§48.5): no clarity trace and no clarify step.
* **`clarify.enabled: false`**: the clarity question is asked and recorded
  (`clarity.status: disabled`); there is no clarify step and no re-check, and the run goes on
  whatever P(clear) is. The benchmark's configs write it (`bench/README.md`).
* **A pre-check that fails** goes to the manual choice as before (§8.6), and the clarify step does
  not run; a re-check that fails does the same.

## 49.3 The clarify step

**The call.** One provider call per clarify step (numbered from 1: `clarify-<N>`), routed by the
`clarify` section (§49.8), with `domain.AuthorityNone` passed explicitly like the translator's and
never through `AuthorityFor`. There are no tools: the model cannot open a file, so "do not read
the code" is enforced by the authority and not by the prompt (D1). It runs with
`execution.ScrubbedEnv()`, the prompt on stdin, and the `apiKeySource` is checked (`FailureAuth`
is fatal). Any other failure fails the run with a `ProviderError` (D13, exit 3): an unclear task
never goes on unclarified. The model's points never decide whether the run goes on; the
re-check does.

**The prompt** (`action.BuildClarifyPrompt`, first line `ACTION: clarify`): the request through the
task block; the rules and each selected excerpt, with its path and heading, as fenced blocks;
the clarification so far, if any; and the mode — `interactive`: questions for an operator;
`auto`: answer what the instructions support, assume the rest and say which is which. It asks for
the JSON of the answer below. A repository with no rules and no excerpts says so in the prompt,
and nothing can then be answered from documentation.

**The answer** is strict: unknown keys, a `basis` outside the three, a `source` on a point that
is not `docs`, and `assumption` in interactive mode are `InvalidResponse`; a prose answer is not
accepted. At most 8 points, each field at most 300 bytes, cut in Go on a rune boundary:

```json
{"points": [{"unclear": "which export format is meant", "question": "Should the export be CSV or JSON?",
             "answer": "CSV", "basis": "docs", "source": "docs/export.md#Formats"}],
 "summary_short": "..."}
```

`basis` is `docs` (the excerpts say so; `source` names the excerpt, `<path>#<heading>`),
`assumption` (auto mode only) or `none` (nothing settles it). An answer with no points is valid:
the re-check still runs and Go decides.

## 49.4 The instructions, and the clarification text

**The instructions** are chosen by Go in `internal/projectctx` (`Docs`), reusing the Context
Manager's documentation code (§8.3): files listed by `git ls-files`, matched against
`clarify.docs` (default `["**/*.md"]`) with vendored paths dropped; the deny list (§8.3) applied
to every path segment before anything is read; binaries, files over the size limit and symlinks
never read. The **rules** are the bullets of the root `AGENTS.md`, `CLAUDE.md` and
`CONTRIBUTING.md`. The **excerpts** are the Markdown sections of the matched files, ranked by the
words of the request and its translation. Together they are cut to `clarify.max_bytes`:
excerpts are dropped from the lowest rank first, then rules from the end. The Context Manager
keeps calling the same excerpt code with `docs/*.md`, so `project_context.json` does not change.

**Render.** `domain.Clarification.Render()` is the one rendering the stage prompts, the
re-check's state and `summary.md` share:

```text
Clarification (agreed before the run; the task above stays the source):
- Which export format is meant? CSV  [from docs/export.md#Formats]
- Which users get the button? admins only  [operator]
- Assumed: the button is hidden for guests  [assumption]
- Open: Which locale is the date in?
Added by the operator:
  ...
```

The stages that take the task (research, investigate, implement first and retry, review) get the
request through the task block as always and, after it, the rendered clarification as a fenced
block; an empty clarification adds nothing, so a run without one is unchanged. The stages always
get the original request first: the clarification is an addition and the task stays the source.

## 49.5 Interactive and auto

**Interactive.** The operator sees each point with its question and the model's answer:

```text
The task is not clear enough (P(clear) 0.42 < 0.80). 2 questions:

1. Which export format is meant?
   from docs/export.md#Formats: CSV
   answer [Enter keeps it, - leaves it open]:
2. Which users get the button?
   answer [- leaves it open]:
```

Enter keeps the model's answer (`by: docs`), text replaces it (`by: operator`), `-` leaves the
point open. The questions wait on the run's user context, not on `execution.timeout`, and the
workflow gets that time back (§25); EOF or Ctrl-C cancels the run (exit 5).

**Auto.** `basis: docs` and `basis: assumption` are taken as answered (`by: docs`, `by:
assumption`); `none` stays open. Nothing is asked.

## 49.6 Still unclear: task needs input

After a re-check `P(clear) >= policy.clarify.threshold` makes the clarification final
(`status: clear`). Otherwise:

* **interactive with rounds left** (`clarify.max_rounds`, default 2; it counts additions): the open
  points, or "the task is still rated unclear", are shown and the operator types an addition ended
  by an empty line (at most 4096 bytes). It joins the clarification (`kind: addition`) and the
  re-check runs again. An empty addition stops the run as below.
* **auto, or no rounds left**: the run ends `status: stopped`, `failure_reason: "task needs
  input"`, exit code 4 (`WorkflowOrLimit`). The console and `summary.md` list the open points and
  the last P(clear) against the threshold. The run has no decision, no `decision.json` and no
  workflow; `resume` refuses it (§37.3) and the operator starts a new run with the missing
  details in the task. The calls the step made are counted in the run's outcome.

## 49.7 The stages after the change

* `domain.AskedStages()` is `research, development`; `stage_analysis` is not a question any more.
* stages = `policy.stages.force[complexity]` ∪ the asked stages at or above their threshold ∪
  testing whenever development is on. Without development, testing and review are off. **Nothing
  enabled → `investigation`**, with the reason `forced: no stage cleared its threshold,
  defaulting to investigation`.
* `investigate` is the old plan with the analysis folded in. Authority `read`. With development
  its artifact is the plan (§17): `summary`, `summary_short`, an optional `root_cause`, `steps`,
  `affected_files`, `risks` and `acceptance_criteria` (at most 8 of 300 bytes), read by implement
  (the first implement starts from `affected_files`) and by review (the criteria). Without
  development it is the last read-only step and writes the `answer` (§10). It gets research's
  artifact and the clarification; there is no analysis artifact.
* Research without development still answers itself.
* A story task turns off research and investigation (§48.5).
* The example template is `force: {medium: [investigation, review], complex: [investigation,
  review]}`, `thresholds: {research: 0.81, development: 0.81}`. A simple task with development
  gets no investigation unless the template says so: implement reads the code itself, and
  `workflow.Gaps` says so in a warning only for `--stage`.

## 49.8 Configuration

```yaml
clarify:                 # required: a route, or `clarify: {enabled: false}` written out
  enabled: true          # false: no clarify step (the benchmark); the keys below are optional
  provider: claude
  model: sonnet
  effort: medium         # optional
  timeout: 120s
  docs: ["**/*.md"]      # globs over git-listed files; the deny list applies first
  max_bytes: 16384       # rules + excerpts, 1024..131072
  max_rounds: 2          # operator additions after the re-check, 0..5
policy:
  clarify:
    threshold: 0.80      # (0, 1], required; in DecisionPolicy and policy_hash
```

With `enabled: true`, `clarify` is checked in the fail-fast chain like `translation` (§7): the
executable; `Supports(AuthorityNone)`; the CLI version; the org layer's `allowed_models`; and
`provider.CostReporter` when `limits.max_cost_usd` is set. The org layer gains no clarify keys
and no floor for the threshold (A4).

**Refused with a message naming the replacement and `specs/clarify`** (D9; never read as the new
meaning): `policy.stages.thresholds.analysis` (→ `policy.clarify.threshold`) and
`.planning`; `analysis` or `planning` in `policy.stages.force` (the repository's and the org
layer's) and in `policy.explore` (→ `investigation`); `actions.analyze` and `actions.plan` (→
`actions.investigate`, which takes what `actions.plan` took, `by_complexity` included); `run
--stage analysis|analyze|planning|plan` (§40.1).

## 49.9 Records

**`run.json: task.clarification`** is written once, before the decision, and never changed:

```json
"clarification": {
  "threshold": 0.8, "checks": [0.42, 0.86], "status": "clear",
  "rounds": [
    {"kind": "clarify", "mode": "interactive", "points": [
      {"unclear": "...", "question": "...", "answer": "CSV", "by": "docs", "source": "docs/export.md#Formats"},
      {"unclear": "...", "question": "...", "answer": "admins only", "by": "operator"}]},
    {"kind": "addition", "text": "..."}
  ]
}
```

It is absent when the first check cleared the threshold, with `clarify.enabled: false`, for a
story task and after a failed pre-check. `status` is `clear` or `unclear`; `by` is `operator`,
`docs`, `assumption` or absent (open). `checks` holds P(clear) of the pre-check and then of every
re-check. It is validated strictly on load: probabilities in [0, 1], `len(checks) == 1 +
len(rounds)`, the first round of kind `clarify`, the closed sets (an interactive round has no
assumption, a source only on a docs answer) and the byte caps.

**`decision.json`**: `clarity {threshold, checks, status, reason}` (`status` is `disabled` under
`clarify.enabled: false`), `usage.clarify[]` and `usage.clarity_recheck[]`. The usage is counted in
`outcome.decision_overhead` and in `limits.max_cost_usd` (§45.2), and `policy_hash` covers
`policy.clarify.threshold`. A trace without them still loads.

**Artifacts and logs**: `artifacts/clarify-<N>.json` (the prompt as sent, the excerpts' paths and
headings, the parsed points; a record that nothing reads back), `logs/clarify-<N>.{stdout,stderr}.log`
and `logs/decide-clarity-<N>.{stdout,stderr}.log` per re-check; N from 1.

**`summary.md`** has a Clarification section (§43.2); `decisions calibrate` shows the P(clear)
distribution and the share of runs that stopped `task needs input` (§45.4) and proposes nothing.

## 49.10 Resume

* A run whose `workflow.json` has `analyze` or `plan`, or whose decision has `analysis` or
  `planning`, is refused: "this run was started before specs/clarify; it cannot be resumed by this
  version".
* `task.clarification` is read back and given to the stages; nothing is asked again and no
  provider is constructed (§37).
* A run stopped with `task needs input` is refused (§49.6).

## 49.11 What does not change, and what is open

**Unchanged.** Go decides: the clarify model's points never decide whether the run goes on, the
re-check does; the clarity probability is compared with a threshold in Go, like every other. No
authority grows: clarify has `none`, investigation `read`, implement `edit`. The decision
provider never sees an instruction file. The same decision gives the same workflow, and the same
persisted run the same resume.

**Decisions** (owner, 2026-10-25), in short: D1 analysis is about the completeness of the request,
never about the code; D2 the clarity check runs before the stage questions; D3 clarity is a
probability judged by a threshold in Go; D4 the decision provider sees text only; D5 the
instructions go only to the clarify call, as excerpts Go selects within a byte limit; D6
interactive: the operator answers, auto: the model answers from the instructions; D7 after
clarify the provider is asked again, then an addition (bounded) or `task needs input`; D8
`analyze` became the clarify step, planning became investigation; D9 old names are config errors
with a message; D10 the other thresholds are recalibrated later and `questions_hash` changed on
purpose; D11 only the mock and the mapper changed for Jev (no access to the real one); D12 a story
task skips clarify and `run --stage` runs it; D13 a failed clarify call fails the run; D14
`clarify.enabled: false` turns the step off for the benchmark and the question is still recorded.
Defaults, open: A1 `threshold: 0.80`, uncalibrated, calibrate shows the distribution only; A4 no
org-layer keys; A5 no edit of the clarification on the review screen; A6 `--mode plan` at a
terminal is interactive.

**Not covered.** A floor on `policy.clarify.threshold` in the org layer; editing a clarification
on the review screen, and `resume` taking new input; a threshold proposal for `clarity` in
`decisions calibrate`; recalibrating the other thresholds after the question change; a live
check of the new question against the real Jev.

**Risks.** The uncalibrated threshold: too high stops clear tasks in auto mode, too low lets
unclear ones through; watch the share of `task needs input` runs. A missed reference to the old
names shows up as a strict-load refusal, not silently. Runs started before this section cannot
be resumed by this version (§49.10).

---

# 50. Delivery

Normative. From a finished run to a merge request, in a terminal or in CI: the commit message,
the branch name, the reviewer's report, the push that opens the merge request, the Jira
write-back, the machine interface and the review of a change somebody else wrote. The design
record, with the owner's decisions D1-D22 and the author's defaults A1-A24, is
`specs/delivery/` (`SPEC.md`, `tasks/`). (The task list called this section §49; §49 is Clarify.)

## 50.1 Principles

1. **Go writes everything a person reads**: the commit message, the report, the merge request
   title and description, the Jira comment. A model proposes at most one closed-list word (the
   commit type) and the `summary_short` it already writes.
2. **No delivery without the commit.** The push and the merge request happen only after the run
   committed; they never relax the §42.6 rules.
3. **Only `--mode auto` delivers without asking.** Every other mode asks before the commit, the
   push and the merge request, and a no stops there. `story.push: false` ends every mode's
   chain of a story's tasks at the commit (§50.5).
4. **Writes outside the machine happen once, after an intent, never retried blindly**: the push
   and every Jira write record an intent first (the §48.7 discipline).
5. **The orchestrator holds no GitLab credentials.** The merge request is opened by the push
   itself (GitLab push options), with the operator's own git credentials; there is no GitLab API
   client and no token variable.
6. **Only the run's own branch is pushed**: never with force, never the operator's branch.
7. **The exit code and one JSON line are the machine interface**; everything else is for a
   person and goes to stderr under `--output json`.
8. **Review-only is a run**: same store, decision record, guard, report and resume; it has no
   `implement` and reviews `base...head`.

## 50.2 The commit message

The header carries the Conventional Commit type and the `Orchestrator-Decision` trailer of §42.2.
The prompt rule next to the `summary_short` rule asks for `"commit_type"`, one of `feat`, `fix`,
`refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore`: the kind of the whole change.

| type | Jira key | header |
|---|---|---|
| `fix` | none | `fix: Use + in Add` |
| `fix` | `PAY-12` | `fix: PAY-12 Use + in Add` |
| none | `PAY-12` | `PAY-12: Use + in Add` |
| none | none | `Use + in Add` |

The key follows the type so the header stays a valid Conventional Commit and the GitLab-Jira
integration still finds the key. The header is cut to 72 runes after this prefix.

## 50.3 The branch name

The branch of a worktree run is named **when the worktree is created** (§47.2: after the
approval, before the restore point and any step) and never changes. The first of these that gives
a valid name wins:

1. **the repository's instructions**: what CLAUDE.md, AGENTS.md or CONTRIBUTING.md say about
   branch names, read by a model;
2. **the config template** `worktree.branch.template`;
3. `orchestrator/<run-id>`.

```yaml
worktree:
  branch:
    template: "feature/{key}-{slug}"   # optional
    from_instructions:                 # optional; absent = instructions are not read
      provider: claude
      model: haiku
      timeout: 60s
```

**The instructions call** is made only when `from_instructions` is set, the run is a worktree run,
the workflow has `implement`, and at least one of the three files at the repository root of
`base_ref` (read with `git show <base_ref>:<file>`, not from the working tree) contains `branch`
or `ветк` (case-insensitive); otherwise nothing is called. One cold, tool-less call: authority
`none`, the prompt on stdin, the three files together at most 32 KiB (each cut at a line) fenced
with `action.fence`; the input is those files, the task's first line (at most 200 runes, through
`action.taskBlock`), the Jira key, the run ID and today's date; the answer is strictly
`{"branch": "<name>"}`, or `{"branch": ""}` when the instructions set no rule. Any failure, an
empty answer or a name that fails the checks prints `Warning: branch name from the instructions
not used: <why>` and falls through to the template; `FailureAuth` stays fatal (§46.5). Recorded:
`run.json: branch_naming {source: instructions|template|default, proposed?, rejected?, usage?,
call?}` and `logs/branch-name.{stdout,stderr}.log`; its cost counts in `outcome.usage` and in
`decision_overhead`.

**The template** is rendered by Go:

| placeholder | value |
|---|---|
| `{id}` | the run ID |
| `{key}` | the Jira key of a `run --jira` task, or a story task's child issue; empty otherwise |
| `{slug}` | the task's first line (a story task: its title): Cyrillic transliterated by a fixed table, lower case, every run of characters outside `[a-z0-9]` becomes `-`, trimmed, at most 40 bytes cut at a `-` |
| `{date}` | `YYYYMMDD`, local time |

An empty placeholder takes the separator after it (`-`, `_`, `/`) with it. An unknown placeholder
is a config error; `{type}` is not offered (the type exists only after implement).

**The checks**, for both sources: `git check-ref-format --branch`; only `[A-Za-z0-9._/-]`; at most
100 bytes; no leading `-` or `/`, no `//`, no `..`, no `.lock` suffix; not `HEAD`, not the base
branch, not `delivery.target_branch`, not under `story/`, and not an existing local branch. An
existing branch with the template's name gets `-<last 6 of the run ID>` appended once and is
checked again; a model's name that exists is rejected (to the template).

**Recording and resume**: the name is in `run.json: worktree.branch`; a review run (§50.11)
is detached and its `worktree.branch` is empty, the only case where it may be. A resume checks the
name against these character and format rules and against `git worktree list` (§47.2).

## 50.4 `report.md`

Written next to `summary.md` whenever a run reaches a terminal status, by Go from `run.json` and
`decision.json`, never read back, never a resume input; failing to write it fails nothing. It is
the merge request description (§50.6) and the note of a review in the CI example (§50.10).
Sections, in order, each left out when empty: the title (the commit header) and a status line
(status, review verdict, tests, checks); **Task** (a block quote of the first 30 lines, and the
source URL); **Why these stages** (complexity, the decider, one row per stage with its reason);
**What changed** (files, insertions, deletions against `base@sha`, at most 50 rows); **Tests and
checks** (the last test step, fix iterations); **Review** (verdict, criteria met, at most 30
issues, most severe first); **Needs attention** (path rules requiring approval, secret findings:
never a value); a footer with the run ID, branch, restore point, the merge request when there is
one, cost, active time and version.

* The file list comes from `executions[].diff.run_files` (§50.12), up to 200 entries of the
  run's net change from the same `git diff --numstat` that gives `diff.run`; the report reads the
  last implement's list.
* **Untrusted text is neutralized.** Task text, ticket text and every model-written string are
  escaped before they reach Markdown: raw HTML, link and image brackets, and the GitLab references
  that notify or link (`@user`, `#123`, `!123`, `~label`, `%milestone`) are backslash-escaped, and
  a line that starts with `/` is indented by one space. A model cannot put a link, an image, a
  mention or a quick action into a merge request.
* Bounded at 60 000 bytes: lists are cut first, then the task quote.
* A review run has "Reviewed" (`base...head`) in place of "What changed" and no "Why these
  stages".

## 50.5 The end of a worktree run

A worktree run that completes with `implement` in its workflow ends with this chain. There is no
flag; `--inplace` leaves it out, and a story task of a story with `story.push: false` ends it after
step 2 (below). Each step
runs only after the one before it succeeded:

1. **commit** on the run's branch, in the worktree (§42.2 message, §42.3 commit);
2. the conflict check (`merge-tree`, §47.5);
3. **push** of the branch to `delivery.remote`;
4. **merge request** `branch → target`, opened by that same push (§50.6);
5. the worktree removal (§47.5), after a successful commit whatever steps 3-4 did.

| mode | commit | push | merge request |
|---|---|---|---|
| `--mode auto` | no question, under the §42.6 rules | no question | no question |
| any other mode (`manual`, `accept-edits`, the interactive default) | `Commit to <branch>? [y]es / [N]o / [e]dit message` | `Push <branch> to <remote>? [y]es / [N]o` | `Open a merge request <branch> → <target>? [y]es / [N]o` |

* The questions come in that order. The default (an empty answer, a closed stdin) is no, and a no
  stops the chain: nothing after it is asked or done. Both the push and the merge request answers
  are taken before the one `git push`.
* The commit question is §42.2's: its warnings, a typed line when there is no `summary_short`.
  The §42.6 refusals apply only under `--mode auto`.
* `auto_commit` has no effect in a worktree run. `--mode plan` never completes a run, so it never
  reaches the chain.
* **No commit, no delivery.** Under `--mode auto`, when §42.6 does not commit or the commit
  fails: `mr.skipped: <why>`, `No push and no merge request: <why>`, the changes stay in the
  worktree, its path is printed, exit code 6. In any other mode a no records `mr.skipped:
  declined at <commit|push|merge request>` and exits 0: the person decided.
* A workflow without `implement` runs in place and has no chain. A resume that completes the run
  runs the same chain (the mode is the resume's own `--mode`, the isolation `options.isolation`).
  A completed run is not resumable, so a failed push is finished by hand with the printed command.
* **`story.push: false`** (`specs/delivery-push`): the push is the story's to turn off, and only
  the story's (a plain `run` always pushes; `delivery` has no `push` key). `story run` writes
  `no_push: true` into each child's task input, and the child copies it at run start into
  `options.push: false`, read from there by a resume: after the commit and the conflict check
  nothing is asked or pushed (no `ls-remote`, no Jira write-back), `mr.skipped: push disabled
  (story.push: false)`, the worktree is removed, the branch stays, and the console prints `Push
  disabled (story.push: false); the branch <branch> stays in this repository.` with `To publish
  it: git push <remote> <branch>`. Exit code 0: the config declined the push, as a person's no
  does. The commit question and the §42.6 rules are unchanged.

## 50.6 The push, which opens the merge request

From the operator's repository (the branch is shared with the worktree), after its git
fingerprint matched the one taken when the worktree was created (a pre-push hook runs) and `git
ls-remote --heads <remote> <branch>` showed nothing. A branch of that name on the remote is
never pushed to, even as a fast-forward: `mr.error: the branch <name> already exists on
<remote>`, exit 6.

```
git push -u --no-follow-tags <remote> refs/heads/<branch>:refs/heads/<branch> \
    -o merge_request.create -o merge_request.target=<target> \
    -o "merge_request.title=<title>" -o "merge_request.description=<description>"
```

Without the merge request (a no to the third question) the same command without the `-o`
options. A story child pushes without `-u` (a sibling's guard compares that shared config).

* argv, never `sh -c`; the full environment (the operator's credential helper, ssh agent); never
  `--force`; bounded by a fixed 5 minutes. `run.json: mr {state: intent, remote, source, target}`
  is saved before the push; the push is never repeated by this run.
* **target**: the run's `worktree.base_branch`; when it is empty (detached HEAD, a CI checkout),
  `delivery.target_branch`.
* **title**: the commit header, at most 255 bytes cut at a rune. **description**: `report.md`
  rendered after the commit, every line break written as the two characters `\n` (CR dropped):
  git refuses a push option with LF or NUL, GitLab turns a literal `\n` back into a line break.
  The whole description option is at most 64 000 bytes after encoding; a longer report is rendered
  again with smaller lists, then a shorter task quote, last of all cut at a line with `…(cut)`.
  `report.md` on disk stays the 60 000-byte version.
* **The result.** GitLab answers on the push's stderr (`remote: View merge request for
  <branch>:` and the URL on the next line). Go takes the first line after that marker that is
  exactly `https://<host>/<path>/-/merge_requests/<n>`, `<host>` being the remote's host. The "To
  create a merge request" link never matches. The URL is recorded and printed, never requested.

| what happened | `run.json: mr` | printed | exit |
|---|---|---|---|
| pushed, URL found | `state: created`, `iid`, `url` | `Merge request !12: <url>` | 0 |
| pushed, merge request declined | `state: pushed`, `skipped: declined at merge request` | `Pushed <branch> to <remote>.` | 0 |
| pushed, merge request asked, no URL in the answer | `state: pushed`, `error: the remote reported no merge request` | the error | 6 |
| push failed: rejected, a hook, the network, `the receiving end does not support push options` (not GitLab) | `state: intent`, `error: <git's last line>` | `To push it yourself: git push -u <remote> <branch>` | 6 |

Minimum: git 2.10 (push options), GitLab 12.2 (the `title` and `description` options); neither
is checked in advance, the push says so. GitHub is not supported: a GitHub remote fails at the
push.

## 50.7 Configuration and the checks before the worktree

```yaml
delivery:
  remote: origin          # default origin: pushed to
  target_branch: main     # optional: the target when HEAD is detached
```

The push itself is turned off for a story only, by `story.push: false` (§48.1, §50.5, §50.8).

Strict schema, every key optional, the section optional. No token, no URL. Checked once the
workflow is known and approved (with `--stage`, before any call), before the worktree is created,
for a run that will be a worktree run: `git remote get-url <remote>` succeeds; a target is known
(the current branch, else `delivery.target_branch`); `security.secret_scan.mode` is not `off`
(a push publishes the tree). A failure is a config error (exit 2) whose message names
`--inplace`. Whether the remote is GitLab is not checked. `orchestrator validate` makes the first
and third checks when the configured isolation is `worktree`. For a story task with `options.push:
false` the remote and the target are not checked (nothing reaches them), nor is the remote by the
`validate` that `story run` makes in each service (the hidden `--story-no-push`); the secret-scan check stays, since the branch is
still meant to be published by a person.

## 50.8 `story run`

* Children run as `run --mode auto --story-task <file>` (§48.5), so every child runs the §50.5
  chain without questions; `story run`'s confirmation is the operator's yes. The target is the
  branch its base came from: the dependency's recorded branch, else the story's base branch.
* The per-service `validate` that `story run` makes before the first dispatch (§48.6) includes
  §50.7's checks.
* `story.json` records each task's `mr` (`{iid, url}`) read from the child's `run.json`;
  `story status` and the end table print it. A child that ends with exit 6 (no merge request) is
  `completed` without one: the story stops before the next task that depends on it and continues
  the independent ones. `story run --retry <ID>` works as before.
* **A story with `story.push: false`** (the story home's config, for every service) needs no remote; its children end at their commits
  (exit 0, no `mr`); a task of it is stacked on its parent's local commit and never held for the
  parent's merge request (it opens none); its spec branch is merged into locally and never pushed (`spec_push`
  stays absent).
* **The land** (`specs/story-land`). At the end of a completed story (and at every later `story
  run` of it, derived from git) each service gets the result on its base branch
  (`bases.<repo>.branch`): Go starts at the branch's current tip and merges the service's leaves
  (done tasks no task of the service starts from), in the story's order, as on the spec branch:
  already in, a fast-forward, or `merge-tree` + `commit-tree` (`Merge <branch> into <base>`, the
  `Orchestrator-Story` trailer; a conflict lands nothing). The fingerprint is compared first. The
  branch then moves: checked out in the service's checkout, by `git merge --ff-only` there (git
  refuses rather than overwrite a local change); checked out in another worktree, not at all; else
  by `git update-ref` with its old value. This is the one place Go writes the operator's branch
  and checkout: the confirmation of a story whose service does not push is the yes to it. When
  the story home is that checkout and on the base branch, the README and the task files `story
  run` changed are committed there (`git commit -- <paths>`, `Record the <slug> story: README and
  As built`). The console says `Landed on <base>: <sha> (<branches>)`, `Recorded the story on
  <base>: <sha>`, then `Not pushed (story.push: false): git push <remote> <base>`. A land that
  was refused prints `Not landed on <base>: <why>` with the hints and the same line with the
  tasks' branches (the spec branch first when a task was based on a merge there), and the story
  ends with exit 6, `status: completed`; the next `story run` tries again. A story started on a
  detached HEAD lands nothing.
* **The spec branch.** A task with no parent, or with several, targets `story/<slug>/spec`; a task
  with one parent targets that parent's branch. The parent `story run` pushes the spec branch, once
  after the approval at the first start and after every merge Go makes on it, before the task
  that needs it is dispatched: `git push --no-follow-tags <remote>
  refs/heads/story/<slug>/spec:refs/heads/story/<slug>/spec` from the repository's checkout after
  its fingerprint matched, no `-u`, no push options, never `--force`. At the first push the
  branch must not exist on the remote. Recorded as `story.json: bases.<repo>.spec_push {state:
  intent|pushed, sha, error?}`; a failed push stops the story before the next dispatch with the
  command to push it by hand.
* **A task's branch name.** Named as any worktree run's (§50.3), by the child, when its worktree
  is created, with the task's repository's own config and its instruction files read from the
  task's `base_ref`: the instructions, then `worktree.branch.template`, then `story/<slug>/<ID>`
  (a retry `story/<slug>/<ID>.<n>`) as the fallback in place of `orchestrator/<run-id>`. `{key}`
  and the call's Jira key are the task's child issue; `{slug}` and the call's task line are the
  task input's `title`. A name from the rules is never under `story/`. The plan before `[Y/n]`
  prints the fallback with `or by <repo>'s rules`. A name from a template that the abandoned
  attempt of a `--retry` holds gets the `-<6>` suffix. The naming call is the child's and counts
  in its `outcome.usage`, so `story.max_cost_usd` counts it.

## 50.9 Jira write-back

A comment and a transition after a merge request: §39.12.

## 50.10 Headless and CI

* **No terminal.** `run`, `resume` and `review` with stdin not a terminal and no `--mode` (or
  `--mode manual`) are a usage error at once, before the fail-fast chain's calls: `stdin is not a
  terminal: pass --mode auto (or plan, accept-edits) to run without questions`.
* **`--output json`** is valid with `run`, `resume` and `review`. Every human-readable line goes
  to stderr (the plain reporter); stdout gets exactly one line when the process exits:

```json
{"v":1,"run_id":"...","status":"completed","exit_code":0,"failure_reason":"",
 "workflow":["investigate","implement","test","review"],"commit":"<sha>","branch":"...",
 "mr":{"iid":12,"url":"https://…/-/merge_requests/12"},"mr_skipped":"","mr_error":"",
 "review_verdict":"pass","tests_passed_final":true,"approval_required":false,
 "cost_usd":0.84,"cost_partial":false,"run_dir":".orchestrator/runs/<id>","report":"…/report.md"}
```

  A story task with `options.push: false` that committed has `"mr":null`, `"mr_skipped":"push
  disabled (story.push: false)"`, its `branch` and `commit`, and exit code 0.
  An error before a run exists is `{"v":1,"run_id":"","status":"error","exit_code":2,"error":"…"}`
  (the message as printed, already redacted). Fields that do not apply are empty or null, never
  absent; `v` changes only on an incompatible change. `failure_kind` is `auth` for a
  `FailureAuth` and a rejected decision key, else `""`: a compatible addition (`v` stays 1)
  that `jira listen` reads to stop instead of counting an attempt (§51.8, §51.10). `--output json` is not offered for
  `story run` and `story plan`.
* **Exit codes** are the table of §4.
* **Docker and CI.** The `Dockerfile` at the repository root builds the orchestrator with git,
  bubblewrap, Node.js and a pinned claude CLI (no image is published). `examples/ci/` holds a
  `.gitlab-ci.yml` with `ai-implement` (`orchestrator run --jira "$JIRA_KEY" --mode auto
  --output json`: a worktree run, so it pushes and opens the merge request) and `ai-review`
  (`orchestrator review --base "$CI_MERGE_REQUEST_DIFF_BASE_SHA" --head "$CI_COMMIT_SHA" --mode
  auto --output json`, then the job itself posts `report.md` as one note: job script, not
  orchestrator code). In CI only `--claude-auth api` (§46); bwrap needs unprivileged user
  namespaces on the runner, and the example never sets `test.sandbox: none`. Push credentials go
  through a credential helper in the job's global git config that reads `$GITLAB_TOKEN` from the
  environment, never a token in the remote URL.

## 50.11 `orchestrator review`

```bash
orchestrator review --base main                       # main...HEAD
orchestrator review --base v1.3.0 --head feature/x
orchestrator review --base main --task "the new cache must not change the API"
orchestrator review --base main --jira PAY-12         # the ticket is the task
```

* `--base` is required; `--head` defaults to `HEAD`. `--mode`, `--output json`, `--config`,
  `--claude-auth`, `--codex-auth` as for `run`; not `--stage`, `--inplace`/`--worktree` or
  `--allow-red-tests`. There is no `--mr`: CI reviews a merge request by its refs.
* Refs resolve with `git rev-parse --verify --end-of-options <ref>^{commit}`; the reviewed change
  is `merge-base(base, head)..head`, three dots like a merge request. An empty change is a usage
  error: `nothing to review: <head> adds nothing to <base>`.
* A run like any other: `decision.json` `source: manual`, stages `testing` and `review`, workflow
  `test → review`, no decision provider, translator or Context Manager. It runs in a **detached
  worktree at `head`** (the operator's tree may be dirty and is never touched; `worktree.setup`
  runs as in §47.4), removed at the end whatever the status. `ReviewDiffOptions.Base` makes the
  diff `merge_base` against the worktree; the bounds, deny list and redaction are unchanged. The
  `test` step runs the checks and `test.command` on `head`, sandboxed and guarded; a red test
  fails the run before the review.
* The task: `--task`, else the `--jira` ticket, else Go writes one from the commits (`Review the
  change from <base> to <head>. Its commits:` and at most 50 subjects, 8 KiB). Recorded in
  `run.json: review_target {base, head, merge_base, base_ref_name, head_ref_name}`.
* A verdict that fails completes the run with it and the command exits 6. `resume` works: the
  same detached worktree, the same `review_target`. Output: `report.md` (the review form),
  `summary.md`, the console verdict and issues, and with `--output json` the one line.

## 50.12 Persistence

All optional on load, strictly validated when present.

| field | rule |
|---|---|
| `options.isolation` | a run started without `--inplace` and without `isolation: inplace` records `worktree` |
| `branch_naming` | `{source, proposed?, rejected?, usage?, call?}`; `proposed` at most 255 printable bytes, `rejected` one line at most 500 bytes; absent for an inplace or review run |
| `worktree.branch` | any name that passes §50.3's rules (a story task: those, or its fallback's story pattern); empty only on a run with `review_target` |
| story task input `title` | one line, 1-200 runes, printable; required |
| story task input `no_push` | a boolean, optional; `true` when the story says `story.push: false` |
| `options.jira_write` | `{comment: bool, transition: string}`, copied from `jira.write` at run start |
| `options.push` | `false` only, copied from the story task input's `no_push: true` (`story.push: false`) at run start; absent pushes |
| `mr` | `{state: intent\|pushed\|created, remote, source, target, iid?, url?, skipped?, error?}`; `iid` and `url` only with `created`; `url` https on the remote's host, ending `/-/merge_requests/<iid>`; `skipped` and `error` one line, at most 500 bytes, redacted; `skipped` alone (no `state`) when the chain stopped before the push |
| `jira_write` | `{comment: {state, id?}, transition: {name, state}}` (§39.12) |
| `review_target` | `{base, head, merge_base}` 40 hex; `base_ref_name`, `head_ref_name` printable, at most 255 |
| `executions[].diff.run_files[]` | at most 200 `{path (clean, repository-relative), insertions, deletions, binary}`, on an implement with `diff.run` only |
| `story.json: tasks.<ID>.mr` | `{iid, url}` |
| `story.json: bases.<repo>.spec_push` | `{state: intent\|pushed, sha (40 hex), error?}`; only with the spec branch |
| `story.json: tasks.<ID>.branch` | §50.3's rules too; set from the child's `worktree.branch`, absent before the child's run is read |

## 50.13 Security

* No GitLab API client, and no request to GitLab but git's own: the push, and for `jira listen`
  the clone and fetch (§51.6). `run`, `resume`, `review` and `story` read no GitLab token and
  use the operator's git credentials like every git call. Only `jira listen` has a token
  variable (`gitlab.token_env`); it reaches nothing but git, through a credential helper (the
  hidden `orchestrator git-credential`), never a URL, argv, `.git/config`, log, state or comment,
  and never a provider CLI, the test command or a Jira call (§51.11). The child runs of the
  listener inherit the helper configuration (their push needs it) and the variable the helper
  reads, which `ScrubCredentials` removes from every provider CLI and `TestEnv` omits.
* Push: only the run's own branch, an explicit refspec, no `--force`, after the operator
  fingerprint check, and never to a branch that already exists on the remote: a model-proposed or
  template name cannot move somebody else's branch. The `-o` values are built by Go, escaped and
  bounded, with no LF or NUL. The merge request URL is read from the remote's answer, checked
  against the remote's host and pattern, and only recorded and printed.
* Only `--mode auto` publishes without a question; a story's children run under it after `story
  run`'s confirmation.
* The branch-name call has authority `none`; the instruction files are read from `base_ref` with
  `git show`, bounded and fenced; whatever it answers only passes Go's checks or is dropped. A
  secret finding never reaches a push: under `--mode auto` it blocks the commit (§42.6), in
  another mode the commit question shows it, and the scan cannot be `off` in a worktree run.
* Everything model- or ticket-written that reaches GitLab or Jira is escaped (§50.4).
* A delivering run is never `inplace`, so `.git/config` (which may carry a URL with a token) is
  outside the worktree's file tools (§47.3).

## 50.14 Not covered

* GitHub; `glab` and the GitLab API; draft, labels, assignees, reviewers, milestones, squash and
  auto-merge; waiting for the merge request pipeline.
* A flag that keeps a worktree run but skips the push (an operator says no; CI uses `--inplace`).
* Updating an existing merge request or pushing again to it; force-push after a story rebase.
* `review --mr`, merge request notes from the orchestrator, inline review comments; reviewing a
  fork's merge request.
* Jira comments on a failed run or without a merge request, outside the listener (§51); links
  between issues.
* Publishing a Docker image; macOS images; a non-GitLab CI example.
* A retry command for a push or merge request that failed at the end of a completed run.
* An org-level floor that forbids or forces delivery or `inplace`.
* Renaming a branch after implement; a branch name for an `inplace` run; naming a story's
  branches up front, in the plan or in the spec.
* Parallel agents, a separate worktree for review, resolving merge conflicts, integrating
  changes automatically, a lifecycle manager over persistent state.
* The live check on gitlab.com (`specs/delivery/SPEC.md` §15) is the operator's gate, not a test.

---

# 51. The Jira listener

Normative as a summary; the design record, with the decisions D1–D21 and the plan as built, is
`specs/jira-listener/` (`SPEC.md` is the full text, `tasks/` the tasks JL01–JL06). Its §N below
is that SPEC's section.

`orchestrator jira listen` is a supervisor like `story run` (§48.6). It polls Jira for tickets
labelled `ai-run`, and takes them one at a time: it claims a ticket by a transition, finds the
service and the GitLab project from the ticket, clones or updates the checkout, runs the ticket
as a child `orchestrator run --listener-task <file> --mode auto --output json` in the service's
directory, and writes the outcome to Jira. The child is an ordinary worktree run (clarify, the
decision, the guard, the sandbox, delivery with the merge request) and knows nothing about the
listener. **The listener owns Jira; its child never calls it.** No model is called by the
listener itself: every word it writes to Jira is Go's, and model-written text is escaped and
bounded.

## 51.1 CLI (SPEC §1)

```bash
orchestrator jira listen [--config orchestrator.yaml] [--once]
```

Runs in the current directory (the container's working directory, a volume); no git repository
is needed there. `--once` runs one poll and the ticket it dispatches, then exits (tests, cron).
It cannot be combined with `--mode`, `--stage`, `--jira`, `--output`, task text or `--inplace`.
Exit: 0 after SIGTERM/SIGINT with nothing left half-written; 2 for a configuration, credential or
`FailureAuth` stop (§51.10); 1 for an unexpected error. Logs go to stderr, one line per event
(`listen: PAY-12 claimed`, `listen: PAY-12 → Ready to Review (!12)`). At start: the fail-fast
chain of the listener's file (`ValidateDependencies` with `DependencyOptions{Jira: true}` plus the
GitLab variable, `git` and the listener's own executable; the provider CLIs are checked per
service by the child), `GET /myself`, the flock, and the recovery of the state (§51.9). The
org layer applies to the listener's file as to any other.

## 51.2 Configuration (SPEC §2)

The `listener` and `gitlab` sections next to `jira` (§5, §6, `orchestrator.example.yaml`), a
strict schema validated in `config.Validate`. `listener.query` names the projects, the label
(default `ai-run`), the statuses tickets are picked from and the reporters allowed to start a
run (`accountId` on Cloud, username on Data Center); `listener.labels` the prefixes of the
service and project labels (`service:`, `gitlab:`); `listener.transitions` the five transitions
`start`, `blocked`, `failed`, `done`, `retry`, each a name or a target status as
`jira.write.transition`. `gitlab` has `base_url`, `token_env` (env only), `username` (default
`oauth2`) and the required `allowed_groups`. A `listener` without `jira` or `gitlab` is a
configuration error. Only `jira`, `listener`, `gitlab`, `story.jira_issue_types`, the provider
sections the chain checks and `security` matter to the listener; each service's run uses that
service's own configuration (§51.6).

## 51.3 Polling (SPEC §3)

Go builds the JQL, nothing comes from the configuration as JQL: `project in (…) AND labels =
"ai-run" AND status in (…) AND reporter in (…) ORDER BY priority DESC, created ASC`, on the
search of §39.3, at most 200 issues a poll. It polls only while no child runs: after a poll
that dispatched a ticket, again at once when the ticket is finished, otherwise after
`poll_interval`. A failed poll is a log line and the next one waits twice as long (up to 1 h);
401 stops the listener. **The search is a filter, not a boundary**: Go re-checks, for each
issue, the project, the exact label, the status (case-insensitive), the reporter, that the
issue type is not in `story.jira_issue_types`, and that the state has no `next_try_at` in the
future. An Epic-type issue gets a comment ("plan it as a story") and `blocked` (D18: story
mode is later).

## 51.4 Claim (SPEC §4)

The transition `start` from the issue's current status (§39.12's lookup and POST). The workflow
allows it only from the queue's status, so a second listener, or a person who moved the ticket,
makes it fail and the issue is skipped: this is the only lock in Jira. The state records
`claiming` before it and `claimed` after. The ticket is not assigned to the bot.

## 51.5 Service and project (SPEC §5)

From the ticket, never from a model. **Service**: every label `service:<name>` and every
component whose name equals (fold case) a directory under `services_dir`; exactly one distinct
name valid for `story.ValidRepo`, otherwise a comment naming the labels found and `blocked`. The
description is not read. **Project**: at most one label `gitlab:<group>/<path>` (segments of
`[A-Za-z0-9_.-]+`, no `.` or `..`, `.git` stripped); several → `blocked`. If
`<services_dir>/<service>` exists it must be a git work tree's top level, and with a project label
`origin`'s URL must name that project on `gitlab.base_url`, else `failed`; without a label it is
used as is. If it does not exist a project label is required and the project must lie under one
of `allowed_groups` (whole segments: `backend` allows `backend/x`, not `backend-old/x`);
otherwise `blocked`.

## 51.6 Checkout (SPEC §6)

* **Clone**: `git clone --no-recurse-submodules -- <base_url>/<path>.git
  <services_dir>/.clone-<service>-<rand>` under `clone_timeout`, then `os.Rename` to the
  service's directory, so a cut clone never looks like a service; a `.clone-*` left by a crash
  is removed at start.
* **The GitLab credential**: the token never enters a URL, argv or `.git/config`. Every git
  process of the listener, and every child, has in its environment `GIT_CONFIG_COUNT`/`KEY_n`/
  `VALUE_n` setting `credential.<gitlab.base_url>.helper` to `!<listener executable>
  git-credential`, a hidden subcommand that answers git's `get` with the username and the token
  from `gitlab.token_env`, only for exactly that protocol and host. Provider CLIs never see the
  token (`ScrubCredentials`), and `test.command` has `TestEnv`'s allowlist. The child's
  environment has no Jira variables.
* **A fresh base**: before each run `git fetch origin`, the default branch from `origin/HEAD`,
  the checkout must be on it and clean, then `git merge --ff-only origin/<default>`. Otherwise
  a comment ("the service checkout is not on a clean <default>") and `failed`: the listener
  owns the services directory, nobody works in it by hand (D16).
* **The service's configuration**: `<service>/orchestrator.yaml` when it exists (its test
  command, checks, protected paths, routes, limits and instruction files apply, D7); otherwise
  `listener.default_config` as `--config` (D6); without one, a comment and `failed`.

## 51.7 The task (SPEC §7)

`jira.Fetch` and `jira.Render` as in §39.4–§39.5, plus the discussion section (§39.5), where the
comments come from `GET …/comment` (at most 100). A text longer than `jira.max_bytes` gets a
comment and `blocked`. The listener writes `.orchestrator/listener/tasks/<KEY>.<attempt>.json`
(`{v: 1, key, url, fetched_at, description, scrub_env}`) and starts `run --listener-task
<file>` (hidden, like `--story-task`), which reads it with a size cap and validates it strictly.
The child records `task.source {kind: jira, key, url, fetched_at, listener: true}`, builds no
Jira client and needs no `jira` section, runs the clarify step like any run (unlike a story
task), and has `jira.write` off (`options.jira_write` empty) whatever the service's file says
(D14). It refuses `--stage`, `--jira`, `--story-task`, task text and `--inplace`.

## 51.8 Outcomes (SPEC §8)

The child's exit code and JSON line, then `run.json` loaded with the persistence validators.
Every comment starts `[orchestrator]`, is Go's wiki markup with model text escaped (§39.12,
§50.4), is bounded to 8 KiB and ends with `Run <run-id>`.

| child | comment | transition | attempt counted |
|---|---|---|---|
| 0, `mr.state: created` | merge request link, commit header, review, tests, cost | `done` | yes |
| 4, `task needs input` | the open points of `task.clarification`, numbered; "answer in a comment and move the ticket back to <status>" | `blocked` | yes |
| 4, any other reason | the failure reason and the last test or check that failed | `failed` | yes |
| 6 | the verdict and its issues, or why there is no merge request | `failed` | yes |
| 2 with `failure_kind: auth` | none | `retry`; the listener stops | no |
| 2 otherwise | the error as printed (already redacted) | `failed` | yes |
| 1 | the error | `failed` | yes |
| 3, a clone or fetch that failed on the network | none | `retry`, `next_try_at` with backoff from `poll_interval`, doubling, at most 1 h | no |
| 5 / killed (the listener is stopping) | none | none: stays `start`, resumed on restart | no |

A ticket blocked before the run (service, project, size, Epic) counts no attempt. At most
`max_attempts` finished runs per ticket: one more pick of a spent ticket gets a comment ("N
attempts spent; remove PAY-12 from .orchestrator/listener/state.json to try again") and `failed`,
once. Before the final transition the status is read again: no longer `start` (a person moved it)
→ the comment is posted, the transition is not (D13). Writes follow §39.12: an intent in the
state before each request, the id or `done` after it, never repeated once it may have reached
Jira; a failed write is a log line, not a retry loop. `--output json` has `failure_kind` (§50.10).

## 51.9 State and restart (SPEC §9)

`.orchestrator/listener/state.json` in the working directory, owned by one goroutine, written
atomically, with `.orchestrator/listener/lock` flocked (a second listener in the same directory
refuses at once). Per key: `attempts`, `service`, `phase` (`claiming|claimed|running|finished`),
`run_id`, `run_dir` (derived from the service and the id, checked on load, never trusted as a
path), `outcome`, `writes {comment, transition}`, `next_try_at`. Strict validation on load; a
corrupt file stops the listener (exit 2). At start, for every key not `finished`: `claiming` →
the status is read, in `start` it counts as `claimed`, else the entry is dropped; `claimed`
without a run → transition `retry`, no attempt; `running` → a non-terminal `run.json` is resumed
(`orchestrator resume <id> --mode auto --output json` in the service directory, D15) and then
§51.8, a terminal one goes to §51.8 straight (only the writes not yet done); an intent without
its result is not repeated, a log line.

## 51.10 Stopping (SPEC §10)

SIGTERM/SIGINT: no new poll or claim; a running child gets SIGINT and up to `childWaitDelay`;
the state is saved; the ticket stays in `start` and is resumed on restart. The container's stop
timeout must exceed `childWaitDelay` (README). Exit 2, after saving the state and putting a
claimed-but-not-run ticket back to `retry`: Jira 401 on any call, `failure_kind: auth` from a
child, a clone that git says was refused for authentication.

## 51.11 Security (SPEC §11)

* **Who can start a run**: only reporters in `listener.query.reporters`, re-checked in Go; the
  label alone is not enough (D19: every reporter is later).
* **What gets cloned**: only from `gitlab.base_url` under `allowed_groups`; the ticket names the
  path, the configuration bounds it. A cloned repository's `orchestrator.yaml` and
  `test.command` are trusted because its group is allowlisted; the default configuration applies
  to a repository without one.
* **Credentials**: the Jira and GitLab tokens come from the environment only, never from argv,
  URLs, `.git/config`, logs, state or comments; the child gets no Jira variables; provider CLIs
  and the test command get no token (§51.6).
* **Comments**: Go's template; the only model text is the clarify points and review issues,
  escaped, bounded and secret-scanned (a finding → "(withheld: looked like a secret)").
* Ticket and comment text is task text (§38.5): no authority comes from it.

## 51.12 Not covered

* Story mode for Epics and multi-service tickets (D18); every reporter (D19); assigning the
  ticket to the bot; several tickets at once; a webhook instead of polling.
* A service worked on by hand (D16); a retry command; a UI.
* The live Jira and GitLab check is the operator's gate (no `-tags security` suite).
