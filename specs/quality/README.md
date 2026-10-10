# Quality — a better result from the same stages (phase 2)

Status: **in progress** (specified 2026-10-08): the owner went through TODO.md Q1–Q8 one by one. Q1, Q2,
Q3, Q5, Q6 and Q7 are in this spec. Q4 (worktree isolation) gets its own spec together with
W1. Q8 (the cost of the decision rounds) waits for the benchmark decision B4. The decisions
are in `SPEC.md` §14 and the author's defaults in §15. In progress (2026-10-09): QL01, QL02
and QL03 done (stage context in the prompts, `acceptance_criteria`, the retry diff stat, prompt
golden files; the review verdict per criterion and the read-the-surroundings prompt; the
`checks:` in the test step and the pre-run test);
QL04 done too (the `internal/verify` MCP server, the hidden `mcp-verify` subcommand, the guard
moved to `internal/guard`; 2026-10-10); QL05 done as well (`actions.implement.verify` wired
into the implement step, `verify_calls` in `run.json`, the `summary.md` row; 2026-10-10), written
without running anything, so its gates are the operator's; QL06 is written (the V-verify tests
and D16) but stays open until the live suite has run; QL07 done (`internal/testreport`, the
failing test names in `TestResult` and in the retry prompt; 2026-10-10), also written without
running anything; QL08 written (the no-progress stop: `tree_after`, `workflow.NoProgress`,
`limits.stop_without_progress`, resume into implement; 2026-10-10) without running anything, so
it stays open until its gates have run; QL09 written (the old/new failure split in the retry
prompt, `domain.SplitFailedTests`, the `summary.md` row; 2026-10-10) without running anything,
so it stays open until its gates have run; QL10 written (`actions.*.by_complexity`,
`ActionConfig.Route`, `ProviderRef.ModelSource`, `decision.json: models`, the resume warning;
2026-10-10) without running anything, so it stays open until its gates have run; QL11 done
(`review.fix_medium`, `Run.Options`, `OutcomeFixable`, `outcomeOf` shared by the engine and
resume, the derived outcome reported to the observer; 2026-10-10), `scripts/check.sh` passed;
QL12–QL15 have not started.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 15 implementation tasks |

## The problem in one paragraph

Each stage is a separate cold call, and the orchestrator passes less of each stage's result
to the next one than the stage produced. `implement` cannot run anything, so it learns that it
broke the build only after its session ends, and it gets at most two more cold attempts. The
test command is the only deterministic gate. Review has no criteria to check against and
forgets medium issues. The fix loop spends its iterations even when nothing changes. Every
step runs on one model regardless of how simple the task is. This phase:
- passes the full stage context;
- adds operator-defined checks (vet, lint, SAST) as exit-code gates in the test step;
- lets `implement` run the tests and checks during its session through two fixed tools
  served by the orchestrator itself, without a shell and only in the sandbox, after a guard
  check;
- makes review verify the plan's acceptance criteria;
- stops the loop when it makes no progress;
- lets a static config table pick the model per decided complexity.

## Tasks

| task | title | TODO | size | depends on |
|---|---|---|---|---|
| [QL01](tasks/TASK-QL01-stage-context.md) | stage context in prompts, `acceptance_criteria`, retry diff stat, prompt golden files ✓ | Q1 | S | — |
| [QL02](tasks/TASK-QL02-review-criteria.md) | review verdict per criterion, read-the-surroundings prompt ✓ | Q6.1–2 | S | QL01 |
| [QL03](tasks/TASK-QL03-checks.md) | `checks:` in the test step and the pre-run test ✓ | Q5 | M | — |
| [QL04](tasks/TASK-QL04-verify-server.md) | `internal/verify`: stdio MCP server, `run_tests`, `run_check` ✓ | Q2 | M | QL03 |
| [QL05](tasks/TASK-QL05-verify-wiring.md) | verify in the implement step: config, fail-fast, dispatcher, record ✓ | Q2 | M | QL04 |
| [QL06](tasks/TASK-QL06-verify-live.md) | live security checks for verify, decision D16 | Q2 | S | QL05 |
| [QL07](tasks/TASK-QL07-testreport.md) | `internal/testreport`: failing test names ✓ | Q7.2 | M | — |
| [QL08](tasks/TASK-QL08-no-progress.md) | stop without progress (tree, tests, review) | Q7.1, Q6.4 | M | QL07 |
| [QL09](tasks/TASK-QL09-baseline-names.md) | old vs new failures in the retry prompt | Q7.3 | S | QL03, QL07 |
| [QL10](tasks/TASK-QL10-by-complexity.md) | `by_complexity`: model and effort per decided complexity | Q3 | M | — |
| [QL11](tasks/TASK-QL11-fix-medium.md) | `review.fix_medium` ✓ | Q6.3 | M | QL02 |
| [QL12](tasks/TASK-QL12-extra-reviewers.md) | extra reviewers `actions.review.models` | Q6.5 | M | QL02 |
| [QL13](tasks/TASK-QL13-integration.md) | integration scenarios 33+ | — | M | QL01–QL12 |
| [QL14](tasks/TASK-QL14-documentation.md) | MVP.md §44 and others, CLAUDE.md, README, example config, TODO.md | — | S | QL13 |
| [QL15](tasks/TASK-QL15-bench-configs.md) | benchmark configurations E (verify) and F (by_complexity) | Q2, Q3 | S | QL14, BM16 |

```
prompts  QL01 → QL02 → QL11
                QL02 → QL12
checks   QL03 → QL04 → QL05 → QL06
         QL03 ─────────────────────→ QL09
loop     QL07 → QL08
         QL07 → QL09
routing  QL10
all      QL01…QL12 → QL13 → QL14 → QL15 (also needs BM16)
```

Estimate: about 20–25 engineering days for QL01–QL14, plus QL15 once the benchmark's full run
(BM16) exists. Four tracks can start on day one: QL01, QL03, QL07 and QL10. Recommended order
for one person: QL01 → QL02 → QL03 → QL04 → QL05 → QL06 (the biggest quality lever early, so
the benchmark can measure it) → QL07 → QL08 → QL09 → QL10 → QL11 → QL12 → QL13 → QL14 →
QL15.

## Ground rules for every task

- **Only exit codes decide.** Checks are exit codes. Parsed test output only names tests in a
  prompt. The in-session `run_tests` is a hint; the `test` step is the truth (MVP.md §31.4).
- **No shell for `implement`.** No change adds `Bash` or any built-in CLI tool to an
  authority. The verify tools run a command from config, never from the model, in the
  sandbox, after a guard check (`SPEC.md` §6.9).
- **Go decides, from config and the persisted run.** No model picks a model, a stage, a
  transition or a stop. Every new transition is derivable on resume from `run.json`.
- `internal/workflow` and `internal/policy` import only `domain`. `internal/testreport`
  imports only `domain` and the standard library. `internal/verify` uses the standard library
  for the protocol, with no new module dependency.
- Provider-specific behavior stays in `internal/provider/<name>`.
- Prompts embed repository data through `fence`, bounded by `limits.prompt`. Artifacts and
  logs keep everything.
- Every new `run.json` field is validated strictly on load. Every new config key is declared
  in the schema (strict parsing).
- Tests need no network and no real CLI, except the live suite in QL06
  (`go test -tags security ./test/security/...`).
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...` and an empty
  `gofmt -l .`, or simply `scripts/check.sh`. QL15 also runs the gates in `bench/`.
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add
  an `## As built` section to the task file with the date and anything built differently,
  and why.
