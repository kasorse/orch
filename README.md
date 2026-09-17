# AI Model Orchestrator

A local Go CLI that takes one engineering task, classifies it, deterministically builds a
workflow, and executes that workflow by shelling out to locally-installed AI CLIs (`claude`,
`codex`, `gemini`) against your current git repository — running real tests and persisting run
state to disk. The full spec is `MVP.md`; `CLAUDE.md` summarizes the invariants that guide the
implementation.

The orchestrator owns classification, workflow selection, provider routing, retry/limit logic,
and persistence in Go code. The AI models only do reasoning (analysis, planning,
implementation, review) — they never decide what happens next; that's always a deterministic
Go state machine. Test pass/fail is decided only by the real exit code of your test command,
never by anything a model says.

## Prerequisites

- Go 1.26.1 or later.
- `git`, on `PATH`, and the target repository must already be a git repo.
- For each provider referenced in your config (`claude`, `codex`, and/or `gemini`), that
  provider's CLI must be installed and resolvable via `PATH` (`exec.LookPath`). You only need
  the providers your config actually uses — if every action is routed to `claude`, `codex` and
  `gemini` don't need to be installed at all.

## Building

```
go build -o orchestrator ./cmd/orchestrator
```

## Configuration

Copy `orchestrator.example.yaml` to `orchestrator.yaml` (the default config path; override with
`--config <path>`) and adjust it:

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

- `classifier` and each entry under `actions` independently choose a `provider`
  (`claude` | `codex` | `gemini`) and a `model` string passed straight through to that
  provider's CLI — there's no automatic "best model" selection, fallback, or benchmarking.
- `test.command` runs via `sh -c` in the target repository (the one place a shell is used —
  it's trusted operator configuration, not user input).
- `limits.max_steps` bounds total workflow steps; `limits.max_fix_iterations` bounds how many
  times a failing test or review can send the workflow back to `implement` — both exist purely
  to guarantee termination.
- `execution.timeout` (Go duration syntax, e.g. `15m`) bounds the whole run.

## Usage

```
orchestrator [--config path] version
orchestrator [--config path] validate
orchestrator [--config path] run [--verbose|-v] "<task description>"
```

- `version` — prints the build version.
- `validate` — loads and validates the config, including checking that every referenced
  provider CLI is actually resolvable on `PATH`, and reports the result without running
  anything. Run this first when setting up a new config.
- `run "<task>"` — classifies the task, builds the workflow deterministically from that
  classification, and executes it step by step against the repository in the current working
  directory, printing progress as it goes.
- `--verbose` / `-v` — the sole verbosity switch (no config key or environment variable
  equivalent). Adds, for every provider call including classification: the redacted command
  line actually run (the prompt argument is always replaced with `<prompt: N chars>` — the
  prompt body itself is never printed or logged, anywhere), the working directory, a heartbeat
  line every 30s while the call is in flight, the child process's live stdout/stderr prefixed
  with `  | `, and a completion line with byte counts and the log path. Without `--verbose` you
  only see a `→ running <provider>...` line before each call and a `✓ completed (Ns)` line
  after it.

Config loading, structural validation, and dependency (executable-on-`PATH`) validation always
run to completion, in that order, *before* any AI provider or the classifier is ever invoked —
a missing executable for an action used later in the workflow (e.g. `codex` for `review`) fails
`run` immediately, before `claude` is even called for classification.

`execution.timeout` bounds the entire run, including classification — not just workflow step
execution. Each provider/test child process runs in its own process group, so a timeout or
Ctrl-C kills the whole process tree it spawned, not just the direct child.

If the target repository has uncommitted changes when `run` starts, the orchestrator prints a
warning and continues — it never stashes, resets, or otherwise touches your working tree on
your behalf.

## Run artifacts

Each `run` persists its state under `.orchestrator/runs/<run-id>/` in the target repository:

```
.orchestrator/runs/<run-id>/
  run.json              # run status, step history, limits/iteration counters
  classification.json   # the classifier's output for this task
  workflow.json          # the deterministic workflow built from that classification
  artifacts/<NNN>-<action>.json          # structured output per step
  logs/<NNN>-<action>.stdout.log         # raw provider/test stdout per step
  logs/<NNN>-<action>.stderr.log         # raw provider/test stderr per step
  logs/classify.stdout.log               # raw classifier-call stdout (if any)
  logs/classify.stderr.log               # raw classifier-call stderr (if any)
```

Every provider call's raw stdout/stderr is persisted this way, whether it succeeds or fails —
not just failures. The classifier call's logs live under `logs/classify.*.log` since
classification happens outside the workflow's per-step numbering.

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

`go test ./...` requires no network access and no real `codex`/`gemini` installs: the
`test/integration` suite (covering the 9 required end-to-end scenarios) uses fake `claude` /
`codex` / `gemini` executables installed on a scoped `PATH`, not real provider access.

## Limitations

This is an MVP, deliberately scoped down (see `MVP.md` for the full rationale). Notably absent:

- No HTTP-based provider APIs (OpenRouter, LiteLLM, etc.) — only local CLI subprocesses.
- No automatic model selection, routing, or fallback between providers.
- No parallel or multi-agent workflows — one task, one sequential workflow, one run at a time.
- No web UI, REST API, or IDE plugin — CLI only.
- No GitHub/GitLab integration or remote repository execution — operates on the local working
  directory only.
- No RAG, persistent conversational memory, or database — state is per-run JSON on disk.
- No custom/dynamically-defined action types or user-defined workflow language — the action
  set and workflow-selection table are fixed in Go code.
