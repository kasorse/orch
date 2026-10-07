# BM09 — Config runners: bare Claude Code and orchestrator templates

**Size** M (2 d) · **TODO** B2 · **Depends on** BM02, BM04 · **Blocks** BM10

## Goal

Run one config in a prepared workspace and return what the result line needs from it
(`SPEC.md` §8, §9, §13.7).

## Files

- `bench/configs/common.yaml`, `A.yaml`, `B.yaml`, `C.yaml`, `D.yaml`, `Bmock.yaml` (B′)
- `bench/internal/config` + test: load a bench config (`kind: claude | orchestrator`,
  `stages`, `config` template, `tools`, `prompt_suffix`), render the template with
  `text/template` (`Model`, `TestCommand`, `RoBinds`, `RwBinds`, `Timeout`)
- `bench/internal/runner` + test: `Run(ctx, cfg, task, ws) (Outcome, error)`; `claude.go`,
  `orchestrator.go`; readers of stream-json, `run.json`, `decision.json` (own minimal
  structs — no import of the core)

## Steps

1. **A**: `claude -p --model <m> --output-format stream-json --verbose --permission-mode
   acceptEdits --allowed-tools <tools>` (the spec first said `--allowedTools`; see "As
   built"), prompt on stdin = task text + rendered
   `prompt_suffix`, working dir = workspace, the task's timeout. Parse the `result` event:
   `total_cost_usd`, `usage`, `num_turns`, `result` text (the answer for questions),
   `is_error`. **Verify the exact flags once against the installed CLI** and write what was
   checked in the task's "As built" (the only live step of the phase).
2. **Orchestrator configs**: render the config into `<out>/configs/<task>-<config>.yaml`
   (hash it), run `orchestrator --config <file> run --mode auto [--stage …] "<task>"` in the
   workspace with the task's timeout. Then read the single run directory under
   `.orchestrator/runs/`: `run.json` (`status`, `failure_reason`, `outcome`, executions),
   `decision.json` (`workflow`, complexity, `usage`), the final answer for questions (the
   last read-only artifact's `answer`).
3. Errors that are not the model's — the binary missing, a rate-limit message, a crash
   before any step, a run directory that is not there — are returned as infra errors;
   an orchestrator exit code of 3 (provider execution) with a rate-limit text is infra,
   other failures are results.
4. `Outcome` for the result line: cost, tokens, calls, decision overhead, stages, complexity,
   status, fix iterations, review verdict, answer text.

## Acceptance

- Template rendering golden per config; the rendered B/C/D configs pass `orchestrator
  validate` (run in the test only if the binary is built — a skip otherwise).
- Runner tests with fake `claude` and fake `orchestrator` executables writing canned
  stream-json / run directories: every field mapped; infra vs result classification table.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-07. Differences from the plan, and why:

- **Each orchestrator template is the whole `orchestrator.example.yaml`, not the short form of
  SPEC §13.7:** the core loads a version-2 file strictly and needs every action, the limits,
  the policy and `execution.timeout`, so a fragment would not validate. The per-task fields are
  `{{.Model}}` (implement's model), `{{.TestCommand}}`, `{{.RoBinds}}`, `{{.RwBinds}}` and
  `{{.Timeout}}`. Strings are escaped for the inside of double quotes and the bind lists are
  JSON flow lists, so paths with spaces or quotes stay valid YAML; the rendering is parsed as
  YAML before it is used. `decision.on_failure` is `fail`, not the example's `ask`, which would
  wait on stdin in a headless run. Config A's prompt suffix is not escaped.
- **A template that names an unknown variable fails at `config.Load`,** not at the first
  task: both templates are executed once against a probe at load time.
- **The orchestrator is started with the task text as its last argument and no `--`:** its
  argument parser reads flags from anywhere, so a task whose text starts with `-` is refused
  before anything runs (an error, not an infra error). For the same reason the runner passes
  `--config <file> --mode auto run [--stage …] <task>`, with `--mode` before the command;
  the core reads it the same as the step's `run --mode auto`.
- **Timeouts:** config A gets the task's timeout. The orchestrator gets it plus 30 s
  (`orchestratorGrace`): its own `execution.timeout` is the same duration, so it stops itself
  and writes the failed run first, and the runner's kill is only the backstop. Both run in their
  own process group, killed as a whole. A timeout is a result (`failed`, `timeout`), never infra.
- **Classification:** infra are a missing binary, exit 2 (config or dependency), exit 5, no run
  directory or more than one, a run directory already in the workspace before the start, an
  unreadable `run.json`, a non-zero exit with no execution recorded, and a rate-limit text. The
  text is looked for only in a run that failed and only where a provider CLI writes it. For
  config A that means no result event, an error `result` event or a non-zero exit, and the text
  is stderr, stdout lines that are not stream-json events, and the error result's text. A claude
  that retried after a 429 and then finished keeps its result, and the transcript, which holds
  the tools' output, is never searched. For the orchestrator it is exit 3 only (stderr,
  `failure_reason`, the last execution's error). Test output is never searched. A cancelled
  caller context returns `ctx.Err()`, not a result.
- **No `outcome` block:** the numbers are recomputed from the executions and `decision.json`
  as `domain.ComputeOutcome` does (a `--stage` decision costs nothing), so a killed run still
  has a cost.
- **Stages** come from the enabled entries of `decision.json`, else from the executions' actions.
- **The copies of the template are kept in step by tests:** B, B′, C and D each hold the whole
  template, so `TestOrchestratorConfigsDifferOnlyInTheDecision` requires their renderings to be
  identical outside `decision`, and `TestOrchestratorConfigsFollowTheExample` requires them to
  equal `orchestrator.example.yaml` outside `decision`, `test` and `execution` (with `implement`
  on the common model). An edit to a threshold, model or limit in one place fails the test.
  `TestBmockDecisionFollowsTheExample` holds B′'s `decision` to the example's Jev decision,
  changed only in `on_failure: fail` and `jev.mock: true`. Read against the core's
  `validateDecision` and `jevKeyProblems` (`internal/config/decision.go`), those values satisfy
  it: a non-empty model (`jev-1.13.0`, as in the core's loader tests), a positive timeout, an
  http(s) `base_url`, a set `api_key_env` and `max_retries` in 0–3. With `mock: true` no API key
  has to be present.
- **The rendered config is validated by `orchestrator validate` in a test** that is skipped
  without `bin/orchestrator` (or `ORCHBENCH_ORCHESTRATOR`), `claude` on PATH and a working
  bwrap, and when the installed claude is outside the range the orchestrator verified.
- **Config A's flags, checked live 2026-10-07 against `claude` 2.1.293 (Claude Code)**, inside
  the range the core adapter verified (2.1.283 to below 2.2.0). `claude --help` lists every flag
  the runner passes: `-p, --print`; `--model <model>`; `--output-format` with `stream-json` among
  its choices; `--verbose`; `--permission-mode` with `acceptEdits` among its choices; and
  `--allowedTools, --allowed-tools <tools...>`, two spellings of one flag. The runner keeps
  `--allowed-tools`, as the core adapter (`internal/provider/claude`) does. The result event's
  fields (`result`, `is_error`, `total_cost_usd`, `usage`) are the ones the adapter reads.
  `--tools` is not passed: it narrows the tools the model sees, which the spec's bare Claude
  Code does not do; the other built-in tools are visible and, in `-p` mode, denied.
- **Rate-limit patterns are narrow:** the account's own wording (`rate limit`, `usage limit`,
  `hit your limit`, the 5-hour or weekly limit, `too many requests`, `overloaded_error` or
  `API Error: Overloaded`, `API Error: 429`, quota, credit balance). A bare `limit reached`,
  `try again later` or `overloaded` is not one, so a model-side `output token limit reached`
  stays a result; `TestRateLimited` has these as negative cases.
- **`answer` of a question** is read only from the step's `artifact_path`; with no usable path
  the answer is empty, and a format drift shows up as an empty answer rather than being guessed
  around.
- **Gates, 2026-10-07, in `bench/` on the final code:** `go build ./...`, `go vet ./...` and
  `go test ./...` (443 tests in 15 packages) exit 0, `gofmt -l .` is empty. The orchestrator
  run that wrote this task could not run them: its implement step has no shell, and its test
  step runs the root module's `go test ./...`, which does not include `bench/`, a separate
  module. `TODO.md` B2 stays `[~]`: it also covers BM10–BM13 and BM16.
