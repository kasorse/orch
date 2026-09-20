# Task: Implement AI Model Orchestrator MVP

Implement the MVP of a local CLI application called **AI Model Orchestrator**.

The implementation language must be **Go**.

The application is intended for software developers using AI coding assistants. A user provides one engineering task. The orchestrator classifies the task, selects a deterministic workflow, executes the required stages with configured AI providers, runs tests, performs review, and may return the workflow back to implementation when tests or review fail.

Do not over-engineer the solution. Build a clean MVP with explicit interfaces, deterministic workflow logic, good testability, and minimal external dependencies.

---

## 1. Product Behavior

The user runs:

```bash
orchestrator run "Fix retry logic. HTTP 404 must not be retried"
```

The application should:

```text
validate configuration
        ↓
classify task
        ↓
build workflow
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

By default `orchestrator run` is interactive: after classification and workflow
construction it prints the classification and derived workflow, lets the user approve
it, edit `type`/`complexity`/`requires_tests`/`requires_review` and rebuild the plan, or
cancel, then lets the user choose an execution mode (`plan`, `manual`, `accept-edits`, or
`auto`) before anything runs. Passing `--mode auto` skips all of that and reproduces the
original fully-unattended behavior described above exactly - this is the escape hatch for
scripts and CI. See §4 for the flag and §27 for example transcripts of both.

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

Do not integrate directly with provider HTTP APIs.

Do not add LiteLLM, OpenRouter, a database, Web UI, IDE plugin, server mode, or distributed execution.

---

# 3. Important Architecture Principle

The responsibilities must be separated as follows:

```text
LLM
→ task reasoning
→ analysis
→ implementation
→ review

Orchestrator
→ classification invocation
→ workflow selection
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

Omitting `--mode` runs the approve/edit loop and then interactively asks the user to
choose one of the four modes above (see §1 and §27).

---

# 5. Configuration

Use YAML.

Example:

```yaml
version: 1

classifier:
  provider: claude
  model: sonnet

actions:
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
    provider: codex
    model: gpt

  answer:
    provider: gemini
    model: pro

test:
  command: "go test ./..."

limits:
  max_steps: 12
  max_fix_iterations: 2

execution:
  timeout: 15m
```

Model names must be treated as opaque provider-specific strings.

Do not hardcode lists of allowed models.

---

# 6. Configuration Validation

Configuration validation must happen completely **before any AI call or repository modification**.

Validate:

* YAML syntax;
* config version;
* required classifier configuration;
* required provider;
* required model;
* configured actions;
* supported provider names;
* limits;
* execution timeout;
* test command when test execution may be required.

Supported provider IDs are only:

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

The classifier provider must also be checked.

### Critical fail-fast requirement

Given:

```text
claude installed
codex missing
```

and config uses Claude for classification and Codex for review:

```bash
orchestrator run "..."
```

must fail before Claude is invoked.

Do not partially execute the workflow and discover missing dependencies later.

---

# 8. Task Classification

The classifier is an AI call using:

```yaml
classifier:
  provider: ...
  model: ...
```

It must produce structured JSON.

Expected model:

```json
{
  "type": "bugfix",
  "complexity": "medium",
  "requires_code_change": true,
  "requires_tests": true,
  "requires_review": true
}
```

Supported task types:

```text
question
bugfix
feature
refactor
test
review
```

Supported complexity values:

```text
low
medium
high
```

Parse and validate the response strictly.

Invalid JSON or unsupported enum values must return a classification error.

The classifier must not invent workflow steps.

---

# 9. Deterministic Workflow Builder

Workflow selection belongs to Go code.

Start with this mapping:

```text
question
→ answer

bugfix + low
→ implement → test → review

bugfix + medium/high
→ analyze → implement → test → review

feature
→ plan → implement → test → review

refactor
→ analyze → implement → test → review

test
→ test

review
→ review
```

Also respect:

```text
requires_code_change
requires_tests
requires_review
```

The same classification must always produce the same workflow.

Do not ask an LLM to generate arbitrary workflow graphs.

---

# 10. Supported Actions

Define a fixed set:

```text
analyze
plan
implement
test
review
answer
```

No dynamically invented action types.

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

Required transitions:

