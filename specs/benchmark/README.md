# Benchmark — prove the value (phase 1)

Status: **in progress** (2026-10-06): BM01–BM05, BM07 done (the core part, TODO.md B3, the bench frame, workspaces and sandbox, the final check). The design questions are answered
(`SPEC.md` §14, Q1–Q15). Covers TODO.md phase 1 (B1–B4) and B5 (contributed results, after
B4).

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification; §13 is the user's manual of `orchbench` |
| [`tasks/`](tasks/) | 18 implementation tasks |

## The problem in one paragraph

Nothing shows that the orchestrator solves tasks better or cheaper than `claude -p "<task>"`
in the same repository. Phase 1 replays past commits as tasks: the model gets the tree before
the commit, the commit's tests — hidden until the end — decide whether it solved it, and the
rest of the suite must stay green. A separate tool, `orchbench`, drives bare Claude Code and
four orchestrator configurations (auto, auto with the mock decision, all stages, implement +
test) as black boxes over the same tasks, repeated, and reports
pass rate, cost and time side by side. The orchestrator itself only learns to record what a
run cost and how it ended (B3). The owner then writes the go / pivot decision from the data
(B4); later, friends add their languages through anonymised bundles (B5).

## Tasks

| task | title | TODO | size | depends on |
|---|---|---|---|---|
| [BM01](tasks/TASK-BM01-persist-usage.md) | persist usage per step and per decision call ✓ | B3 | S | — |
| [BM02](tasks/TASK-BM02-run-outcome.md) | `outcome` in `run.json` ✓ | B3 | S | BM01 |
| [BM03](tasks/TASK-BM03-runs-report.md) | `orchestrator runs report` (`--dir`, alias) ✓ | B3 | M | BM02 |
| [BM04](tasks/TASK-BM04-bench-skeleton.md) | `bench/` module, CLI frame, task file, languages ✓ | B1/B2 | M | — |
| [BM05](tasks/TASK-BM05-workspace-sandbox.md) | mirrors, workspace without the future, sandbox ✓ | B2 | M | BM04 |
| [BM06](tasks/TASK-BM06-mine.md) | `orchbench mine` | B1 | M | BM04 |
| [BM07](tasks/TASK-BM07-final-check.md) | final check: reset, oracle, suite, answer checks ✓ | B2 | S | BM05 |
| [BM08](tasks/TASK-BM08-validate.md) | `orchbench validate` | B1 | M | BM05, BM07 |
| [BM09](tasks/TASK-BM09-config-runners.md) | config runners: A, orchestrator templates | B2 | M | BM02, BM04 |
| [BM10](tasks/TASK-BM10-run.md) | `orchbench run`: cells, results, resume, budget | B2 | M | BM07, BM08, BM09 |
| [BM11](tasks/TASK-BM11-summarize.md) | `orchbench summarize` | B2 | M | BM04 |
| [BM12](tasks/TASK-BM12-integration.md) | end-to-end test with fakes | B2 | M | BM10, BM11 |
| [BM13](tasks/TASK-BM13-container.md) | container image for running the benchmark | B2 | S | BM10 |
| [BM14](tasks/TASK-BM14-documentation.md) | `bench/README.md`, MVP.md, CLAUDE.md, README, TODO.md | — | S | BM03, BM12 |
| [BM15](tasks/TASK-BM15-task-set.md) | the 30 tasks (owner + assistant) | B1 | L | BM06, BM08 |
| [BM16](tasks/TASK-BM16-pilot-and-full-run.md) | pilot, then the full run | B2 | M | BM13, BM14, BM15 |
| [BM17](tasks/TASK-BM17-decision.md) | `bench/DECISION.md` | B4 | S | BM16 |
| [BM18](tasks/TASK-BM18-contributed-results.md) | contributed results: export, merge, images, guide | B5 | L | BM17 |

```
core    BM01 → BM02 → BM03
bench   BM04 → BM05 → BM07 → BM08
        BM04 → BM09            (BM09 also needs BM02)
        BM04 → BM06
        BM04 → BM11
        BM07 + BM08 + BM09 → BM10 → BM12 → BM14   (BM14 also needs BM03)
                             BM10 → BM13
tasks   BM06 + BM08 → BM15
runs    BM13 + BM14 + BM15 → BM16 → BM17 → BM18 (B5, later)
```

Estimate: about 17 engineering days for BM01–BM14, plus the task set (BM15: about a week of
the owner's time, in parallel) and the runs (BM16: mostly waiting, plus the money). Two
tracks can go in parallel from day one: the core (BM01 → BM03) and the bench frame
(BM04 → BM05 → BM07). Recommended order for one person: BM01 → BM02 → BM04 → BM05 → BM07
→ BM06 → BM08 (BM15 starts here) → BM09 → BM10 → BM11 → BM12 → BM03 → BM13 → BM14 →
BM16 → BM17.

## Ground rules for every task

- **Black box.** `bench/` is a separate module with path `orchbench`; it never imports
  `orchestrator/...` and reads only what the orchestrator writes (`run.json`,
  `decision.json`). Dependencies: the standard library and `gopkg.in/yaml.v3`, like the core.
- **The core changes only in BM01–BM03**, and only in ways an operator needs without the
  benchmark. `internal/workflow` and `internal/policy` import nothing new; `internal/report`
  imports only `domain`.
- **Exit codes decide.** A task's result comes from the oracle's and the suite's exit codes,
  never from model text.
- **Nothing private is committed.** No task from a private repository, no result directory
  except `summary.md`, no token-shaped literal (CLAUDE.md).
- **Tests need no network and no real CLI** — fakes, like `test/integration`. The one
  exception is BM09's check of config A's flags against the installed `claude`.
- Core tasks end green on `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`
  empty. Bench tasks also on the same four run in `bench/`.
- When a task is done: tick it here and in TODO.md, and add an "As built" section to the task
  describing anything built differently and why.
