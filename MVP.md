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

Do not integrate directly with provider HTTP APIs. The one exception is the decision
provider: the Jev decision API (`POST /v1/systemone`) is called over HTTP with `net/http`
(§8). It only returns probabilities; it never writes code or runs tools.

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
orchestrator validate
orchestrator version
```

Support configuration file selection, for example:

```bash
orchestrator --config orchestrator.yaml run "..."
```

A sensible default config path may be used when `--config` is omitted.

Support an execution-mode flag on `run`:

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

test:
  command: "go test ./..."

limits:
  max_steps: 12
  max_fix_iterations: 2

execution:
  timeout: 15m

# test.sandbox: bwrap runs the test command under bubblewrap (§38.3), with
# test.ro_binds / test.rw_binds re-exposing caches
security:                      # optional (§38); no key widens what a step may do
  require_clean_worktree: true # a workflow with implement refuses a dirty tree
  protected_paths: []          # extra patterns implement must not change
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
  `review`), non-negative per-action timeouts;
* supported provider names;
* limits;
* execution timeout;
* test command.

All problems are aggregated into one error.

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
The model version Jev reports is recorded in the trace.

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
produces the same workflow.

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
    WorkingDir string
}

type Result struct {
    Output   string
    ExitCode int
    Duration time.Duration
}

type Client interface {
    Execute(ctx context.Context, req Request) (Result, error)
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
type ProcessExecutor interface {
    Run(
        ctx context.Context,
        executable string,
        args []string,
        stdin string,
        workingDir string,
    ) (ProcessResult, error)
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

* configured model;
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
files included, built on a throwaway index. It is printed as `Restore point: <sha>`.

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

Example analysis artifact:

```json
{
  "summary": "Retry policy retries all non-2xx responses.",
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

---

# 19. Review

Review is an AI-backed action.

Review input should contain:

* original task;
* analysis or plan where available;
* implementation summary;
* latest test result;
* current Git diff.

Require structured output:

```json
{
  "status": "fail",
  "summary": "404 is still retried in one path.",
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
embedded as delimited data that cannot close its own block.

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

      artifacts/
        <NNN>-<action>.json

      logs/
        <NNN>-<action>.{stdout,stderr}.log
        translate.{stdout,stderr}.log
        decide-context.{stdout,stderr}.log    # LLM decision provider
        decide-classify.{stdout,stderr}.log
```

Use UUID or equivalent unique Run ID.

Persist enough data so a completed or failed run can later be inspected.

At minimum save:

* original task;
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
* the restore point (`pre_run_ref`) recorded before the first step;
* step result;
* test result;
* review result;
* final run status;
* failure reason if any;
* execution mode the run actually ran under (`mode`).

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
```

Preserve underlying errors.

`AuthorityError{Provider, Route, Authority, Reason, Remedy}` means a provider cannot
enforce the authority a call requires (§38); it is raised by `ValidateAuthorities` before
any AI call and again by an adapter's `Execute` as a backstop.

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
command: claude --print --restricted --permission-mode dontAsk --permission-prompts none --strict-mcp-config --setting-sources '' --settings '{"permissions":{"deny":["Read(**/.env)",...]}}' --tools Read,Grep,Glob,Edit,Write --allowed-tools Edit,Write --model opus <prompt: 1832 chars>
working dir: /home/user/repo
exit code: 1
duration: 2.4s

stdout:
API Error: Connection error.

stderr:
(empty)

full output: .orchestrator/runs/2f3c.../logs/001-implement.stdout.log
```

`command:` renders the real argv verbatim except the prompt element, which is always
replaced with `<prompt: N chars>` - the prompt body itself is never echoed or logged
anywhere. Do not dump secrets or complete environment variables.

`full output:` is only present once the failing call's raw stdout/stderr has actually
been persisted (see §21) - it points at the exact file to read for output longer than
the message's 4000-byte tail.

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
provider that cannot enforce its authority is misconfiguration.

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
within the run's deadline.

Use `context.Context` throughout the execution chain.

SIGINT/SIGTERM must cancel an active execution.

Provider processes and test processes must be terminated through context cancellation.
Each child process runs in its own process group; on cancellation the whole group is
killed, not just the direct child - a provider CLI that is (or wraps) a shell script may
fork grandchildren that inherit its stdout/stderr pipes, and killing only the direct child
would leave those running, holding the pipes open and defeating the timeout.

## 25.1 Per-Action Timeout Override

Support an optional `timeout` alongside `provider`/`model` on each of the six fixed
actions:

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
  workflow/         builder, engine, transitions; imports only domain
  provider/         CLI provider abstraction, registry, claude/codex/gemini adapters
  execution/        process executor
  repository/       git (read-only helpers)
  action/           step executors and prompt builders
  testexec/         test runner
  interaction/      approval, mode select, manual-mode gate, manual choice
  persistence/      run directory
  logging/          console output
  apperr/, exitcode/
```

Jev's transport types stay unexported inside `internal/decision/jev`.

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

[2] IMPLEMENT
Provider: claude
Model: opus
→ running claude...
✓ completed (68.1s)

[3] TEST
Command: go test ./...
✓ passed

[4] REVIEW
Provider: claude
Model: sonnet
→ running claude...
✗ review failed

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
header is followed by `Authority: <authority>`. In `manual` mode the gate before the step
that follows a completed `implement` first lists the working tree's changes and
`Protected paths: unchanged`. `--verbose` adds the per-stage table:

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
a hang is visible in real time rather than as a silent pause; a `--verbose` run
additionally prints redacted argv, working directory, prompt size, a 30s heartbeat, and
the live child output prefixed with `  | ` (see SPEC-diagnostics.md §6.4).

On failure, the error rendering (§23) includes a `full output: <path>` line pointing at
the persisted raw stdout/stderr for that call - there is no separate "read the logs at..."
line printed by the CLI itself.

A workflow without development ends with the reply:

```text
Run completed successfully.

Answer:
Use a token bucket: ...
```

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

[1] IMPLEMENT
Provider: claude
Model: opus
Run this step? [y]es / [n]o / [A]ll remaining: y
→ running claude...
✓ completed (68.1s)

[2] TEST
Command: go test ./...
Run this step? [y]es / [n]o / [A]ll remaining: n
[r]etry / [a]bort: r
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

### Decision scenarios

`specs/JEV integration/TASKS.md` T13: project context without secrets, depth none
without context, forced stages, a failing Context Manager, translation and its failure,
Jev down with a manual choice, Jev down in `--mode auto`, a rejected key, a missing key,
choice and score disagreeing, and an LLM decision provider.

---

# 30. Out of Scope

Do not implement:

```text
HTTP provider APIs (except the Jev decision API, §2)
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
GitHub PR integration
GitLab integration
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

# 37. Planned Feature — Resume by Run ID (not in initial MVP delivery)

Not required for the milestone in §33/§35, but in scope for the next implementation
pass after it. Recorded now so the persistence/workflow layers aren't accidentally
built in a way that forecloses it.

## Problem

Today every `orchestrator run "<task>"` invocation starts a brand-new run: it always
decides from scratch and always calls `store.NewRun` (fresh `run.json`, empty
`Executions`). A run that failed, was stopped (`ctrl-c`, manual-mode rejection), or was
interrupted by a crash has no way to continue - the only option is to start over,
which re-runs the decision and loses the fact that steps 1..N already succeeded.

## CLI

Add a fourth command, parallel to `run`/`validate`/`version`:

```bash
orchestrator resume <run-id>
```

`--config`, `--mode`, and `--verbose` all apply exactly as they do to `run` (§4). If
`--mode` is omitted, resuming still runs the interactive mode-select prompt (§1/§27)
unless the persisted run already has a `mode` other than `plan` recorded, in which case
that becomes the default offered choice.

## Behavior

* Fail-fast validation (`config.Load` → `config.Validate` →
  `config.ValidateDependencies`, §7) always runs first, exactly as for `run` - resuming
  does not bypass dependency checks, since the operator's environment or config may
  have changed since the original run.
* Load the persisted `Run` for `<run-id>` from `.orchestrator/runs/<run-id>/run.json`
  (§21). Unknown run ID is a distinct error (new `RunNotFoundError` category, §23),
  exit code 1 (§24) - not confused with a config/dependency failure (exit code 2).
* Do **not** re-run the decision and do **not** rebuild the workflow. The persisted
  `Decision` and `Workflow` are reused as-is (§8/§9) - resume continues the exact
  workflow that was already committed to, even if the decision provider would answer
  differently today. Re-deciding on resume would let the same task silently diverge
  into a different workflow between attempts, defeating the point of resuming.
* Resumable statuses: `failed`, `stopped`. A `running` status found on load means the
  previous process died mid-step (crash, killed, machine restart) without updating
  `run.json` past that point; treat it the same as `failed` and resume from the last
  persisted `Executions` entry.
* `completed` is not resumable: report it as already done and exit 0 without invoking
  any provider.
* Re-entry point is derived the same way the engine already decides "what's next"
  after any step finishes (§11's transition table) - resume is not a special code path
  through `workflow.Engine`, it is `Engine.Run` given a `Run` whose `Executions` is
  non-empty instead of empty. `max_steps`/`max_fix_iterations` (§12) are evaluated
  against the full historical `Executions` length, including the executions from
  before the interruption - resuming a run does not reset either limit.
* `Executions` remains append-only (§11): resuming never edits or removes prior
  entries, it only appends new ones.
* If `mode` was `manual` on the original run, the manual-mode gate (§11) applies again
  on resume for every remaining step, same as a fresh manual run.

## Persistence additions

* `persistence.Store` needs a `LoadRun(runID string) (*domain.Run, error)` to pair with
  the existing `SaveRun` (§21) - reads and unmarshals `run.json`, returns
  `RunNotFoundError` if the directory/file doesn't exist.

## Out of scope for this feature

* No schema/version migration for `run.json` written by an older binary - resuming a
  run produced by an incompatible orchestrator version is undefined behavior, not a
  case this feature needs to detect or handle.
* No editing the task description, decision, or workflow on resume - if any of
  those need to change, that is a new `run`, not a resume.

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
  deny list (§8.3's list, one source: `projectctx.DenyPatterns`), then `--model` and the
  prompt last. The target repository's settings, hooks and MCP servers never load.
* codex and gemini support no authority until their flags are verified.
* `config.ValidateAuthorities` refuses an unenforceable route before any AI call (§7);
  `Execute` refuses a missing or unsupported authority without spawning anything.
* There is no bypass: the codebase never passes a permission-skipping mode.
* Provider CLIs and the test command get `execution.ScrubbedEnv()`: the inherited
  environment minus the variables through which a launching agent session reaches its
  children (`CLAUDECODE`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`,
  `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`). git keeps the full
  environment.

## 38.3 Guards around implement

* **Clean start.** A workflow containing `implement` refuses a dirty worktree (exit 2,
  before any approval prompt) unless `security.require_clean_worktree: false`.
* **Restore point.** Before the first step `run.json` records `pre_run_ref`: HEAD, or on a
  dirty tree a snapshot commit of the whole tree (untracked files included) written through
  a throwaway index - the working tree, index, stash list and refs are untouched.
* **Protected paths.** Around every `implement` call, a snapshot before is compared with
  one after, before the outcome is accepted and so before `test` runs anything: files that
  differ from HEAD or are git-ignored and match `.claude/**`, `.mcp.json`,
  `.github/workflows/**`, `.gitlab-ci.yml`, `.envrc`, `.vscode/**`, the config file when it
  lives in the worktree, or `security.protected_paths` (at any depth, by content hash); and
  a fingerprint of the repository's git config and effective hooks directory. A change
  fails the step (`WorkflowError`, exit 4) naming the paths and the restore point; the
  changes stay on disk. Without a guard, or when its snapshot fails, `implement` refuses
  before the provider call.
* **The test step.** The same guard runs around the test command, which executes code the
  model wrote; the guard compares the git fingerprint before running `git status`, which
  would itself run a planted `core.fsmonitor`. With `test.sandbox: bwrap` the command runs
  under bubblewrap: filesystem read-only, the repository and a private `/tmp` writable, the
  git directories read-only, `$HOME` an empty tmpfs, no network, only `test.ro_binds` /
  `test.rw_binds` re-exposed. The fail-fast chain checks `bwrap` on PATH, the binds, and one
  probe run with the same isolation; the authority line shows `test runs (sandboxed)`.
* **Fenced data.** The git diff (review) and test output (implement retry) are embedded in
  prompts inside a delimiter with a per-prompt random nonce and a line saying the block is
  data, never instructions. Defense in depth, not a boundary.

## 38.4 Audit

`run.json` records each AI step's `provider.authority` and the run's `pre_run_ref`; the
`authority:` line and `Restore point:` line are printed in every mode (§27).

## 38.5 Not protected

* Without `test.sandbox: bwrap`, code the model writes runs in `test` with the operator's
  authority; reviewing the changes in `manual` mode before `test` is then the mitigation.
  With it, the test can still read what the operator can read outside `$HOME`, and write
  anything inside the repository.
* Repository content goes to the model provider - that is the job; only the secret deny
  list is withheld from the file tools, matched case-sensitively.
* Prompt injection is reduced, not eliminated; its effects are bounded by the step's
  authority and exposed by the guard, review and the diff.
* `test.command` is trusted config run through `sh -c`. A repository you do not control
  additionally needs `--config` from outside it, a sandboxed test step and ideally a
  container.
* A hostile provider CLI binary is out of scope.