```text
analyze success
→ implement

plan success
→ implement

implement success
→ test if required
→ otherwise review if required
→ otherwise done

test success
→ review if required
→ otherwise done

test failure
→ implement

review pass
→ done

review fail
→ implement
```

After a review failure, the normal flow should be:

```text
review
→ implement
→ test
→ review
```

if tests are required.

Maintain execution history rather than overwriting previous step runs.

Support an optional `Gate` hook the engine consults immediately before each step
executes:

```text
Gate.BeforeStep(ctx, run, index, action) -> proceed | abort
```

A nil `Gate` reproduces fully-automatic execution exactly (today's behavior, and
`--mode auto`/`accept-edits`). A `Gate` implementation may mutate `run`'s classification
and workflow before returning `proceed` (e.g. manual mode's decline → edit path); such a
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
* non-zero process exit.

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

If there are existing uncommitted changes, display a warning:

```text
Warning: repository contains uncommitted changes.
Existing changes will not be reverted.
```

Do not automatically stash, reset, checkout, or revert user files.

After implementation obtain the actual Git diff.

The actual repository state is the source of truth.

---

# 17. Action Context and Artifacts

Do not pass an indefinitely growing chat transcript between models.

Use structured artifacts plus repository state.

Implement artifact types similar to:

```text
AnalysisArtifact
PlanArtifact
ImplementationArtifact
TestArtifact
ReviewArtifact
```

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
classifier
analyze
plan
implement
review
answer
```

Do not assemble large prompts ad hoc inside workflow logic.

Use typed input structures.

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

Suggested location:

```text
.orchestrator/
  runs/
    <run-id>/
      run.json
      classification.json
      workflow.json

      artifacts/
        ...

      logs/
        ...
```

Use UUID or equivalent unique Run ID.

Persist enough data so a completed or failed run can later be inspected.

At minimum save:

* original task;
* run ID;
* classification;
* workflow;
* executed steps;
* provider/model for each AI step;
* step result;
* test result;
* review result;
* final run status;
* failure reason if any;
* execution mode the run actually ran under (`mode`);
* the classifier's original, unedited classification, if the user edited it during the
  approve/edit loop or a manual-mode step gate (`original_classification`) - the
  `classification` field always holds the effective classification that was actually
  built into the workflow.

No database is required.

---

# 22. Domain Model

Design clear domain types.

Suggested concepts:

```text
Task
Classification
Run
Workflow
Step
StepExecution
Artifact
ProviderRef
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
to `Classification`/`Workflow` - the same classification always produces the same
workflow regardless of mode.

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
ClassificationError
WorkflowError
RepositoryError
ExecutionError
LimitExceededError
```

Preserve underlying errors.

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
    Action     string        // "classify" for the classifier call
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
command: claude --print --permission-mode bypassPermissions --model opus <prompt: 1832 chars>
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

---

# 25. Timeout and Cancellation

Support:

```yaml
execution:
  timeout: 15m
```

`execution.timeout` bounds the entire run, not just workflow step execution: it wraps
opening the repository, classification, and the full engine run (analyze/plan/implement/
test/review). A provider call that hangs during classification is bounded by the same
timeout as one that hangs during an implement step - there is no separate,
un-timed phase.

Use `context.Context` throughout the execution chain.

SIGINT/SIGTERM must cancel an active execution.

Provider processes and test processes must be terminated through context cancellation.
Each child process runs in its own process group; on cancellation the whole group is
killed, not just the direct child - a provider CLI that is (or wraps) a shell script may
fork grandchildren that inherit its stdout/stderr pipes, and killing only the direct child
would leave those running, holding the pipes open and defeating the timeout.

## 25.1 Per-Action Timeout Override

Support an optional `timeout` alongside `provider`/`model` on each of the five fixed
actions (`classifier` is excluded - it has no override):

```yaml
actions:
  review:
    provider: codex
    model: gpt
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

# 26. Suggested Project Structure

Use a clean package structure similar to:

```text
cmd/
  orchestrator/
    main.go

internal/

  app/
    app.go

  config/
    config.go
    loader.go
    validator.go
    dependencies.go

  domain/
    task.go
    classification.go
    run.go
    workflow.go
    artifact.go

  classifier/
    classifier.go
    prompt.go

  workflow/
    builder.go
    engine.go
    transitions.go

  provider/
    client.go
    registry.go

    claude/
      client.go

    codex/
      client.go

    gemini/
      client.go

  execution/
    process.go

  repository/
    git.go

  action/
    analyze.go
    plan.go
    implement.go
    review.go
    answer.go
    prompts.go

  testexec/
    runner.go

  persistence/
    filesystem.go

  logging/
```

You may adjust the structure if there is a clearly better Go design, but preserve separation of concerns.

---

# 27. CLI Output

Provide readable progress.

Example (`--mode auto` - no prompts, identical output shape to before execution modes
existed):

```text
$ orchestrator run "Fix retry logic. 404 must not be retried" --mode auto

Validating configuration...
✓ configuration valid
Run: 2f3c1a8b  (.orchestrator/runs/2f3c1a8b)

Classifying task...
→ claude (model sonnet)
✓ type: bugfix
✓ complexity: medium

Workflow:
analyze → implement → test → review

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
Provider: codex
Model: gpt
→ running codex...
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
Provider: codex
Model: gpt
→ running codex...
✓ PASS

Run completed successfully.
```

Every provider call - classification and each workflow step - prints its `→ running
<provider>...` (or, for classification, `→ <provider> (model <model>)`) line immediately
before the call, so a hang is visible in real time rather than as a silent pause; a
`--verbose` run additionally prints redacted argv, working directory, prompt size, a 30s
heartbeat, and the live child output prefixed with `  | ` (see §21, §6.4 of
SPEC-diagnostics.md).

On failure, the error rendering (§23) includes a `full output: <path>` line pointing at
the persisted raw stdout/stderr for that call - there is no separate "read the logs at..."
line printed by the CLI itself.

Example (no `--mode` flag - the interactive default: approve/edit the plan, choose a
mode, then confirm each step under `manual`):

```text
$ orchestrator run "Fix retry logic. 404 must not be retried"

Validating configuration...
✓ configuration valid
Run: 2f3c1a8b  (.orchestrator/runs/2f3c1a8b)

Classifying task...
→ claude (model sonnet)
✓ type: bugfix
✓ complexity: medium

Workflow:
analyze → implement → test → review

Approve this plan? [a]pprove / [e]dit / [c]ancel: e
Edit which field? [t]ype / [c]omplexity / [1] requires_tests / [2] requires_review / [d]one: c
New complexity (low/medium/high): low

Workflow:
implement → test → review

Approve this plan? [a]pprove / [e]dit / [c]ancel: a

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
[r]etry / [e]dit plan / [a]bort: e
Edit which field? [1] requires_tests / [2] requires_review / [d]one: 2
requires_review (y/n): n

Workflow (remaining): test
[r]etry / [e]dit plan / [a]bort: r
✓ passed

Run completed successfully.
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
* classifier provider missing;
* no AI execution happens after validation failure.

### Classifier

* valid classification;
* malformed JSON;
* invalid type;
* invalid complexity.

### Workflow builder

Cover every supported task type and relevant complexity.

### Workflow engine

* successful forward workflow;
* test failure → implementation;
* review failure → implementation;
* review failure → implementation → test → review;
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

### Scenario 1 — successful bugfix

```text
classification
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

### Scenario 8 — invalid classifier response

Run fails before repository-changing actions.

### Scenario 9 — max iterations reached

Workflow stops and does not invoke additional agents.

---

# 30. Out of Scope

Do not implement:

```text
HTTP provider APIs
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

Do not implement automatic "best model" routing.

### 31.2 Deterministic workflows

Classifier returns task properties.

Go code builds workflow.

Do not let an LLM invent workflow stages.

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
classifies from scratch and always calls `store.NewRun` (fresh `run.json`, empty
`Executions`). A run that failed, was stopped (`ctrl-c`, manual-mode rejection), or was
interrupted by a crash has no way to continue - the only option is to start over,
which re-runs classification and loses the fact that steps 1..N already succeeded.

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
* Do **not** re-run classification and do **not** rebuild the workflow. The persisted
  `Classification` and `Workflow` are reused as-is (§8/§9) - resume continues the exact
  workflow that was already committed to, even if the classifier would answer
  differently today. Re-classifying on resume would let the same task silently diverge
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
* No editing the task description, classification, or workflow on resume - if any of
  those need to change, that is a new `run`, not a resume.

