# H10 — Integration scenarios

**Size** M (1–1.5 d) · **Depends on** H01–H09 · **Blocks** H11

## Goal

End-to-end coverage of phase 0 with the fake CLI, fake test commands and real git, as
scenario 23 (`test/integration/scenario23_hardening_*_test.go`). Some cases are already listed
in the tasks' acceptance; this task makes sure each exists once, as a scenario, and that the
old scenarios still pass unchanged in behavior.

## Scenarios

| id | setup | expected |
|---|---|---|
| 23a | 1 MiB task text (research only) | run completes; no argv element > 4 KiB (`FAKE_ARGS_DIR`) |
| 23b | implement creates `internal/new.go`; review | `review.txt` prompt contains its `+` lines |
| 23c | implement modifies `go.sum` (big) and `go.mod` | `go.sum` stat line only; `go.mod` full |
| 23d | 400 KiB diff, `max_diff_bytes: 65536` | review artifact `diff.truncated: true`; prompt ≤ ~70 KiB |
| 23e | failing test prints 2 MiB with a `--- FAIL` line in the middle | implement retry prompt keeps that line, has the omission marker, ≤ budget |
| 23f | env has `ANTHROPIC_API_KEY`, `GOFLAGS`; `env_pass: [GOPROXY]` | test env: `GOFLAGS`, `GOPROXY`, no key; provider env unchanged |
| 23g | no `test.sandbox`, no bwrap on PATH (fake checker) | exit 2, both remedies named |
| 23h | `FAKE_CLI_VERSION=2.2.0` | exit 2, nothing called; with override → runs, `verified: false` recorded |
| 23i | red suite before implement | exit 4, no implement call, `logs/pre-run-test.*` |
| 23j | red suite, `baseline: off` | implement runs |
| 23k | resume of 23i after the suite turns green | pre-run test passes, workflow from the first step |
| 23l | implement writes an AWS key, `mode: stop` | exit 4, file:line:rule, value nowhere in the run dir |
| 23m | same, `mode: warn` | completes; finding recorded; review prompt redacted |
| 23n | secret present before the run, dirty start allowed | no finding |

## Steps

1. Add helpers: `withEnv`, `fakeTestCommand(script)`, `grepRunDir(t, runDir, needle) bool`.
2. Write the scenarios; reuse the existing harness patterns (scenario 15/16 for guard and
   sandbox, 18 for resume).
3. Re-run the whole suite; any change to an older scenario must be the explicit
   `test.sandbox: none` or the argv/stdin change from H01 — nothing else.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-05. Most rows were written with their task, under the next free scenario
number, so the ids differ from the table:

| row | scenario |
|---|---|
| 23a | 23 (`scenario23_large_prompt_test.go`, H01) |
| 23b, 23c, 23d, 23e | **28** (`scenario28_bounded_prompts_test.go`, this task) |
| 23f | 25 (H04) |
| 23g | 16c (H05: a bwrap that cannot sandbox, rather than a missing one - the integration harness uses the real `LookPath`) |
| 23h | 24 (H06) |
| 23i, 23j, 23k | 26 (H07) |
| 23l, 23m, 23n | 27 (H09) |

Scenario 28 runs `run --stage review` over hand-made changes (a review-only workflow may
start dirty) to see what the review prompt holds: a new file's `+` lines, `go.sum` as
`summarized` with `go.mod` in full, a deny-listed `config/secrets.yaml` withheld, and the
artifact's diff counts; a ~430 KiB change under `max_diff_bytes: 65536` (prompt < 80 KiB,
`not shown: diff budget`, `truncated: true`); and a ~1 MB failing test output whose
`--- FAIL` line in the middle survives into the retried implement prompt (< 48 KiB, cut
marked) while the step log keeps all of it. The config helper gained `promptLimits`.

Older scenarios changed only where phase 0 changed what they rely on: explicit
`test.sandbox: none` and `test.baseline: off` in the helper config (H05, H07), argv without
the prompt (H01), scenario 19g's output path in the command instead of `$ENV_OUT` (H04),
scenario 16's fake bwrap with its settings baked in (H04), and scenario 4's simulated
`ls-files` failure narrowed to the Context Manager's call (H03).
