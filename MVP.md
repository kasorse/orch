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
* failure reason if any.

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

Provider execution errors should include useful context:

```text
provider execution failed

provider: claude
action: implement
model: opus
exit code: 1

stderr:
...
```

Do not dump secrets or complete environment variables.

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

Use `context.Context` throughout the execution chain.

SIGINT/SIGTERM must cancel an active execution.

Provider processes and test processes must be terminated through context cancellation.

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

Example:

```text
$ orchestrator run "Fix retry logic. 404 must not be retried"

Validating configuration...
✓ configuration valid

Classifying task...
✓ type: bugfix
✓ complexity: medium

Workflow:
analyze → implement → test → review

[1] ANALYZE
Provider: claude
Model: sonnet
✓ completed

[2] IMPLEMENT
Provider: claude
Model: opus
✓ completed

[3] TEST
Command: go test ./...
✓ passed

[4] REVIEW
Provider: codex
Model: gpt
✗ review failed

1 high severity issue found.
Returning to implementation (iteration 1/2).

[5] IMPLEMENT
✓ completed

[6] TEST
✓ passed

[7] REVIEW
✓ PASS

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

