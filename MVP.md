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
orchestrator decisions report
```

`resume` continues a persisted run where it stopped, without deciding again (§37).

`decisions report` reads the `decision.json` of every run under `.orchestrator/runs/` and
compares the two complexity questions (§8.2): how often `choice` and `score` select the
same level, the 3×3 matrix of their selections, each one's mean confidence, and the runs
where they disagree. It skips manual (`--stage`) runs, runs chosen by hand after a provider
failure, mock answers and runs without the shadow question, and lists unreadable traces.
It needs no configuration and calls nothing. It is the input for choosing one question
(`specs/JEV integration/TASKS.md` T21).

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

policy:
  context_depth:
    project_threshold: 0.5
  complexity:
    complex: 0.40            # P(complex) >= 0.40             -> complex
    medium: 0.50             # P(medium) + P(complex) >= 0.50 -> medium, else simple
  stages:
    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}
    force:
      complex: [analysis, planning, review]

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
  prompt:                    # optional bounds on repository data in prompts (§41.2)
    max_test_output_bytes: 32768
    max_diff_bytes: 262144
    diff_summarize: []

execution:
  timeout: 15m

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
* `stage_<stage>` (yes/no) for each of the six stages (§9), each judged independently.

Answers are strictly validated: the expected questions only, the right answer type,
exactly our options, values in [0, 1], distributions summing to 1. Anything else is an
`InvalidResponse` decision error. A provider's own pick (Jev's `choice` field) is ignored.

## 8.3 Context Manager

Runs only when the policy picks depth `project`. It builds a compact `ProjectContext` -
never whole files:

* metadata from the manifest (`go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`,
  `pom.xml`): language, project type, module, direct dependencies;
* test layout (unit/integration);
* rules: bullet lines of `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`;
* excerpts of the `README.md` / `docs/*.md` sections that mention the request's terms;
* relevant files - path-word matches plus verbatim identifiers in sources - with a
  one-line summary each, followed by their tests;
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

complexity     complex if P(complex) >= policy.complexity.complex
               else medium if P(medium) + P(complex) >= policy.complexity.medium
               else simple
               (applied to both questions; primary decides, agree = primary == shadow)

stages         enabled if P(stage) >= policy.stages.thresholds[stage] (inclusive)
               + policy.stages.force[complexity]
               nothing enabled → analysis
```

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
        <NNN>-<action>.json

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
  latency of each decision step;
* workflow;
* executed steps;
* provider/model and authority for each AI step (`provider.authority`, §38);
* the restore point (`pre_run_ref`) recorded before the first step, and with it
  `head_ref` and `resume_baseline` (§38.3);
* each resume (`resumes`) and, per execution, the `attempt` that ran it (§37.10);
* step result;
* test result;
* review result;
* final run status;
* failure reason if any;
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
  report/           decisions report over the persisted traces (§4); imports only domain
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
```

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
parallel workflows
multi-agent parallel execution
Web UI
REST API
IDE plugin
GitHub/GitLab integration; Jira beyond reading one issue for `run --jira` (§39)
remote repository execution
RAG
persistent conversational memory
database
custom dynamically defined action types
arbitrary user-defined workflow language
a mode that bypasses provider permission checks
per-action configurable authority or a permission policy language
per-tool-call approval during a step
OS-level sandboxing of provider CLIs (the test step's optional bwrap sandbox, §38.3, is
the one exception)
```

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
  (completed tests/reviews that failed, in a workflow with implement) from the history.
  The persisted counters are not trusted.
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
```

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
| implement | `edit` | the only step that changes code; compiling and testing is the test step's job |
| test | - | no AI call: the orchestrator runs `test.command` itself |

No call has both repository access and a way out (network, shell). An action without an
entry in `domain.AuthorityFor` gets `none`.

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

* **Clean start.** A workflow containing `implement` refuses a dirty worktree (exit 2,
  before any approval prompt) unless `security.require_clean_worktree: false`.
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
* **The test step.** The same guard runs around the test command, which executes code the
  model wrote; the guard compares the git fingerprint before running `git status`, which
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
GET {base_url}/rest/api/2/issue/{KEY}?fields=summary,description,issuetype,parent,<fields[].id...>
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

* Writing to Jira: comments, transitions, worklogs, links.
* Comments, linked issues, sub-tasks, attachments, images; JQL, boards, sprints.
* The Data Center "Epic Link" field: on Data Center only a sub-task's parent is shown.
* Converting wiki markup to Markdown; ADF (REST v3); OAuth or any interactive login.
* `--jira` on `resume` or `validate`.
* Deferred (`specs/jira/SPEC.md` §19): an outcome comment or transition at the end of a
  run, comments and linked issues as sections. (Fencing the task block in prompts is done:
  §20.)

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
step, inside `execution.timeout`. It is not an execution.

* Exit 0: `run.json: pre_run_test` (the whole `TestResult`), console `pre-run test: passed`.
* Non-zero: the run fails before any AI step (exit 4), naming the log
  (`logs/pre-run-test.{stdout,stderr}.log`) and the ways out: fix the tests, start the run
  with `--allow-red-tests`, or set `test.baseline: off`. A resume re-runs it.
* `run --allow-red-tests` (valid with `run` only, recorded as `allow_red_tests`): the task is
  to make failing tests pass. A red pre-run test is recorded and the run goes on; the first
  implement prompt gets its bounded output ("already FAILS before any change, and the
  operator expects that") until the first test step has run, also after a resume. The run
  still ends green or not by the test step's exit code.

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
explicit yes.

## 42.1 When it is offered

After the engine has finished and the final answer (if any) is printed, `run` and `resume`
(the shared tail, `executeWorkflow`) offer a commit when all of these hold:

* the operator is at the terminal: `--mode auto` was not given and stdin is a terminal.
  `--mode auto` never asks and never commits;
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
* The message is written by Go, with no provider call: the header is the last
  implementation's `summary_short` (restored from its artifact on resume), cut to its first
  line and 72 runes (`...` marks a cut), prefixed with `KEY: ` for a
  `run --jira` task. What the cut left out goes into the body; the body ends with the
  `Orchestrator-Run: <run-id>` trailer.
* Without a `summary_short` (the model left it out, or no implementation artifact is
  left) no message is proposed: the orchestrator says
  `The implementation proposed no commit message.` and asks for one line; an empty answer
  commits nothing, a written one is shown with the trailer and still needs the `y`.
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

* A separate branch per run, Conventional Commit types, pushing, merge requests (TODO W1/W2).
* Committing a part of the changes: the operator commits by hand when they want that.

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
| Decision | Who decided (`jev`, `llm`, `user`; `manual (run --stage)`), the decision provider and model that answered, the complexity, and whether the operator edited the decision on the review screen. |
| Steps | Executions recorded (every attempt, retries and resumes included) and the fix iterations. |
| Wall time | `outcome.duration_ms`: from the first step's start to the last step's finish, pauses between a stop and its resume included. The decision and the approval are not in it. |
| Active time | `outcome.active_ms`: the sum of the steps' own durations, without those pauses. |
| Decision time | `outcome.decision_overhead.ms`: the provider-bound decision steps (translation, pre-check, context, classification), never the operator's thinking time. |
| Cost | `outcome.usage.cost_usd`: every provider call of the run, the decision calls included. With partial calls it reads `$X or more: N calls ended without a report`, since their cost is unknown. |
| Decision cost | The part of the cost spent deciding the workflow, and how many decision calls reported usage. |
| Tokens | Summed over every call: `prompt` is every input token (`input` uncached + `cache read` + `cache write`); `output` the generated tokens. |
| Provider calls | Calls that reported usage (`outcome.usage.calls`). |
| Tests (last run) | The last test step's result: `passed`, `failed`, or `not run`. |
| Review verdict | The last review's authoritative verdict (`ReviewResult.Decide`: any critical or high issue fails). |
| Code change | `outcome.diff`: the run's net change, i.e. the working tree after the last implement against the restore point `pre_run_ref` — files (binary ones in parentheses), inserted and deleted lines. |
| Commit | The commit the operator approved at the end (§42), when there is one. |

### Steps

One row per execution, in order.

| Column | Meaning |
| --- | --- |
| # | The execution's index; it is also the `NNN` of its artifact and log files. |
| Step | The action, with `(resume N)` when the N-th resume ran it. |
| Result | `completed` or `failed`; a test's `passed` / `failed (exit N)`; a review's verdict and issue count. |
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

### Notes

One line per partial call (what its numbers mean) and per failed step (its telling error
line). The full error is in `run.json` and the step's logs.

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
| `executions[].diff.step` | `{files, insertions, deletions, binary}` of the step's own change. Binary files count in `files` and `binary`, never in the line counts. |
| `executions[].diff.run` | The same, for the working tree after the step against `pre_run_ref`; absent without a restore point. |
| `executions[].diff.not_reported`, `.reported_unchanged` | As in §43.2's Code changes. |
| `outcome.duration_ms` | Wall time of the steps, as above. |
| `outcome.active_ms` | Sum of the steps' durations. |
| `outcome.usage.partial_calls` | How many of `outcome.usage.calls` were partial; when not 0, `cost_usd` is a lower bound. |
| `outcome.diff` | The run's net change: the last implement's `diff.run`. |

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
