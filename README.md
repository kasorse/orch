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
    complex: 0.40
    medium: 0.50
  stages:
    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}
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
  review:    { provider: codex,  model: gpt, timeout: 20m }

test:
  command: "go test ./..."

limits:
  max_steps: 12
  max_fix_iterations: 2

execution:
  timeout: 15m
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
  - `claude` / `codex` / `gemini` — the same questions answered by that CLI.
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
  that action's share of `execution.timeout`.
- `test.command` runs via `sh -c` in the target repository (the one place a shell is used —
  it's trusted operator configuration, not user input).
- `limits.max_steps` bounds total workflow steps; `limits.max_fix_iterations` bounds how many
  times a failing test or review can send the workflow back to `implement` — both exist purely
  to guarantee termination.
- `execution.timeout` (Go duration syntax, e.g. `15m`) bounds the whole run, including the
  decision — except the time you spend on a manual choice.

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
decision provider the same data goes to that CLI.

## Usage

```
orchestrator [--config path] version
orchestrator [--config path] validate
orchestrator [--config path] run [--verbose|-v] "<task description>"
```

- `version` — prints the build version.
- `validate` — loads and validates the config, including checking that every referenced
  provider CLI is actually resolvable on `PATH` and that the Jev API key is set, and reports
  the result without running anything. Run this first when setting up a new config.
- `run "<task>"` — decides the workflow (printing the decision: context depth, complexity,
  who decided, the workflow) and executes it step by step against the repository in the
  current working directory, printing progress as it goes.
- `--mode plan|manual|accept-edits|auto` — without it, `run` asks you to approve or cancel the
  decided workflow and then to pick a mode; `manual` confirms every step. `auto` asks nothing
  (and, if the decision provider fails, stops instead of asking you to choose).
- `--verbose` / `-v` — the sole verbosity switch (no config key or environment variable
  equivalent). Adds the per-stage decision table (probability, threshold, forced, reason) and,
  for every provider call including translation and LLM decisions: the redacted command
  line actually run (the prompt argument is always replaced with `<prompt: N chars>` — the
  prompt body itself is never printed or logged, anywhere), the working directory, a heartbeat
  line every 30s while the call is in flight, the child process's live stdout/stderr prefixed
  with `  | `, and a completion line with byte counts and the log path. Without `--verbose` you
  only see a `→ running <provider>...` line before each call and a `✓ completed (Ns)` line
  after it.

Config loading, structural validation, and dependency validation (executables on `PATH`, the
Jev API key) always run to completion, in that order, *before* any AI provider is invoked or
any request is sent to Jev — a missing executable for an action used later in the workflow
(e.g. `codex` for `review`) fails `run` immediately.

`execution.timeout` bounds the entire run, including the decision — not just workflow step
execution. Each provider/test child process runs in its own process group, so a timeout or
Ctrl-C kills the whole process tree it spawned, not just the direct child.

If the target repository has uncommitted changes when `run` starts, the orchestrator prints a
warning and continues — it never stashes, resets, or otherwise touches your working tree on
your behalf.

## Run artifacts

Each `run` persists its state under `.orchestrator/runs/<run-id>/` in the target repository:

```
.orchestrator/runs/<run-id>/
  run.json              # run status, the decision, step history, limits/iteration counters
  decision.json         # the full decision trace (see below)
  project_context.json  # exactly the context sent to the decision provider, if any
  workflow.json         # the workflow built from the decision
  artifacts/<NNN>-<action>.json          # structured output per step
  logs/<NNN>-<action>.{stdout,stderr}.log  # raw provider/test output per step
  logs/translate.{stdout,stderr}.log       # the translation call, if one was made
  logs/decide-{context,classify}.{stdout,stderr}.log  # LLM decision provider calls
```

`decision.json` answers "why did this run have these stages?": the request and its
translation, who decided (`jev`, `llm`, or `user` with the provider's error), the model
version that answered, the pre-check distribution and chosen depth, both complexity questions
with their picks and whether they agree, every stage's probability, threshold, forced flag and
reason, the workflow, and how long each decision step took.

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
command: claude --print --permission-mode bypassPermissions --model opus <prompt: 1832 chars>
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

- `failure:` says *how* the call failed: `spawn` (couldn't start the executable at all),
  `exit` (ran and returned non-zero), `timeout` (exceeded `execution.timeout`), or `cancelled`
  (SIGINT/SIGTERM). Some provider CLIs — notably `claude --print` — write backend/API errors to
  stdout rather than stderr, so both streams are always shown; a blank stream renders as
  `(empty)` rather than nothing, so you can tell "no output" from "output not captured".
- `stdout:`/`stderr:` are each truncated to their last 4000 bytes in this message. The
  `full output:` line, when present, points at the untruncated file on disk.
- `command:` is the real argv that ran, except the prompt argument, which always renders as
  `<prompt: N chars>` — the prompt text itself is never printed or written to any log file.

## Testing

```
go build ./...
go vet ./...
go test ./...
gofmt -l .   # should print nothing
```

`go test ./...` requires no network access, no Jev key and no real `claude`/`codex`/`gemini`
installs: the `test/integration` suite uses fake executables installed on a scoped `PATH` and
a fake Jev served by `httptest`.

## Limitations

This is an MVP, deliberately scoped down (see `MVP.md` for the full rationale). Notably absent:

- No HTTP-based provider APIs (OpenRouter, LiteLLM, etc.) — the AI steps run only as local
  CLI subprocesses. The one HTTP integration is the Jev decision API, which only returns
  probabilities.
- No automatic model selection, routing, or fallback between providers — including between
  decision providers.
- Stages cannot yet be edited before a run (approve or cancel only); the decision thresholds
  are calibrated for Jev and shared with LLM decision providers.
- No parallel or multi-agent workflows — one task, one sequential workflow, one run at a time.
- No web UI, REST API, or IDE plugin — CLI only.
- No GitHub/GitLab integration or remote repository execution — operates on the local working
  directory only.
- No RAG, persistent conversational memory, or database — state is per-run JSON on disk.
- No custom/dynamically-defined action types or user-defined workflow language — the stage
  set, their order and the policy rules are fixed in Go code; only thresholds are configured.
