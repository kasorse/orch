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
   acceptEdits --allowedTools <tools>`, prompt on stdin = task text + rendered
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
