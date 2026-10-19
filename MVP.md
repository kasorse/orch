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
  pre-check: does classifying it need repository context?
  collect compact project context if so
  classify: complexity + probability of every stage
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
analyze → implement → test → review
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

All three are accepted by the configuration and have adapters, but the MVP runs on
**claude only**: codex and gemini support no authority until their permission flags are
verified against the real CLI (§38.2), so `ValidateAuthorities` refuses any route to them
(§7). Adopting either one is a separate specification of its own.

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
→ research, analysis, planning
→ implementation
→ review

Decision provider (Jev or an LLM CLI)
→ bounded probabilities: does the task need project context,
  how complex is it, how likely is each stage needed

Orchestrator
→ translation and decision-round invocation
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
orchestrator resume <run-id>
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

providers:                   # optional; absent = auth: auto, api_key_env: ANTHROPIC_API_KEY (§46)
  claude:
    auth: auto               # auto | api | login; --claude-auth overrides it
    api_key_env: ANTHROPIC_API_KEY   # the variable that holds the key, never the key itself
    api_env:                 # optional: set for claude in api mode, removed in login (§46.3)
      ANTHROPIC_BASE_URL: https://ai-gateway.example.internal

policy:
  context_depth:
    project_threshold: 0.5
  complexity:
    cuts: {medium: 1.24, complex: 1.48}  # on size = P(medium) + 2·P(complex), §9
  stages:
    thresholds: {research: 0.81, analysis: 0.81, development: 0.81}  # the three asked stages
    force:                   # the template: stages on at each complexity
      medium: [analysis, review]
      complex: [analysis, planning, review]
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
    effort: medium           # optional, claude only: low|medium|high|xhigh|max (§15)
  analyze:
    provider: claude
    model: sonnet
  plan:
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
  isolation: inplace           # inplace (default) | worktree: where implement runs (§47)

# worktree:
#   setup: "npm ci"            # optional; trusted, runs once before any step, with the network (§47.4)

security:                      # optional (§38); no key widens what a step may do
  require_clean_worktree: true # a workflow with implement refuses a dirty tree
  protected_paths: []          # extra patterns implement must not change
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
* `policy`: every stage threshold present (absent is not 0) and in [0, 1], known stage
  and complexity names only, `project_threshold` and complexity thresholds in [0, 1];
* `context`: positive `max_files`/`max_bytes`, no empty `extra_deny` pattern;
* every action's provider and model (`research`, `analyze`, `plan`, `implement`,
  `review`), non-negative per-action timeouts, and an optional `effort` in
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
  `email` key is an error.

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
provider "codex" is required by actions [analyze, review],
but executable "codex" was not found in PATH
```

The translator's provider is always checked (even for an English request, so fail-fast
does not depend on the request's language), and so is an LLM decision provider's. With
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
every route (each action, the translator, an LLM decision provider), whether the routed
provider can enforce the authority that route runs with (§38). A route it cannot enforce
fails the run with an `AuthorityError` (exit 2), e.g.:

```text
configuration validation failed:

provider "codex" cannot enforce the "read" authority required by "review"

The orchestrator does not run a provider with authority it cannot enforce, rather than let it run with unknown authority (specs/permissions/SPEC.md §6).

remedy: route review to a provider that supports it: claude (actions.review.provider: claude)
```

codex and gemini currently support no authority (their flags are unverified, §38), so any
route to them fails here even when their executable is installed. `orchestrator validate`
runs the same chain.

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
`specs/JEV integration/SPEC.md` (normative) and `specs/JEV integration/TASKS.md`.

```text
request
  → language check (deterministic) → translation if needed (§8.1)
  → pre-check: P(none), P(project)          -- decision round 1
  → policy picks the context depth
  → project → Context Manager → ProjectContext (§8.3)
  → classification: complexity + 6 stages    -- decision round 2
  → policy → complexity, enabled stages (§9)
  → workflow
```

With `run --stage` none of this runs: the operator's stages become the decision and the
flow continues at the workflow builder (§40).

## 8.1 Translation

The questions are asked in English. A request whose prose letters are at least 30%
Cyrillic (identifiers, paths and backticked text are left out of the count) is
translated by one CLI call (`translation.provider/model`, `translation.timeout`). The
translation is sanity-checked: non-empty, 0.3×–3× the original length, under 10%
Cyrillic, every identifier and path of the original preserved verbatim. Any failure sends
the original instead, with `translation.failed` in the trace and a warning - it never
fails the run. The translated text is used only for the decision provider and for
relevant-file search; the stages always get the original.

## 8.2 Questions

Every provider answers the same questions (`internal/decision/questions.go`), over the
same state:

* `context_depth` (choice: `none` | `project`) - is repository context needed to classify
  the request? Asked against the request alone.
* `complexity` (choice: `simple` | `medium` | `complex`) and, when
  `decision.complexity.shadow` is on, `complexity_score` (score over the same three
  levels). The policy decides on `primary`; the other is recorded for comparison only.
  Both are built from one criteria list, so their texts cannot drift apart.
* `stage_<stage>` (yes/no) for the three asked stages, each judged independently:
  `development` (must repository files change?), `research` (is outside knowledge needed?)
  and `analysis` (is the cause or the place to change not yet known?). Planning, testing and
  review are not asked: their answers repeated the task's size (§9.1); they come from the
  level and from development (§9).
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
`credentials*`, `secrets*`, plus `context.extra_deny`) is applied to every path segment;
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
call per round (`ACTION: decide-context`, `ACTION: decide-classify`), the state embedded
as JSON data. The answer is the same probability JSON, validated the same way (with a
slightly wider sum tolerance, then normalised). CLI failures become `DecisionError`s, so
everything downstream - including §8.6 - behaves exactly as with Jev. The trace records
`source: llm`.

## 8.6 Provider failure: manual choice

If either round fails and `decision.on_failure` is `ask`, the user chooses by hand: the
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
               nothing enabled → analysis
```

