# AI Model Orchestrator

A local Go CLI that takes one engineering task, classifies it, deterministically builds a
workflow, and executes it by shelling out to locally-installed AI CLIs (`claude`, `codex`,
`gemini`) against the current git repository, running real tests and persisting run state to
disk. Full spec: `MVP.md` — treat it as the source of truth; this file is a summary to keep
future work aligned with it, not a replacement for it.

## Architecture (MVP.md §3)

Responsibilities are strictly separated:

- **LLM** — task reasoning, analysis, implementation, review.
- **Orchestrator (this codebase)** — classification invocation, deterministic workflow
  selection, provider routing, state transitions, limits, artifacts/context, test execution,
  persistence.
- **Git** — source of truth for code changes.
- **Test command** — source of truth for test pass/fail (exit code only, never LLM text).

Orchestrator/workflow code must never contain AI-provider-specific behavior. Per-provider CLI
argv quirks are isolated in each provider's `buildArgs` function
(`internal/provider/{claude,codex,gemini}/client.go`).

`internal/workflow` must never import `config`/`provider`/`repository` — it depends only on
`domain` and the `StepExecutor` interface it defines itself. `internal/action` is the
composition point that implements `StepExecutor` using config/provider/repository/testexec.

## Fixed tables (MVP.md §9–10) — Go code, not LLM-invented

Actions: `analyze`, `plan`, `implement`, `test`, `review`, `answer` — fixed set, no dynamic
action types.

Workflow builder (`internal/workflow/builder.go`), by classified task type:

```
question           → answer
test                → test
review              → review
bugfix + low        → implement → test → review
bugfix + medium/high→ analyze → implement → test → review
feature             → plan → implement → test → review
refactor            → analyze → implement → test → review
```

`requires_tests`/`requires_review` prune the trailing `test`/`review` steps. The same
classification always produces the same workflow.

Engine transitions (`internal/workflow/transitions.go`) are a state machine, not a for-loop
over a fixed list: analyze/plan success → implement; implement success → test if required,
else review if required, else done; test failure → implement; review fail → implement (which
naturally cascades back through test → review). Execution history is append-only
(`run.Executions`), never overwritten. Two independent limits guard against infinite loops:
`max_steps`, `max_fix_iterations`.

## Non-negotiable invariants

- **Fail fast, before any AI call** (MVP.md §7): `config.Load` → `config.Validate` →
  `config.ValidateDependencies` always run fully, in order, before any provider/classifier
  object is constructed. A missing executable for an action used later in the workflow (e.g.
  Codex for `review`) must fail `orchestrator run` before Claude is ever invoked for
  classification.
- **Only exit codes and git diff are trusted, never LLM prose** (MVP.md §18/§19/§31.4):
  test pass/fail comes only from the test command's real exit code; review inspects the actual
  `git diff`, not just an implementation summary. Never parse phrases like "all tests should
  pass" out of model output as a substitute for running the tests.
- **Static provider routing** (MVP.md §31.1): provider/model per action comes from config.
  No automatic "best model" selection or fallback.
- Provider CLI invocation is argv-only (`exec.CommandContext`), never `sh -c`, for the user's
  task/prompt — the one documented exception is `test.command`, which is trusted operator
  config and does go through `sh -c` to support shell syntax.

## Out of scope (MVP.md §30)

No HTTP provider APIs/OpenRouter/LiteLLM, no model benchmarking or automatic model
selection/routing/fallback, no parallel or multi-agent workflows, no Web UI/REST API/IDE
plugin, no GitHub/GitLab integration, no remote repository execution, no RAG or persistent
conversational memory, no database, no custom dynamically-defined action types or
user-defined workflow language. Don't add abstractions only useful for these.

## Build & test

```
go build ./...
go vet ./...
go test ./...
gofmt -l .          # must be empty
```

## Run artifacts

Persisted under `.orchestrator/runs/<run-id>/`: `run.json`, `classification.json`,
`workflow.json`, `artifacts/<NNN>-<action>.json`, `logs/<NNN>-<action>.{stdout,stderr}.log`.