The asked stages are research, analysis and development (§8.2); planning, testing and review
have no probability and come from the template. A config that still sets
`policy.complexity.medium`/`complex` or a threshold for planning, testing or review is
refused with a message naming the replacement. The design record is
`specs/decision-shape/`.

Stages run in a fixed order, and each maps to one action:

```text
research    → research
analysis    → analyze
planning    → plan
development → implement
testing     → test
review      → review
```

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
`medium: 1.24, complex: 1.48`, the thresholds `research: 0.81, analysis: 0.81,
development: 0.81` and the template `medium: [analysis, review]`, `complex: [analysis,
planning, review]`.

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

---

# 10. Supported Actions

Define a fixed set:

```text
research
analyze
plan
implement
test
review
```

No dynamically invented action types.

`research`, `analyze` and `plan` are read-only: their prompts forbid changing files.
Research gathers external knowledge (approaches, libraries, alternatives) and feeds
analyze and plan; analysis feeds plan.

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

* configured model, and the configured `effort` where the CLI can apply it (claude
  only; config validation rejects it for any other provider);
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
AnalysisArtifact
PlanArtifact
ImplementationArtifact
TestArtifact
ReviewArtifact
```

The read-only artifacts (research, analysis, plan) carry an optional `answer`: the reply
to the user when that step is the last read-only step of a workflow without
development (§10).

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
(`specs/quality/SPEC.md` §4): plan gets the analysis `affected_files` and `risks`; implement
gets those of the analysis and the plan, and the plan's steps and `acceptance_criteria`;
review gets the plan's steps, `risks` and criteria (numbered). Empty lists are left out. The
first implement after a plan with `affected_files` is told to start from those files and not
to survey the repository again, opening another file only for what they do not answer, and to
follow the code where the plan is wrong (a retry is not: it starts from the failure). A
retried implement also gets the git diff --stat of what the run has changed so far (against
`pre_run_ref`), fenced and bounded by `limits.prompt.max_diff_bytes`; if git cannot tell, the
step goes on without it and a warning is shown. The plan's optional `acceptance_criteria`
(at most 8, each at most 300 bytes) are checkable statements about the result; the parser
keeps the first 8 non-empty ones and cuts each on a UTF-8 boundary.

When the plan has criteria, review must return a verdict for each one
(`criteria: [{index, met, evidence}]`, `specs/quality/SPEC.md` §7.1): exactly one entry per
criterion, indexes `1…N` each once, `evidence` of 1 to 500 bytes. Any other answer is a
malformed review (a hard step error). Go fills `criterion` from the plan. An unmet criterion
fails the review like a `high` issue, and the retried implement is told which criteria were
unmet, with the evidence, before the issues. Without criteria the answer has none. Every
review prompt also tells the reviewer it has Read, Grep and Glob and should read the code
around the change (§7.2).

Example analysis artifact:

```json
{
  "summary": "Retry policy retries all non-2xx responses.",
  "summary_short": "Every non-2xx response is retried",
  "root_cause": "404 is not excluded.",
  "affected_files": [
    "internal/http/client.go"
  ],
  "recommendation": "Mark 404 as non-retryable.",
  "risks": []
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

* original task;
* analysis or plan where available;
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
research
analyze
plan
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

Stage prompts always get the user's original request, never the translation.

For research include:

* original task.

For analysis include:

* original task;
* research, where available.

For planning include:

* original task;
* research and analysis, where available.

A read-only step that is the last read-only step of a workflow without development
also asks for the final `answer` (§10).

For an initial implementation request include:

* original task;
* analysis or plan where available.

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

      logs/
        <NNN>-<action>.{stdout,stderr}.log
        translate.{stdout,stderr}.log
        decide-context.{stdout,stderr}.log    # LLM decision provider
        decide-classify.{stdout,stderr}.log
```

Use UUID or equivalent unique Run ID.

Persist enough data so a completed or failed run can later be inspected, and a failed,
stopped, planned or interrupted run can be resumed (§37). The run directory is locked by
the process that owns it (§37.9).

At minimum save:

* original task, and with `run --jira` where it came from (`task.source`, §39.7);
* run ID;
* the decision (`run.json`'s `decision`) and its full trace (`decision.json`): the
  request and its translation, who decided (`jev`, `llm` or `user`, with the provider
  error in the last case), the model version that answered, the pre-check distribution
  and depth, both complexity questions with their selections and agreement, each stage's
  probability, threshold, forced flag, enabled flag and reason, the workflow, and the
  latency and, where the provider reported one, the usage (`usage.translation`,
  `usage.context_depth`, `usage.classification`: tokens, cost, turns) of each decision call;
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
ContextDepthDecision, TaskClassification     (provider's raw probabilities)
DecisionPolicy                               (thresholds, forced stages)
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
    Action     string        // e.g. "implement"; "translate", "decide-context", "decide-classify" outside steps
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

4 workflow error / limits exceeded

5 user cancellation
```

Exact values may be encapsulated centrally.

A `DecisionError` maps to 3, except `auth` (2 - a key problem is a configuration
problem) and `cancelled` (5). An `AuthorityError` maps to 2: routing an action to a
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
opening the repository, the decision phase (translation, both decision rounds, context
collection), and the full engine run. A provider call that hangs while deciding is
bounded by the same timeout as one that hangs during an implement step - there is no
separate, un-timed phase. The one thing not counted is the time a user spends on a
manual choice (§8.6): it waits on a context without the deadline, and the workflow gets
that time back. `decision.timeout` and `translation.timeout` bound each of their calls
within the run's deadline. A resume has no decision phase: its `execution.timeout` is a
fresh budget that starts after approval (§37.6).

Use `context.Context` throughout the execution chain.

SIGINT/SIGTERM must cancel an active execution.

Provider processes and test processes must be terminated through context cancellation.
Each child process runs in its own process group; on cancellation the whole group is
killed, not just the direct child - a provider CLI that is (or wraps) a shell script may
fork grandchildren that inherit its stdout/stderr pipes, and killing only the direct child
would leave those running, holding the pipes open and defeating the timeout.

## 25.1 Per-Action Timeout Override

Support an optional `timeout` alongside `provider`/`model` on each of the five AI-backed
actions (`research`, `analyze`, `plan`, `implement`, `review`; `test` has no provider
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

A step the account's usage limit refused does not end the run. The claude adapter tells it
from any other refusal by the CLI's `api_error: "usage_limit_reached"` (an org policy, a
server throttle or a 429 on an API key carry none) and reports `FailureRateLimit` with the
reset time from `api_error_params.rate_limit_info.resetsAt`. The shared tail
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
  projectctx/       Context Manager (§8.3)
  policy/           thresholds → decisions (§9); imports only domain
  classification/   the decision flow and the manual-choice fallback (§8)
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
  interaction/      approval, mode select, manual-mode gate, manual choice
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
workflow: analyze → implement → test → review
authority: analyze=read, implement=edit, review=read; test runs: go test ./...
Restore point: 9c1e0f4d2b7a...

[1] ANALYZE
Provider: claude
Model: sonnet
→ running claude...
✓ completed (12.8s)

Result:
  Summary: Every non-2xx response is retried, 404 included.
  Root cause: shouldRetry only checks for err != nil.
  Affected files:
    - internal/http/retry.go
  Recommendation: Retry only on 5xx and network errors.

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
  research     off  p=0.05 < 0.70
  analysis     on   p=0.92 >= 0.60
  planning     off  p=0.40 < 0.65
  development  on   p=0.99 >= 0.60
  testing      on   p=0.95 >= 0.60
  review       on   forced: complexity=complex
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
  ✓ 000 analyze    claude/sonnet  read  completed
  ✓ 001 implement  claude/sonnet  edit  completed
  ✓ 002 test       test command         passed
  ✗ 003 review     claude/sonnet  read  FAIL (2 issues)
  ✓ 004 implement  claude/sonnet  edit  completed  (resume 1)
  ✗ 005 test       test command         tests failed (exit 1)  (resume 1)
Working tree:
   M internal/retry/retry.go
Next: implement — tests failed, retrying implement
Remaining workflow: implement → test → review
authority: analyze=read, implement=edit, review=read; test runs: go test ./...
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
  [2] analysis  * forced for complexity=complex
  [3] planning  * forced for complexity=complex
  [4] development
  [5] testing
  [6] review  * forced for complexity=complex
Stage numbers, comma-separated (* stages are included; -N excludes one): 4,5
complexity: complex (chosen by user)
decision: chosen by user (decision provider jev failed: unavailable ...)
workflow: analyze → plan → implement → test → review
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

* stage thresholds are inclusive; forced stages; nothing enabled → analysis;
* complexity rule on both questions, agreement flag;
* fixed stage order regardless of input order.

### Workflow engine

* successful forward workflow;
* test failure → implementation;
* review failure → implementation;
* review failure → implementation → test → review;
* workflows without implement (research → analysis; testing only; review only);
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
→ analyze
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

An action routed to an installed codex fails the fail-fast chain (exit 2) before any Jev
request or CLI call (§7, §38).

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
by `max_fix_iterations`; planning alone answers and warns about the missing analysis; both
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

### Decision scenarios

`specs/JEV integration/TASKS.md` T13: project context without secrets, depth none
without context, forced stages, a failing Context Manager, translation and its failure,
Jev down with a manual choice, Jev down in `--mode auto`, a rejected key, a missing key,
choice and score disagreeing, and an LLM decision provider.

---

# 30. Out of Scope

Do not implement:

```text
HTTP provider APIs (except the Jev decision API and reading one Jira issue, §2)
OpenRouter
LiteLLM
model benchmarking
automatic model selection
cost optimization
automatic provider fallback
parallel workflows (a story runs its tasks one at a time, §48)
multi-agent parallel execution
Web UI
REST API
IDE plugin
GitHub/GitLab integration; Jira beyond reading one issue for `run --jira` (§39) and the
child issues of an approved story (§48.7)
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
another provider.

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
analyze
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
execution in order, through the same `apply*` methods the live steps use: research,
analysis and plan artifacts; implement sets the last implementation and clears the
pending retry context; a failed test becomes the pending test failure; a failed review
leaves its issues pending. Failed executions change nothing, as live: a failed implement
did not consume the retry context, and its retry gets the same one. Research, analyze,
plan and implement artifacts are required and decoded strictly. Test and review results
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
| `not_resumable` | no workflow; a run predating resume support |
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
| translation, decide-context, decide-classify | `none` | work on the request text in the prompt |
| research | `web` | external knowledge; its prompt carries only the task, so no repository data can leak |
| analyze, plan, review | `read` | inspect the repository; review also gets the diff in its prompt |
| implement | `edit` | the only step that changes code; compiling and testing is the test step's job (opt-in: the two verify tools below) |
| test | - | no AI call: the orchestrator runs `test.command` itself |

No call has both repository access and a way out (network, shell). An action without an
entry in `domain.AuthorityFor` gets `none`.

**Verify tools (opt-in, `actions.implement.verify: true`, §44.3).** Under `edit` the session
gets two more tools from an MCP server named `orch` that the orchestrator itself serves,
`mcp__orch__run_tests` and `mcp__orch__run_check`. This is not a shell and does not change the
authority: the adapter's `--tools` stays `Read,Grep,Glob,Edit,Write` and only the two MCP names
join `--allowed-tools`. The command is `test.command` or a configured check, chosen in Go,
never by the model; the model picks only whether to run it, which configured check (a closed
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
* codex and gemini support no authority until their flags are verified.
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
  differ from HEAD or are git-ignored and match `.claude/**`, `.mcp.json`,
  `.github/workflows/**`, `.gitlab-ci.yml`, `.envrc`, `.vscode/**`, the config file when it
  lives in the worktree, or `security.protected_paths` (at any depth, by content hash); and
  a fingerprint of the repository's git config and effective hooks directory. A change
  fails the step (`WorkflowError`, exit 4) naming the paths and the restore point; the
  changes stay on disk. Without a guard, or when its snapshot fails, `implement` refuses
  before the provider call.
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

* Writing to Jira other than the child issues of an approved story (§39.11, §48.7): comments,
  transitions, worklogs, links. Resume and `run --jira` write nothing.
* Comments, linked issues, sub-tasks, attachments, images; JQL, boards, sprints.
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
orchestrator run --stage plan "plan the cache"
orchestrator run --stage review --jira PMC-2789
```

* `--stage X` and `--stage=X`, in any position, repeatable; one stage per flag (a comma is a
  usage error). Valid only with `run` (`--stage is only valid with run`, exit 2).
* A task is required, as for any `run`; with `--jira` the ticket is the task.
* Names are the six stages (`research`, `analysis`, `planning`, `development`, `testing`,
  `review`) and the action names as aliases (`analyze`, `plan`, `implement`, `test`),
  case-insensitive. An unknown name is a usage error listing the valid ones. Duplicates
  collapse. `domain.ParseStageFlag` parses; `ParseStage` stays strict for persisted data.

## 40.2 The decision

`domain.ManualDecision` builds `Decision{Source: manual}` from the flags: context depth
`none`, no complexity, one outcome per stage in `StageOrder` (`selected with --stage` /
`not selected`) with no probability or threshold. `policy.stages.force` is not applied.
`workflow.Build` runs on it as usual, and `workflow.Verify` accepts it on resume.

No translator, decision provider or Context Manager is constructed and no project context
is collected. The fail-fast chain (§7) is unchanged and still checks every executable
the config declares, including the decision provider's and the Jev key. `decision.json` has
`source: manual`, the request, the stages and the workflow, and empty provider, model and
latencies; `project_context.json` is not written.

## 40.3 Console and gap warnings

Instead of the decision box the console prints `Stages (manual): testing → review`, then
the authority line and the mode prompt. A stage whose usual input was not selected gets a
one-line warning on stderr, never a refusal (`workflow.Gaps`): planning without analysis;
development without a plan (naming analysis too if it is also missing). Testing and review
never warn: testing runs the command on the current tree and review reads the real diff.

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

The claude adapter passes every prompt on stdin (`--input-format text`), never in argv:
Linux limits one argument to 128 KiB, and a prompt carrying a diff or a test log can exceed
it. argv ends with `--model <model>`; a `ProviderError` shows the argv and
`prompt: N bytes on stdin`, never the text. codex and gemini keep the prompt in argv until
their own specs adopt stdin (they support no authority yet).

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

In a worktree run (§47) the commit is made on the run's own branch `orchestrator/<run-id>`
without this question, under the §42.6 rules, and the question is asked only when those rules
refuse the automatic commit and the operator is at the terminal (§47.5).

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

* Conventional Commit types, pushing, merge requests (TODO W1/W2). A branch per run exists in
  worktree mode (§47); `inplace` still commits on the current branch.
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
§42.3's and is recorded as in §42.4. `Committed <sha> to <branch>: <header>` is printed. A worktree run (§47) behaves as if
`auto_commit` were true, on its own branch.

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

Only claude reports models, API time and tool calls; codex and gemini steps leave them
empty. A call that ends before its final report (a timeout, a crash, a kill) is still
counted: its usage is summed from the assistant messages it streamed until then and marked
`partial` (§43.3).

## 43.2 summary.md

Written to `.orchestrator/runs/<run-id>/summary.md` whenever a run reaches a terminal
status (`completed`, `failed`, `stopped`), together with `outcome`; a resume that ends
rewrites it over the whole history. It is rendered by Go from `run.json` and
`decision.json` with no provider call, is never read back, and is not a resume input.
Failing to write it fails nothing. Values that do not apply or are not known are `—`.

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
| Decision time | `outcome.decision_overhead.ms`: the provider-bound decision steps (translation, pre-check, context, classification), never the operator's thinking time. |
| Cost | `outcome.usage.cost_usd`: every provider call of the run, the decision calls included. With partial calls it reads `$X or more: N calls ended without a report`, since their cost is unknown. |
| Decision cost | The part of the cost spent deciding the workflow, and how many decision calls reported usage. |
| Tokens | Summed over every call: `prompt` is every input token (`input` uncached + `cache read` + `cache write`); `output` the generated tokens. |
| Provider calls | Calls that reported usage (`outcome.usage.calls`); each extra reviewer's call (§44.4) counts. |
| Verify | Only when the implement session made verify calls (§44.3): how many (`outcome.verify_calls`) and how many of them ran and passed; a refused call never passed. |
| Tests (last run) | The last test step's result: `passed`, `failed`, `failed (check <name>)` when a check failed and the tests did not run (§44.2), or `not run`. |
| Failing tests | Only when the run started red (`--allow-red-tests`) and the last failed test step and the pre-run test have parsed names in the same format: `N failing before the run, M new` (§44.7). A hint. |
| Review verdict | The last review's authoritative verdict (`ReviewResult.Decide`: any critical or high issue, or any unmet acceptance criterion, fails). |
| Reviewers | Only when the last review had extra reviewers (§44.4): the models whose answers were merged, base model first, e.g. `sonnet, opus`. |
| Code change | `outcome.diff`: the run's net change, i.e. the working tree after the last implement against the restore point `pre_run_ref` — files (binary ones in parentheses), inserted and deleted lines. |
| Approval | Only when `outcome.approval_required`: `Commit needs manual approval (policy.paths)`. |
| Commit | The commit the operator approved at the end (§42), when there is one. |

### Steps

One row per execution, in order.

| Column | Meaning |
| --- | --- |
| # | The execution's index; it is also the `NNN` of its artifact and log files. |
| Step | The action, with `(resume N)` when the N-th resume ran it. |
| Result | `completed` or `failed`; a test's `passed` / `failed (exit N)` / `failed (check <name>, exit N)`; a review's verdict and issue count, with `N/M criteria met` when the plan had criteria. |
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
| `outcome.verify_calls` | The verify calls of the whole run (§44.3), 0 and absent when there were none. |
| `outcome.approval_required` | `true` when the last implement matched a `policy.paths` rule with `approval: manual` (an earlier implement's matches do not count); absent otherwise. A CI job can read it: the change was not committed by the orchestrator. |
| `options.path_rules` | A copy of `policy.paths` made when the run is created: `{match, require_stages, approval}` in file order. A resume applies these and never the current config's; if the config's rules differ, it prints `Warning: policy.paths changed since the run started; this run keeps the rules it started with`. Absent without rules. Each rule is validated as in the config. |
| `executions[].path_rules` | On an implement that passed the guard and the secret scan: `{match, files, require_stages, missing, approval}` for each rule that matched a file of the run's net diff (`pre_run_ref` against the tree after the step; the step's own diff without a restore point; the runs directory excluded). `files` holds at most 50 clean repository-relative paths; `missing` is always present and a subset of `require_stages`. Recorded also when the step failed because of it. On an implement step only. |
| `org_config`, `resumes[].org_config` | `{path, sha256}` of the org file the run (or that resume) ran under (§45.3): `path` absolute, `sha256` 64 hex. Absent without an org file. |

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
`source: manual` and in traces written before them, and a stage that was not asked (planning,
testing, review) has no `probability` or `threshold`.

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
* Models, API time and tool calls for codex and gemini: their CLIs do not report them in a
  form the adapters read.
* The changes a test command makes, and a diff for research/analyze/plan (read-only).
* A machine-readable summary beyond `run.json` itself; `orchestrator runs report`
  aggregates over many runs.

---

# 44. Quality loop

Normative. Design record, decisions and what was built differently: `specs/quality/`
(`SPEC.md`, `README.md`, `tasks/`). Each stage is a separate cold call, so the result is only as
good as what a stage is told and how soon the run learns that it is wrong. This section makes
the stages pass on more of what they found, adds deterministic gates, lets `implement` run the
tests during its session without a shell, checks the plan's criteria in review, ends a fix loop
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
| plan | the analysis `affected_files` and `risks` |
| implement | the analysis' and the plan's `affected_files` and `risks`; the plan's `acceptance_criteria` |
| implement (retry) | the git diff --stat of what the run changed so far, against `pre_run_ref` |
| review | the plan's `steps`, `risks` and numbered `acceptance_criteria` |

Lists are `- item` lines under a heading; an empty list is left out. The plan's JSON gains
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
| `mcp__orch__run_tests` | `filter` (optional string) | `test.command`; returns the exit code and bounded output |
| `mcp__orch__run_check` | `name` (one of the configured check names, an `enum`) | that one check; offered only when `checks` is not empty |

* **Not a shell** (D16, §38.1). The command comes from config; the model supplies only whether to
  run, which configured check, and a `filter` matching `^[A-Za-z0-9_./:-]{1,200}$`. Anything
  else (`;`, a space, `$(…)`, a quote, an empty string, 201 bytes) is refused and runs
  nothing. A valid filter is passed only as `ORCH_TEST_FILTER` of that one run, never put
  into a command line; whether `test.command` uses it is the operator's choice
  (`'go test ./... ${ORCH_TEST_FILTER:+-run "$ORCH_TEST_FILTER"}'`). The `test` step never sets it.
* **Fail fast** (`Validate`, `ValidateDependencies`): `verify: true` needs `test.command`,
  `test.sandbox` other than `none` and provider `claude`; `verify` on any other action is an
  unknown key; `extensions.mcp.orch` and any server name containing `__` are refused.
* **Wiring.** The dispatcher creates a private `orch-verify-*` directory (0700) outside the
  repository, writes `verify.json` (version 1: the repository root, `test.command`, the checks,
  `pre_run_ref`, the sandbox, the computed test environment, the guard's before-state, the
  limits) and merges the `orch` server into the step's `--mcp-config` with the two tool
  names in `--allowed-tools`; the adapter's `--tools` is unchanged. The server reads nothing
  but `verify.json` and its stdin, opens no run directory, makes no network call, and writes only
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

## 44.4 Review

* **A verdict per criterion.** When the plan has `acceptance_criteria`, the review answer has
  `criteria: [{index, met, evidence}]`: exactly one entry per criterion, indexes `1…N` each once,
  `evidence` of 1 to 500 bytes; anything else is a malformed review (a hard step error). Go
  fills `criterion` from the plan. An unmet criterion counts as a `high` issue, so
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
`test.command`, `verify` without a sandbox or on another action, a reserved or ambiguous MCP
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

* `by_complexity` is optional on every action that has a provider (research, analyze, plan,
  implement, review). Its keys are `simple`, `medium` and `complex`; each value has `model`
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
| `verify_calls[]` | implement executions | `{tool, check, filter, exit_code, success, duration, output_cut, refused}`; `tool` is `run_tests` or `run_check`; `check` only on `run_check` (and only valid names); `filter` only on `run_tests` and matching the filter rule; a refused call has a short one-line `refused` and no result, a call that ran has `success == (exit_code == 0)`; at most 50 |
| `tree_after` | implement executions | the git tree of the working tree after the step; 40 or 64 hex characters |
| `provider.model_source` | executions | `base`, `by_complexity.{simple,medium,complex}`, `by_complexity.<level>.escalate` or `escalated.{medium,complex}` (§44.8) |
| `review_result.criteria[]` | review executions | `{index, criterion, met, evidence}`; indexes `1…N` unique; evidence at most 500 bytes |
| `review_result.reviewers[]` | review executions | `[{model, result}]`, the base model first, absent for a single reviewer; a reviewer has no reviewers of its own |
| `call.extra_calls[]` | review executions | `{model, api_ms, input_tokens, …}` per extra reviewer; at most 4 |
| `outcome.verify_calls` | outcome | the verify calls of the whole run, not negative |

`decision.json` gains `models` (§44.8). Artifacts: the plan gains `acceptance_criteria`, the
review artifact `reviewers`. Logs gain `<NNN>-test.<check>.*`, `pre-run-test.<check>.*` and
`<NNN>-implement.verify-<K>.*`. `summary.md` gains the rows `Verify`, `Failing tests` and
`Reviewers` and the `failed (check …)` and `N/M criteria met` results (§43.2).

## 44.10 Not covered

* **Worktree isolation** (TODO Q4) and **the cost of the decision rounds** (TODO Q8): later,
  with their own specs.
* **Parsed names as a gate.** Letting a run pass while "only old" tests fail would make success
  depend on parsed text (§31.4).
* **A shell, parallel verify calls or any tool beyond `run_tests` and `run_check`**, and verify
  for codex or gemini (no adapter is verified, §38.2).
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
  (`research`, `analysis`, `planning`, `development`, `testing`, `review`) without duplicates.
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
  reports a cost: the actions' routes, every extra reviewer and every `by_complexity` model.
  `provider.CostReporter` (`ReportsCost() bool`) is implemented by the claude adapter only;
  any other routed provider is a config error, `limits.max_cost_usd cannot be enforced:
  codex (actions.review) reports no cost`. A silently unenforced budget is refused.
* **Check.** Before each step the engine sums `usage.cost_usd` over the persisted
  executions. At or over the budget, the run is `stopped` with `budget exceeded: $5.12 of
  $5.00`, exit code 4. A resume counts what was already spent and continues once the budget is
  raised.
* **Limits, not hidden.** A step that starts under budget can end over it by one step's cost.
  Decision calls are not counted (`decision_overhead` reports them). A call cut off before
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
`by_complexity` models, extra reviewers, the translator and the LLM decision provider. A
route with no model is an error under it. Thresholds, `auto_commit` and the decision, Jev and
Jira settings are not in the org schema on purpose.

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
  bucket with fewer runs prints `not enough data`. Research, analysis and planning get a
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
* A config written back by calibration; automatic thresholds for research, analysis or
  planning; exploring models; a `--stage` run that still records probabilities.
* Per-user or per-team policy beyond one org file, fetching the org config over the
  network, and enforcing anything against the machine's owner.

# 46. Claude credentials

Every call to the `claude` CLI (workflow steps, the translator, the LLM decision provider) runs
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
`test.env_pass` refuses it; only the claude adapter puts the value back, as
`ANTHROPIC_API_KEY`. There is no `providers.codex`/`providers.gemini` and no org-layer floor.

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
whose stream carries `system/api_retry` with `error` in `authentication_failed`,
`oauth_org_not_allowed`, `account_on_hold` or `billing_error`. It is fatal everywhere: the
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

## 46.7 Not covered

* A Messages API client in Go (TODO P12): it needs a change to §30.
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
  isolation: worktree   # inplace (default) | worktree
worktree:
  setup: "npm ci"       # optional; trusted operator config, like test.command
```

* `inplace` is the behaviour of §38.3 and §42 unchanged.
* `worktree` applies to workflows that contain `implement`, decided on the workflow the
  operator approved. A workflow without `implement` runs in place: `run --stage review` still
  reviews the operator's uncommitted work (§40.4).
* `run --inplace` and `run --worktree` make one run `inplace` or `worktree` whatever the
  config says; given together they are a usage error. (Changed 2026-10-18: the default was
  `worktree` with no flag the other way; the owner moved it to `inplace` so that a chain of
  runs lands on the current branch without a merge after each.)
* The choice is recorded in `run.json: options.isolation`; a resume continues in that mode.
* `worktree.setup` with `isolation: inplace` written out is a config error; with the key absent
  it runs only in a `run --worktree` run. `isolation` accepts the two values only.
* A dirty operator tree gets one warning, not a refusal: `The run starts from HEAD <short>;
  <n> uncommitted changes in your tree are not part of it (commit them first, or run with
  --inplace).` An unborn HEAD is a config error naming `isolation: inplace`.

## 47.2 Creation and identity

After approval and before the restore point: `git worktree add --quiet -b
orchestrator/<run-id> <path> <base_ref>`, where `base_ref` is the commit HEAD names. The path
is `<state>/orchestrator/worktrees/<repo-id>/<run-id>`, `<state>` being `$XDG_STATE_HOME` or
`~/.local/state` and `<repo-id>` the first 16 hex digits of the sha256 of the common git
directory's resolved path. It is outside the repository and not configurable. The path and
the branch come from the run ID alone; the orchestrator refuses rather than reuse either.
`run.json: worktree` is saved at once: `path`, `branch`, `base_ref`, `base_branch` (empty when
detached), `admin_dir`, `setup_done`, `conflicts`, `removed`. From then on every step, guard,
sandbox, scan, review diff and commit uses the worktree; the store, the run directory and its
lock stay in the operator's repository. The restore point is `base_ref`, pinned as in §38.3.
Console: `Worktree: <path> (orchestrator/<id> from <branch>@<short>)`.

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

A completed run commits its changes on `orchestrator/<run-id>` without the §42 question, under
the §42.6 rules: no commit when the question would show a warning, when there is no
`summary_short`, or when the last implement matched a `policy.paths` rule with `approval:
manual`. An operator at the terminal then gets `Commit to orchestrator/<run-id>? [y]es / [N]o
/ [e]dit message`; under `--mode auto` the changes stay in the worktree and its path is
printed. Before the commit the pointer and `CheckBaseline` are checked in the worktree. Output:
`Committed <sha> to orchestrator/<run-id>: <header>`, then `Based on <branch>@<short>. To take
it: git merge orchestrator/<run-id>`.

When `base_branch` is set, `git merge-tree --write-tree --name-only --no-messages <branch>
<commit>` reports conflicts with the operator's branch as it is now (git ≥ 2.38; older git
skips the check with a note). The files are warned about and recorded in
`worktree.conflicts`; nothing is resolved. After a successful commit the worktree is removed
with `git worktree remove` (never `--force`; a worktree git refuses to remove is kept and its
path printed). The branch stays: it is the result. Without a commit the worktree stays. The
`summary.md` has a Worktree row (§43.2).

A story task (§48.5) is the same run on a branch and from a base that the story gives it
(`story/<slug>/<ID>` from a given commit) instead of `orchestrator/<run-id>` from HEAD; its
commit message carries the `Orchestrator-Story` trailer (§42.2).

## 47.6 `orchestrator runs clean [--older-than 7d] [--dry-run]`

For every run older than the limit whose worktree is not removed and whose lock is free,
removes the worktree with `git worktree remove` (a worktree with changes is kept and listed),
then `git worktree prune`, and sets `worktree.removed`. It never deletes a branch, a ref or a
run directory.

## 47.7 Not covered

* Parallel runs (§30), pushing and merge requests (TODO W2).
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
A spec is a list of tasks, not a workflow language (§30). Tasks run one at a time (§30).

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
  jira_issue_types: [Epic]        # run --jira on these refuses (§39.11); default none
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
   place, with that repository's deny list, on its `actions.analyze` route. The answer (summary,
   relevant files, interfaces, constraints) is strictly validated and bounded.
4. **Specify**: one call with `none` authority on the home's `actions.plan` route, from the
   request, the surveys (fenced) and the closed list of services. A task is a vertical slice
   one run can implement, test and review, leaves its repository's tests passing, changes one
   repository, and lists in `depends_on` what it needs. The answer is validated strictly: slug,
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
* **Base** of task T in repository R: of T's ancestors (transitively, across repositories) that
  are in R, keep those no other such ancestor depends on. None: R's story base, the commit of the
  branch checked out in R at the first start. One: that ancestor's commit, as recorded in
  `story.json` when it was done, so T is stacked on it. More than one: a spec error (make one
  depend on the other). A dependency in another repository gives order and context, not code.
* **Branch**: `story/<slug>/<ID>`, a retry adds `.<n>`. A merge request targets the base's
  branch. Rebasing a stack after review is the operator's, with git.

## 48.5 The child run

`orchestrator run --mode auto --story-task <file>` (hidden; excludes `--stage`, `--jira`,
`--inplace` and positional text) runs a task. The file is the parent's, strictly validated:
story, task, attempt, `branch` (`^story/[a-z0-9-]+/[A-Z]{2}[0-9]{2}(\.[0-9]+)?$`, must not
exist), `base_ref` (a commit of this repository), `base_branch` (the repository's story base, for
the conflict check of §47.5), `jira`, `acceptance`, `text`. The text (at most 32 KiB) is the
story's title and the task's place in it, the task's body, the spec's approach and contracts as
context, and the As built line of every done task it depends on; it becomes `Task.Description`.

* Always a worktree run (§47), whatever `execution.isolation` says, on the given branch and base.
* **Development is always on** (reason `story task` in `decision.json`); everything else is the
  policy's.
* The scope question is not asked (§48.9).
* The task's `acceptance` items are review criteria ahead of the plan's own (§44.4), recorded in
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
   branch present, no unknown `story/<slug>/<ID>` branch, and `orchestrator validate` exiting 0
   there; `story.max_cost_usd` not reached.
2. **First start**: the bases are recorded and the plan (order, repository, base, branch, Jira
   key) printed; at a terminal `Run 6 tasks in 2 repositories? [Y/n]` is asked once. `--mode
   auto` and a non-terminal stdin go on. That answer and the command are the approval
   (`approved_at`).
3. **Children in Jira** (§48.7), then each task in Go's order: done → skipped; failed → its run
   resumed (`orchestrator resume <id> --mode auto` in its repository); pending → the task input
   is written to `tasks/<ID>.json`, the file's sha256 recorded (the task is frozen from now) and
   `<orchestrator> run --mode auto --story-task <file>` run as a child process in the task's
   repository, its output under `── RF02 · gateway · 2/6 ──`. The child's run is found by its
   `task.source`; none found (the child failed its own fail-fast chain) means the task `failed`.
4. A task is **done** when its run is `completed` and has a `commit`. `completed` without a commit
   (a failing review, a secret warning, `approval: manual`) makes the task and the story
   `blocked`: the operator commits in the kept worktree or drops the work, and runs `story run`
   again, which counts the task done once its branch has a commit beyond its base. `failed` or
   `stopped` stops the story. `story run --retry RF02` abandons a failed task's run (its branch
   stays) and dispatches the task again on `story/<slug>/RF02.<n>`, re-reading its file: the one
   way to edit a dispatched task.
5. **After each task** Go appends `## As built (<date>)` to the task file - run, outcome, branch
   and base, commit, cost, the run's `summary_short`, conflicts with the base branch - and
   rewrites `README.md`. Nothing a model wrote goes there except `summary_short`, as data.
6. **Budget**: before every dispatch `story.max_cost_usd` is checked against the recorded total
   (the planning calls and every child's `outcome.usage`), failing closed; a finished run without
   an outcome stops the story when a budget is set.
7. Every task done: the story is `completed`; per task the repository, branch, the branch its
   merge request targets, commit, Jira key and conflicts are printed. Pushing and merge requests
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
`services`, `bases`, `status`, per task: status, `file_sha256`, attempt, `run_id`, repo, branch,
`base_ref`, `commit`, `jira`, `jira_intent`, `cost_usd`, `abandoned_cost_usd`; the planning
calls' usage), `ticket.json`, `artifacts/`, `logs/`, `tasks/<ID>.json`. All of it is validated
strictly on load: paths are derived, never read; run IDs and SHAs by format; a task's `run_id`
only from its repository's runs directory; a dispatched task needs `approved_at`.

* Authorities stay fixed in Go: survey `read` in one repository, specify `none`, a child run as
  any run. No call reads two repositories.
* The spec is model-written data that becomes a task only after the operator's approval. It
  reaches a child as `Task.Description` through the task block, like a ticket (§39.8); it cannot
  name a stage, a model, a tool, an authority or a command, and `repo` must be a found service.
* Slug, prefix, IDs and branch names are checked by pattern before they reach git; the base by
  SHA. Child processes are the orchestrator binary itself (`os.Executable`, symlinks resolved)
  with argv only, each running its own fail-fast chain, guard and sandbox.
* The operator's checkouts are never touched: every task runs in a worktree.

## 48.9 The scope question (shadow)

The classification call (§8.2) asks a fourth yes/no question, `scope_story`, from Jev and the
LLM provider alike. Its probability is recorded as `scope.story` in `decision.json` and decides
nothing. It is data for a later calibration against the runs that turned out too large (failed
on `max_fix_iterations`, budget, a large diff). It changes `questions_hash`, so `decisions
calibrate` keeps old and new decisions apart, and the §9.1 cuts and thresholds are to be
re-checked once enough new decisions exist.

## 48.10 Not covered

* Pushing and merge requests (TODO W2); Jira transitions, comments and issue links (W3).
* Parallel tasks (§30); rebasing a stack after review; re-planning; a model editing the spec
  after approval.
* Nested stories; a story task without a code change; submodules (as for §47).
* The benchmark: story tasks are not in `bench/`.
* Calibrating and using the scope probability (a later spec, on data).
