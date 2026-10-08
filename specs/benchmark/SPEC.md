# Spec: Benchmark — prove the value (phase 1)

Status: **draft** (2026-10-06). The questions of §14 are answered (2026-10-06, all as
proposed), and so are the usage decisions of §13 (`--max-cost` required, `--allow-mixed`,
`--redo`, `--dry-run`, exit codes). Tasks: [`tasks/`](tasks/) (BM01–BM18, see
[`README.md`](README.md)).
Scope: TODO.md phase 1 (B1–B4). A reproducible task set with a hidden, exit-code oracle; a
runner that drives `orchestrator` and bare Claude Code as black boxes on the same tasks; the
outcome and cost of a run recorded in `run.json` and reported; a go / pivot decision written
from the data; after that, results contributed by people outside the project, on their
own repositories and languages (§17). Touches MVP.md §30 (benchmarking stays outside the core), §21 (persistence:
`outcome`, usage in `run.json` and `decision.json`), the `decisions report` command (becomes
`runs report`).

---

## 1. Problem

TODO.md §0.1: nothing shows that adaptive stage selection gives a better result or a lower
cost than `claude -p "<task>"` in the same repository. Concretely, three hypotheses are
untested:

- **H1 (value).** `orchestrator` with decision-chosen stages solves at least as many tasks
  as bare Claude Code with the same model, at no higher cost.
- **H2 (overhead).** The decision rounds (translate, context depth, classify — up to three
  cold CLI starts before any work) pay for themselves: config B (auto) beats config C (all
  stages) on cost without losing pass rate, and translation changes the decision for Russian
  tasks enough to matter.
- **H3 (decision quality).** The chosen stages match what a human would choose
  (`expected_stages`), and the uncalibrated `policy.*` thresholds are not obviously wrong.

Phase 1 produces the data to accept or reject each and writes the decision down (B4).
It does not tune anything: calibration is G5, model-by-complexity is Q3.

## 2. What already works (checked on master at `8ad5bac`)

- **Unattended runs.** `run --mode auto` asks nothing; without a TTY a provider failure
  under `on_failure: ask` fails the run with instructions instead of prompting
  (`internal/interaction/choose.go`). Exit codes are stable (`internal/exitcode`).
- **Forced stage sets.** `run --stage <s>` (repeatable) gives configs C and D without any
  decision call (`domain.ManualDecision`, `source: manual`).
- **Usage is parsed but not kept.** The claude adapter reads `total_cost_usd`, tokens and
  turns from the stream-json `result` event (`internal/provider/claude/stream.go`) into
  `provider.Result.Usage`; `provider.ReportUsage` hands it to the TUI observer only.
  Neither `run.json` (`domain.StepExecution`) nor `decision.json` (`domain.DecisionTrace`,
  latencies only) records it. B3 has to persist it — the benchmark cannot get cost
  otherwise.
- **Run status.** `run.json` has `status`, `failure_reason`, `fix_iterations`, every test
  step's `test_result.exit_code`, the review `verdict`, timestamps per execution.
- **Run directory inside the clone is invisible to git checks**: every git call excludes
  `:(top).orchestrator` (`internal/repository/git.go`), so a fresh clone stays clean and the
  final diff can be taken with the same pathspec.
- **Test sandbox with cache binds.** `test.sandbox: bwrap` with `ro_binds` / `rw_binds`
  lets a per-language dependency cache outside the clone be visible to tests.
- **`orchestrator decisions report`** (`internal/report`) reads every `decision.json`
  and imports only `domain`; it is the natural base for `runs report`.

## 3. Principles

1. **Black box.** The runner starts `orchestrator` and `claude` as programs and reads only
   what they write (`run.json`, `decision.json`, stream-json on stdout). It never imports
   `orchestrator/internal/...`; this is enforced by the module boundary (§4). The core gets
   no benchmark-only code — B3 adds what an operator also needs.
2. **Exit codes decide** (MVP.md §18). A task is solved when, on the final tree, the hidden
   tests the commit added now pass (fail → pass, `oracle.command`) **and** the repository's
   own suite still passes (pass → pass, `test_command`) — §7.5. No LLM text is parsed for
   success; question tasks use deterministic checks (§5.3, Q3).
3. **The oracle stays hidden.** Oracle files are absent from the tree the model sees, and so
   is every commit after `base_commit` (§7.2). A config that can see the fix is not measured.
4. **Same task, same model, same limits.** Every config gets the same task text, the same
   model for the code-writing call, the same wall-clock budget and the same visible test
   command. Differences between configs are the thing measured, nothing else.
5. **Reproducible.** A result line records every input that can change it: task file hash,
   orchestrator commit, claude CLI version, model, config file hash, repetition number.
6. **Infra failures are not results.** Setup failure, rate limiting, a crashed CLI are
   recorded as `infra_error` and retried; they never count as an unsolved task.
7. **Model-written code is untrusted here too.** The oracle and setup run under the same
   kind of sandbox the orchestrator uses; config A runs a model with a shell (§7.4, Q4).

## 4. Layout

```
bench/                       separate Go module, path "orchbench" (cannot import orchestrator/internal)
  go.mod
  cmd/orchbench/main.go      validate | run | summarize | mine
  internal/...               task loading, workspace, runners, oracle, results, summary
  tasks/<id>.yaml            public tasks (open-source repositories)
  configs/                   orchestrator config templates per benchmark config (B, C, D, ...)
  results/<date>-<label>/    results.jsonl, summary.md, logs/ (gitignored except summary.md)
  DECISION.md                B4
```

- A nested module is excluded from `go test ./...` at the root, so the core's build and test
  stay as they are; `bench/` is built and tested with `cd bench && go test ./...`.
- The module path `orchbench` makes `orchestrator/internal/...` unimportable by Go's
  internal-package rule — the black-box principle is a compile error, not a convention.
- Mirrors of task repositories and workspaces live outside every repository:
  `$XDG_CACHE_HOME/orchbench/{mirrors,work}`.
- Tasks built from private repositories are not committed here (Q1).

## 5. Task set (B1)

### 5.1 Task file

```yaml
id: go-pgqueue-retry-backoff        # unique, [a-z0-9-], equals the file name
repo: https://github.com/org/pgqueue.git
base_commit: 3f2c...                # 40 hex; the tree the model starts from (parent of fix)
fix_commit: 9a1b...                 # 40 hex; the reference solution, never shown
category: bugfix                    # bugfix | feature | refactor | question
language: go                        # go | python | node | php | cpp, or any other name (§5.5)
lang: en                            # en | ru — the language of `task`
pair: go-pgqueue-retry-backoff-ru   # optional: the same task in the other language (Q7)
task: |
  Retries currently use a fixed 1s delay. Make the delay exponential ...
expected_stages: [analysis, development, testing]   # human label, for H3 only
setup_command: "go mod download"    # trusted, runs with network, before any model call
test_command: "go test ./..."       # whole suite: orchestrator's test.command, told to A,
                                    # and the regression check of §7.5 (must stay green)
test_globs: ["**/*_test.go"]        # optional; default per language: what counts as a test file
oracle:
  files: [queue/retry_test.go]      # taken from fix_commit and written over the final tree
  command: "go test ./queue/ -run 'TestRetry'"   # must turn red → green
sandbox:                            # binds for setup/test/oracle, as in test.sandbox
  ro_binds: []
  rw_binds: []
timeout: 30m                        # wall clock for the whole run of one config
```

Changes to TODO.md B1's sketch, and why:

- **`oracle.files` instead of `oracle_patch`.** The oracle files are *copied from
  `fix_commit`* over the final tree rather than applied as a patch. If the model also edited
  an existing test file, a patch would conflict and the task would fail for a reason that is
  not the model's; overwriting the file is deterministic. The model's own edits to those
  files are lost — they were never the measure.
- **`fix_commit`** replaces a stored solution: validation (§6) checks the oracle against it.
- **`test_command` separate from `oracle.command`.** The visible suite is what the
  orchestrator's `test` step and the pre-run test run, and what the final check runs to catch
  regressions; the oracle runs only the hidden tests that must turn green. This is
  SWE-bench's split into FAIL_TO_PASS and PASS_TO_PASS tests.
- **`lang` and `pair`** make the translation measurable on identical tasks (H2).

### 5.2 Composition

30 tasks first, 100 later. At least three languages (Go, Python, TypeScript). At least 20%
`question` tasks (no code change expected — they exercise the read-only branches of the
workflow and the final `answer`). About 20% Russian, as pairs of an English task (Q7).
Categories and sizes spread so that every complexity level is plausible.

### 5.3 Question tasks

A question has no test oracle. Proposed (Q3): `answer_checks`, a list of case-insensitive
regular expressions that must all match the final answer, written together with a reference
answer that validation checks them against:

```yaml
category: question
answer_checks: ['\bretry_test\.go\b', 'exponential|backoff', 'max(imum)?\s+(delay|attempts)']
reference_answer: |
  ...
```

The answer is the orchestrator's final `answer` (printed and stored in the last artifact) or
config A's `result` text. A question task also fails if the tree changed (`git status`
outside `.orchestrator` not empty).

### 5.4 Mining candidates (`orchbench mine`)

A task is a past commit replayed: the model gets the tree *before* the commit and the
problem the commit solved; the tests the commit added decide whether it solved it. Finding
such commits by hand in a long history is most of B1's cost, so `mine` does the search and a
human does the judgement.

**What `mine` does — reads git only, runs nothing, calls no model:**

```
orchbench mine --repo ~/src/pgqueue --since 2025-01-01 --out ~/bench-private/tasks
```

| flag | meaning |
|---|---|
| `--repo <path>` | a local clone to read history from; it is only read, never changed or copied |
| `--since <date>` | look only at commits made after this date (optional); bounds the search, and recent commits are less likely to be in a model's training data |
| `--out <dir>` | where the draft task files go, one small YAML per candidate; no repository is copied there |

Each commit is judged **on its own, against its parent** (`git show <sha>`), never as a
range from `--since` to HEAD. The commit's code change is the reference solution, its test
change the oracle; the draft only points at them by hash. It walks
`git log --no-merges` and keeps a commit when all of these hold:

| filter | why |
|---|---|
| it changes at least one code file **and** at least one test file (the language's `test_globs`, §5.5) | the code change is the solution, the test change is the oracle |
| test files are added or modified, not only deleted | a deleted test checks nothing |
| at most 10 files and 400 changed lines outside tests (`--max-files`, `--max-lines`) | one task, not a release; a model can finish it within `timeout` |
| it has exactly one parent | `base_commit` is unambiguous |
| no lock file, vendored or generated file changes (`go.sum`, `package-lock.json`, `vendor/`, `*.pb.go`, ...) | a dependency bump is not a task, and setup could not reproduce it |

For each kept commit it writes a draft:

```yaml
# ~/bench-private/tasks/orche-b648329.yaml — written by `orchbench mine`
draft: true                        # validate refuses a draft
id: orche-b648329
repo: /home/user/src/orch
base_commit: <parent of b648329>
fix_commit: b648329...
category: feature                  # guess: "fix"/"bug" in the message -> bugfix, else feature
language: go
lang: en
task: |                            # the commit message as is - must be rewritten
  Add per-action effort setting, passed to claude and recorded in run.json
expected_stages: []                # a human fills this
setup_command: "go mod download"   # per-language default
test_command: "go test ./..."      # per-language default
oracle:
  files:                           # the test files the commit touched
    - internal/config/validator_test.go
    - internal/provider/claude/client_test.go
  command: "go test ./internal/config/ ./internal/provider/claude/"   # their packages
# stat: 9 files, +69 -14 (code 6, tests 2, other 1: orchestrator.example.yaml)
```

**What the human does with a draft** (about 10–15 minutes per task, editing the YAML in
`--out`). Nobody writes a solution and nothing compares diffs: the solution already exists
(the commit, written by a developer), and a model's result is judged by running the
commit's tests on it, not by comparing its code with the commit's.

1. **Rewrite `task` as a problem statement**, the way a ticket would put it: what is wrong
   or missing and what is expected, not how to do it. A commit message is either too terse
   ("error output fix") or describes the solution.
2. **Name the interface the oracle uses.** Hidden tests call functions, fields, flags by
   name. If the model names them differently, the oracle does not compile and the task fails
   though the behaviour is right. So a feature task states the names: "a config key
   `effort` per action (`low|medium|high`), passed to claude as `--effort`, recorded as
   `provider.effort` in run.json". A bugfix usually needs nothing — the tests call existing
   code.
3. Narrow `oracle.command` to the tests the commit added (`-run 'TestEffort'`) if the
   package has unrelated slow or flaky tests.
4. Fill `expected_stages`, set `category`, delete `draft: true`. Discard the draft if the
   commit is not a sensible task.
5. Run `orchbench validate` on it (§6). Only `validate` runs code: whether the parent builds,
   the visible tests are green, the oracle is red before and green after.

`mine` overgenerates on purpose; the expected yield is one usable task out of three to five
drafts.

### 5.5 Languages

`language` is an open name. For the languages below `orchbench` has defaults, which `mine`
writes into drafts and a task file may override; for any other language a task must set
`test_globs`, `setup_command` and `test_command` itself, and `mine` needs `--test-glob`.

| language | `test_globs` | `setup_command` | `test_command` | oracle command (as `mine` guesses it) |
|---|---|---|---|---|
| `go` | `**/*_test.go` | `go mod download` | `go test ./...` | `go test <packages> -run '<names>'` |
| `python` | `**/test_*.py`, `**/*_test.py`, `tests/**` | `python -m venv .venv && .venv/bin/pip install -e .[test]` | `.venv/bin/pytest -q` | `.venv/bin/pytest -q <files>` |
| `node` (JS and TS) | `**/*.{test,spec}.{js,jsx,ts,tsx,mjs,cjs}`, `**/__tests__/**`, `test/**` | `npm ci` (or `pnpm i --frozen-lockfile` / `yarn --immutable` by lock file) | `npm test` | from `package.json` `scripts.test`: `npx jest <files>`, `npx vitest run <files>`, `npx mocha <files>` |
| `php` | `tests/**/*Test.php` | `composer install --no-interaction` | `vendor/bin/phpunit` | `vendor/bin/phpunit --filter '<names>'` |
| `cpp` | `**/*_test.{cc,cpp}`, `**/*_unittest.{cc,cpp}`, `test/**`, `tests/**` | `cmake -S . -B build && cmake --build build -j` | `cmake --build build -j && ctest --test-dir build --output-on-failure` | `cmake --build build -j && ctest --test-dir build -R '<names>'` |

Language notes, which `validate` catches when they bite:

- Dependencies and build output must land in ignored paths (`node_modules/`, `vendor/`,
  `.venv/`, `build/`) or the tree is dirty after setup (§6 step 2). Caches in `$HOME` (Go
  modules, npm, composer) are hidden by the sandbox; the task binds them (`sandbox.ro_binds`).
- **C++:** a new test usually needs registering in the build (`tests/CMakeLists.txt`). That
  is why `test/**` and `tests/**` count as test files: the registration comes with the
  oracle. A commit that registers its test in the top-level `CMakeLists.txt` is not a usable
  task unless the task text names it. Build times make C++ tasks the slowest; their `timeout`
  is set accordingly.
- **Node:** `npm test` often runs a linter or a type check too; then it is the regression
  check as written, which is intended.
- Languages whose unit tests live in the same file as the code (Rust's `#[cfg(test)]`) do not
  separate solution from oracle by file; only their tests in a separate directory (`tests/`)
  are usable.

## 6. Task validation (`orchbench validate [ids]`)

`validate` checks the tasks, not the configs: it calls no model and runs no config, only
git, `setup_command`, `test_command` and `oracle.command` against the reference commits. It
is cheap and is re-run whenever a task file changes; `run` refuses a task that has not
passed it with the same file hash (`validate` records each valid task's hash in
`$XDG_CACHE_HOME/orchbench/validated.json`). For each task, in a fresh workspace (§7):

1. `base_commit` and `fix_commit` exist; `base_commit` is an ancestor of `fix_commit`.
2. `setup_command` exits 0 and leaves `git status --porcelain` empty (dependencies must land
   in ignored or bound paths, or the orchestrator would refuse a dirty tree).
3. `test_command` exits 0 at `base_commit` (the pre-run test would refuse a red suite).
4. Oracle files written over `base_commit`: `oracle.command` exits non-zero.
5. At `fix_commit`: `oracle.command` exits 0, three times (a flaky oracle is rejected), and
   `test_command` exits 0 (if the reference solution itself breaks the suite, a model that
   matches it would be counted as a regression).
6. No oracle file is present at `base_commit` with the content of `fix_commit` (the model
   would see the answer).
7. Question tasks: every `answer_checks` pattern compiles and matches `reference_answer`;
   none matches the bare task text (a check the question already satisfies measures nothing).

"Done when" of B1 is `orchbench validate` green on all 30 tasks, plus the human review of
each task text.

## 7. Runner (B2)

### 7.1 One cell

`orchbench run --tasks <glob> --configs A,B,C,D --reps 3 --max-cost <usd> --out
bench/results/<label>` (all flags: §13.5) runs every (task, config, repetition) cell:

1. Workspace (§7.2), `setup_command`.
2. Run the config (§8) with the task's `timeout`; capture its stdout/stderr to `logs/`.
3. Collect: the final diff against `base_commit` excluding `.orchestrator` (files, +/−
   lines), the run directory (orchestrator configs), the stream-json result (config A).
4. Final check (§7.5): reset the test files, write `oracle.files`, run `oracle.command`,
   then `test_command`, both sandboxed (§7.4). Question tasks: the checks of §5.3 instead.
5. Copy the orchestrator's run directory (`.orchestrator/runs/<id>`) to
   `<out>/runs/<task>/<config>/<rep>/`, append one line to `results.jsonl` (§9) and delete
   the workspace (kept with `--keep-workspaces`).

A cell already present in `results.jsonl` with a non-`infra_error` result is skipped, so an
interrupted benchmark continues where it stopped. `--max-cost <usd>` stops launching cells
once the recorded cost reaches it.

### 7.2 Workspace without the future

The repository is mirrored once (`git clone --mirror`), and each workspace is created with
`git init` + `git fetch <mirror> <base_commit>` (via a temporary ref), then checked out
detached at `base_commit`. The workspace therefore has **no commit after `base_commit`,
no `fix_commit` object, no remote**: `git log --all`, `git show <fix>` and `git fetch`
reveal nothing. This matters for config A, which has a shell; the orchestrator's stages have
no shell, but the same workspace is used for every config.

### 7.3 Order and parallelism

Cells run sequentially by default, in an order that interleaves configs per task (A, B, C, D
of task 1, then of task 2, ...) so that a model or rate-limit change during the benchmark
affects every config alike. `-j N` runs N cells in parallel (Q11).

### 7.4 Isolation

`setup_command` runs with network (it downloads dependencies) but before any model call;
`test_command` inside the orchestrator runs under its own bwrap; the **oracle** runs under
bwrap without network, the workspace writable, `$HOME` hidden, the task's binds — the same
policy as `testexec.Sandbox`, reimplemented in `bench` (it cannot import it) and kept small.

Config A runs `claude` with a Bash tool: model-written code executes on the host with the
developer's environment and network. That is what bare Claude Code does, so it is the honest
baseline, but it is not acceptable on a developer machine. Proposed (Q4): the whole
benchmark runs inside a disposable container or VM (claude CLI, bwrap, git, toolchains), with
only the claude credentials passed in.

### 7.5 The final check

A task is solved when the change makes the new tests pass without breaking the old ones. On
the tree the config left behind, in this order:

1. **Reset the existing tests.** Every file matching `test_globs` that exists at
   `base_commit` is restored to its `base_commit` content, and one the model deleted is
   restored. Otherwise a model could make the suite green by weakening or deleting a test.
   Test files the model *added* stay: they are part of its change and must pass too.
2. **Write the oracle.** `oracle.files` are written from `fix_commit` over the tree. This
   also covers an existing test the commit changed on purpose (the old expectation is
   replaced by the commit's, so the intended behaviour change is not taken for a regression).
3. **Fail → pass:** `oracle.command`; its exit code is `oracle_exit`.
4. **Pass → pass:** `test_command`; its exit code is `suite_exit`. It runs even when step 3
   failed, so the report shows both.

| `oracle_exit` | `suite_exit` | `result` | `fail_reason` |
|---|---|---|---|
| 0 | 0 | `pass` | — |
| ≠ 0 | any | `fail` | `oracle` — the task is not solved |
| 0 | ≠ 0 | `fail` | `regression` — solved, but something else broke |

Because `test_command` usually also runs the oracle tests (`go test ./...`), a non-zero
`suite_exit` with a zero `oracle_exit` points at a test outside the oracle. Both commands run
under the sandbox of §7.4 with the task's binds; their outputs go to `logs/`. The cost is one
more suite run per cell, measured in the pilot.

The same reset-and-overwrite runs in `validate` (§6, steps 4 and 5), so a task whose oracle
only works without it is rejected there, not counted against a model.

## 8. Configurations

All configs use the same model for the code-writing call (`implement` / config A), set once
in `bench/configs/common.yaml` (Q5). The orchestrator configs are generated per task from a
template: `test.command = test_command`, `test.sandbox: bwrap` with the task's binds,
`execution.timeout` from the task, everything else from the template.

| config | what | invocation |
|---|---|---|
| **A** bare Claude Code | the main competitor | `claude -p --model <m> --output-format stream-json --verbose --permission-mode acceptEdits --allowed-tools <Read,Grep,Glob,Edit,Write,Bash>` (exact flags verified in the task), prompt on stdin = task text + one line naming `test_command` (Q4) |
| **B** orchestrator auto | the product | `orchestrator run --mode auto "<task>"`, `decision.provider: claude` (the LLM decision provider), policy thresholds from `orchestrator.example.yaml` unchanged |
| **C** all stages | upper bound on the pipeline | `orchestrator run --mode auto --stage research --stage analysis --stage planning --stage development --stage testing --stage review "<task>"` |
| **D** implement + test | the pipeline without planning or review | `orchestrator run --mode auto --stage development --stage testing "<task>"` |
| **B′** auto, mock decision (optional) | decision quality vs a keyword heuristic, zero decision cost | B with `decision.provider: jev`, `decision.jev.mock: true` |

Config E (after Q1/Q2) is added later as another template; nothing in the runner is specific
to A–D except A's invocation.

## 9. Result record

One JSON object per cell in `results.jsonl`:

```json
{"schema": 1, "task": "go-pgqueue-retry-backoff", "task_sha256": "…", "config": "B", "rep": 2,
 "language": "go", "category": "bugfix", "lang": "en", "pair": "go-pgqueue-retry-backoff-ru",
 "contributor": "owner", "started_at": "…", "wall_ms": 412000,
 "result": "pass",            // pass | fail | infra_error
 "fail_reason": "",           // oracle | regression | answer | tree_changed (§7.5, §5.3)
 "oracle_exit": 0, "suite_exit": 0, "tests_reset": 0, "answer_checks_failed": [],
 "run_status": "completed", "failure_reason": "",
 "cost_usd": 1.84, "tokens": {"input": 0, "output": 0, "cache_read": 0, "cache_creation": 0},
 "llm_calls": 6, "decision": {"cost_usd": 0.07, "ms": 21000, "calls": 3},
 "fix_iterations": 1, "review_verdict": "pass",
 "stages": ["analysis","development","testing"], "expected_stages": ["analysis","development","testing"],
 "complexity": "medium",
 "diff": {"files": 3, "added": 54, "deleted": 12},
 "model": "…", "claude_cli": "2.1.290", "orchestrator_commit": "…", "config_sha256": "…"}
```

- `pair` is the task file's `pair` (§5), copied by `run`; absent when the task has none. It is
  what `summarize` reads to find the en/ru pairs (§10).
- Orchestrator configs: cost, tokens, calls, decision overhead, stages, complexity and status
  come from `run.json` `outcome` and `decision.json` (B3). Config A: from its stream-json
  `result` event (`total_cost_usd`, `usage`, `num_turns`); `llm_calls` = 1, no decision.
- `tests_reset` counts the existing test files the model had changed or deleted and the
  final check restored (§7.5 step 1). It is not a failure by itself, but a config that often
  edits existing tests is worth a look in the logs.
- `cost_usd` is the cost the CLI reports (`total_cost_usd`), not a price table: one source
  for every config, no table to maintain. Under a subscription login it is an estimate,
  equally for every config (Q5).

## 10. Summary (`orchbench summarize <dir>` → `summary.md`)

- Per config: pass rate (mean over repetitions, min–max), cost per task (mean, median),
  cost per solved task, wall time, LLM calls, fix iterations, `infra_error` count.
- Per config: failures by `fail_reason` (`oracle` vs `regression`, `answer`,
  `tree_changed`), and the mean `tests_reset`.
- Per config × category and × language.
- **Paired B vs A** (and C vs A, D vs A): per task wins / ties / losses by majority over
  repetitions; difference in pass rate with a bootstrap 95% interval over tasks.
- Decision overhead: share of B's cost and time spent in decision rounds; B vs C cost.
- Stage choice (B, B′): exact match with `expected_stages`, and per-stage
  precision / recall; complexity distribution.
- Translation: for each en/ru pair, whether B chose the same stages and whether both passed.

## 11. Run outcome and report in the orchestrator (B3)

The one change to the core. It is useful without the benchmark: an operator sees what a run
cost and how it ended.

### 11.1 Usage persisted

- `domain.StepExecution` gets `usage` (`domain.Usage` as returned by the provider, omitted
  when the CLI reported none).
- `domain.DecisionTrace` gets `usage` per decision call (translation, context depth,
  classification) next to the existing latencies. Jev reports tokens and no cost; the mock
  reports neither as cost.
- Resume's strict validation (`persistence`) accepts and checks the new fields; old run
  directories without them stay readable.

### 11.2 `outcome`

A pure function in `domain` computes the outcome from `run.json` and the decision trace; it
is written to `run.json` whenever the run reaches a terminal status (and again after a
resume):

```json
"outcome": {
  "status": "completed", "failure_reason": "",
  "fix_iterations": 1, "tests_passed_final": true, "review_verdict": "pass",
  "duration_ms": 412000,
  "usage": {"cost_usd": 1.84, "input_tokens": 0, "output_tokens": 0, "calls": 6},
  "decision_overhead": {"cost_usd": 0.07, "ms": 21000, "calls": 3}
}
```

`tests_passed_final` is the last test step's exit code (absent when the workflow had no
test). `outcome` is derived data: the report recomputes it for runs written before B3 and
for runs that died without a terminal status.

### 11.3 `orchestrator runs report`

`decisions report` becomes `runs report`, with `decisions report` kept as an alias that
prints the existing choice-vs-score section. New sections: runs by status; cost and success
by complexity and by stage set; decision overhead share; the existing comparison. Still
reads only the run directories, calls nothing.

`--dir <path>` reads run directories from anywhere instead of `.orchestrator/runs/` of the
current repository, recursively (any directory holding a `run.json`), so the same report
works over the run directories a benchmark collected (§7.1 step 5).

## 12. Decision (B4)

After the first full run, `bench/DECISION.md` records the numbers and the decision. The rule
is fixed **before** the data (Q9), proposed:

- **B not worse than A**: the lower bound of the 95% interval of (pass rate B − pass rate A)
  is ≥ −5 percentage points, **and** mean cost B ≤ mean cost A → the adaptive pipeline is a
  selling point; next G5 (calibration) and Q3 (complexity → model).
- **B worse than A** → the value is in the gates and governance; the decision layer is
  simplified (default one cheap classification or `--stage`), effort goes to phases 2–3.
- Separately: translation and the context-depth round pay off if removing them (B′ or the
  en/ru pairs) loses no pass rate; otherwise they are turned off by default (Q8).

## 13. Usage

This section is the user's manual of `orchbench`; once built it becomes `bench/README.md`
(kept in sync, like README.md with MVP.md). §13.1–§13.8 are the reference, §13.9–§13.12 a
walkthrough. Order of work: B3 first (the runner needs cost), then B1 and B2 in parallel,
then a pilot, then the full run, then B4. The numbers in the sample outputs are invented;
only their shape is specified.

### 13.1 Build

```
go build -o bin/orchestrator ./cmd/orchestrator
(cd bench && go build -o ../bin/orchbench ./cmd/orchbench)
```

`orchbench` finds `orchestrator` and `claude` on `PATH`, or takes `--orchestrator <path>`
and `--claude <path>`. Each result line records the orchestrator's commit and the claude
CLI version, so results from different builds are never mixed silently.

Prerequisites on the machine (or in the container, Q4): `git`, `bwrap`, the claude CLI in
the verified range (MVP.md §41.4) and logged in, the toolchains of the task languages (Go,
Python, Node). `orchbench` checks all of them before its first cell and refuses with what is
missing — the same fail-fast idea as the orchestrator's.

### 13.2 The workflow in three steps

| step | command | calls a model? | costs | how often |
|---|---|---|---|---|
| 1. make tasks | `mine`, then edit the YAML by hand | no | your time | once per task source |
| 2. check tasks | `validate` | no | seconds–minutes per task | after every task edit |
| 3. measure | `run` (pilot, then full), then `summarize` | yes — every cell | money and hours | per orchestrator change worth measuring |

Step 3 is one command: `run` itself goes through every task × config × repetition; nothing
is started per task or per config by hand.

Common flags, accepted by every command:

| flag | default | meaning |
|---|---|---|
| `--cache-dir <dir>` | `$XDG_CACHE_HOME/orchbench` | mirrors, workspaces, `validated.json` |
| `--orchestrator <path>` | `orchestrator` on `PATH` | the binary under test |
| `--claude <path>` | `claude` on `PATH` | config A, and what the orchestrator's config routes to |
| `-v` | off | print every command `orchbench` runs and its output live |

Exit codes, the same for every command: `0` done; `1` done, but something failed (an invalid
task, a cell with `infra_error` left); `2` usage or a missing prerequisite, nothing ran; `3`
stopped by `--max-cost`; `130` interrupted (Ctrl-C).

### 13.3 `orchbench mine` — draft tasks from history

```
orchbench mine --repo <path> --out <dir> [--since <date>] [--rev <rev>]
               [--max-files 10] [--max-lines 400] [--language <name>] [--test-glob <glob>...]
               [--remote <url>]
```

| flag | default | meaning |
|---|---|---|
| `--repo` | required | local clone to read; never changed |
| `--out` | required | where draft YAML files are written, one per candidate |
| `--since` | whole history | only commits after this date (`YYYY-MM-DD`) |
| `--rev` | `HEAD` | the branch or commit whose history is walked |
| `--max-files`, `--max-lines` | 10, 400 | size bound on the code change (tests not counted) |
| `--language` | from file extensions | the language whose defaults (§5.5) the drafts get |
| `--test-glob` | the language's `test_globs` | repeatable; what counts as a test file — required for a language without defaults |
| `--remote` | the clone's `origin` URL, else `--repo` | what goes into `repo:` of the drafts |

Reads git only (§5.4). A draft whose `id` already exists in `--out` is not overwritten, so
mining the same repository again after editing drafts is safe. Output: one line per draft
written, then the candidates skipped, counted by filter (`no test change: 140, too large: 22,
lock file: 9, merge: 31`).

### 13.4 `orchbench validate` — check tasks, no model

```
orchbench validate --tasks <glob> [--tasks <glob>...] [--ids a,b] [-j 1] [--keep-workspaces]
```

| flag | default | meaning |
|---|---|---|
| `--tasks` | required, repeatable | task files (glob, quoted) |
| `--ids` | all | only these task ids |
| `-j` | 1 | tasks checked in parallel |
| `--keep-workspaces` | off | keep each workspace for inspection; the path is printed |

Runs the checks of §6 for each task and records a valid task's file hash in
`<cache-dir>/validated.json`. Output: one line per task, `ok` with timings or `FAIL` with the
first failed check and a hint (example in §13.9). Exit `1` if any task is invalid. Network is
used to mirror the repository and by `setup_command`; nothing else.

### 13.5 `orchbench run` — measure

```
orchbench run --tasks <glob> [--tasks <glob>...] --out <dir> --max-cost <usd>
              [--configs A,B,C,D] [--reps 3] [--ids a,b] [--limit N] [-j 1]
              [--redo] [--keep-workspaces] [--dry-run] [--allow-mixed]
              [--contributor owner] [--configs-dir <dir>]
```

| flag | default | meaning |
|---|---|---|
| `--tasks` | required, repeatable | task files; each must be valid with the same hash (§6) |
| `--out` | required | results directory (§13.8); if it exists, the run continues it |
| `--max-cost` | required | dollars; no new cell starts once the recorded cost reaches it (there is no unlimited default) |
| `--configs` | `A,B,C,D` | which configs; each name is a file in `bench/configs/` (§13.7) |
| `--reps` | 3 | repetitions per task and config |
| `--ids` | all | only these task ids |
| `--limit` | all | only the first N tasks by id, after `--ids` |
| `-j` | 1 | cells in parallel (Q11) |
| `--redo` | off | run the selected cells again even if they have a result; the new line replaces the old |
| `--keep-workspaces` | off | keep each cell's workspace; the path is in the progress line |
| `--dry-run` | off | print the cells in order and their count, run nothing |
| `--allow-mixed` | off | continue an `--out` whose `manifest.json` names another orchestrator commit or CLI version |
| `--contributor` | `owner` | a short name written into `manifest.json` and every result line (§17); never taken from git or the OS |
| `--configs-dir` | `./bench/configs`, else `./configs` | the directory of the config files and `common.yaml` (§13.7) |

What one cell does is §7.1 and §7.5. Cells already in `results.jsonl` with `pass` or `fail`
are skipped, `infra_error` ones are run again, so the same command continues an interrupted
or partly failed run, and a larger `--reps` or a new `--configs` on the same `--out` adds
only the missing cells. An infra error is retried once at once; if it persists the cell is
left as `infra_error` and the run goes on. Ctrl-C stops the current cell without writing
its line. Output: one progress line per cell (example in §13.10) and a total.

### 13.6 `orchbench summarize` — the report

```
orchbench summarize <out> [--baseline A] [--seed 1] [--by language] [--stdout]
```

| flag | default | meaning |
|---|---|---|
| `<out>` | required | a results directory |
| `--baseline` | `A` | the config every other one is paired against (§10) |
| `--seed` | 1 | the bootstrap's seed, so the same results give the same report |
| `--by` | `language` | what the second breakdown groups by: `language`, `category` or `contributor` (§17.5) |
| `--stdout` | off | print instead of writing `<out>/summary.md` |

Reads `results.jsonl` only, calls nothing; can be run at any time, also during a run, on the
cells done so far. The report's sections are §10; example in §13.11. A missing `<out>` or
`results.jsonl`, or a bad `--by`, is a usage error (exit 2); a malformed line is exit 1.

### 13.7 Benchmark configs (`bench/configs/`)

A config is a file named after it. `A.yaml` is bare Claude Code; every other config is an
orchestrator config template plus the stage flags:

```yaml
# bench/configs/common.yaml — shared by all configs
model: sonnet                    # the code-writing model: A's --model, implement's model
```

```yaml
# bench/configs/A.yaml
kind: claude
tools: [Read, Grep, Glob, Edit, Write, Bash]
prompt_suffix: "Run the tests with: {{.TestCommand}}"
```

```yaml
# bench/configs/D.yaml
kind: orchestrator
stages: [development, testing]   # → --stage flags; omitted = the decision chooses (B)
config: |                        # orchestrator config; {{...}} filled per task
  version: 2
  decision: {provider: claude, model: haiku, ...}
  implement: {provider: claude, model: "{{.Model}}"}
  test:
    command: "{{.TestCommand}}"
    sandbox: bwrap
    ro_binds: {{.RoBinds}}
    rw_binds: {{.RwBinds}}
  execution: {timeout: "{{.Timeout}}"}
```

The config generated for a task is saved as `<out>/configs/<task>-<config>.yaml` and its hash
goes into the result line. Adding config E later is adding `E.yaml` and running with
`--configs E` on the same `--out`.

### 13.8 The results directory

```
<out>/
  manifest.json                 orchestrator commit, claude CLI version, configs and task hashes
  results.jsonl                 one line per cell (§9); the only input of summarize
  summary.md                    written by summarize
  configs/<task>-<config>.yaml  the orchestrator config each cell actually used
  logs/<task>/<config>/rep<N>/  setup.log, config.stdout, config.stderr, oracle.log, suite.log
  runs/<task>/<config>/rep<N>/  the orchestrator's run directory (run.json, decision.json, artifacts)
```

Only `summary.md` (and `DECISION.md` beside the results) is committed; the rest stays local
(`bench/results/*/` is gitignored except `summary.md`). A result directory of private tasks
lives with them, outside this repository (Q1).

### 13.9 Walkthrough: make tasks (B1)

```
orchbench mine --repo ~/src/pgqueue --since 2025-01-01 --out ~/bench-private/tasks
#   wrote 23 drafts to ~/bench-private/tasks
# ... rewrite and keep about 5-8 of them by hand (§5.4) ...
orchbench validate --tasks '~/bench-private/tasks/*.yaml'
```

```
go-pgqueue-retry-backoff     ok    setup 14s, tests green, oracle red→green (3/3)
go-pgqueue-lease-expiry      FAIL  oracle green at base_commit: the hidden test checks nothing new
py-shop-cart-rounding        FAIL  test_command red at base_commit (2 failing): the pre-run test would refuse
ts-ui-date-format            ok    setup 41s, tests green, oracle red→green (3/3)
orche-b648329                FAIL  draft: rewrite the task and remove `draft`
2 of 5 valid
```

### 13.10 Walkthrough: pilot

Pick five tasks across languages and categories, look at the plan, then run it:

```
orchbench run --tasks 'bench/tasks/*.yaml' --tasks '~/bench-private/tasks/*.yaml' \
  --ids go-pgqueue-retry-backoff,py-shop-cart-rounding,ts-ui-date-format,go-orche-effort,py-shop-q-discounts \
  --configs A,B,C,D --reps 1 --max-cost 30 --out bench/results/2026-10-pilot --dry-run
#   20 cells (5 tasks × 4 configs × 1 rep), order: task-major, configs interleaved
orchbench run ...same flags without --dry-run...
```

```
[ 1/20] go-pgqueue-retry-backoff  A  rep1  pass   $0.62   3m10s
[ 2/20] go-pgqueue-retry-backoff  B  rep1  pass   $0.91   6m02s  analysis,development,testing
[ 3/20] go-pgqueue-retry-backoff  C  rep1  pass   $2.40  14m45s
[ 4/20] go-pgqueue-retry-backoff  D  rep1  fail   $0.55   4m20s  oracle: hidden tests exit 1
[ 5/20] py-shop-cart-rounding     A  rep1  infra  —       —      setup: pip install exit 1 (retried next run)
[ 6/20] ts-ui-date-format         A  rep1  fail   $0.48   2m55s  regression: oracle ok, suite exit 1
[ 7/20] ts-ui-date-format         B  rep1  pass   $0.97   6m40s  development,testing
...
total $18.70 of $30.00, 1 h 47 min, 19 results, 1 infra error
```

Interrupting it (Ctrl-C) and starting the same command again continues with the cells
that have no result yet; `infra` cells are retried. The pilot answers three things before
the full run is paid for: cost per cell, wall time per cell, and whether setup and the
oracle work in the container.

### 13.11 Walkthrough: full run and report

```
orchbench run --tasks ... --configs A,B,C,D --reps 3 --max-cost 400 --out bench/results/2026-10-full
orchbench summarize bench/results/2026-10-full      # writes bench/results/2026-10-full/summary.md
```

`summary.md` (excerpt):

```
## Overall (30 tasks × 3 reps)

| config | pass rate      | cost/task mean | cost/solved | time/task | LLM calls | infra |
|--------|----------------|----------------|-------------|-----------|-----------|-------|
| A      | 63% (60–67)    | $0.84          | $1.33       | 4m12s     | 1.0       | 0     |
| B      | 67% (63–70)    | $1.21          | $1.81       | 7m30s     | 5.4       | 1     |
| C      | 70% (67–73)    | $2.95          | $4.21       | 15m02s    | 8.1       | 0     |
| D      | 57% (53–60)    | $0.61          | $1.07       | 4m40s     | 2.6       | 0     |

## Paired: B vs A (per task, majority of 3 reps)
wins 6, ties 21, losses 3; pass-rate difference +4 pp, 95% CI [−6, +13]

## Decision overhead (B)
11% of cost, 19% of time; 3.0 decision calls per run

## Stage choice (B) vs expected_stages
exact match 14/30; development: precision 0.96 recall 1.00; review: precision 0.40 recall 0.75
```

These four tables are what B4 decides on (§12): here the interval's lower bound is −6 pp
(below −5) and B costs more than A, so H1 would not be accepted.

### 13.12 Walkthrough: looking into single runs

Every orchestrator cell keeps its run directory in `<out>/runs/<task>/<config>/<rep>/`, so
the core's own report works on them, and so does reading `run.json` / `decision.json` by
hand for a surprising cell:

```
orchestrator runs report --dir bench/results/2026-10-full/runs
```

```
Runs report: bench/results/2026-10-full/runs
runs: 270 (completed 181, failed 86, stopped 3)

by complexity     runs  completed  cost mean  decision share
simple              96    81%        $0.70        17%
medium             121    66%        $1.35        10%
complex             53    51%        $2.80         6%

by stage set                              runs  completed  cost mean
development,testing                         90    61%        $0.61
analysis,development,testing                71    72%        $1.02
analysis,planning,development,testing,review  ...
```

The same command without `--dir` reports the operator's own runs in the current
repository — B3 is useful outside the benchmark, and those runs are the data G5 calibrates
on later.

### 13.13 Recipes

| want | do |
|---|---|
| debug one task in one config | `orchbench run --tasks <t> --ids <id> --configs B --reps 1 --max-cost 5 --out /tmp/dbg --keep-workspaces -v`, then look at the kept workspace and `/tmp/dbg/logs/` |
| more repetitions after the fact | the same `run` with `--reps 5` and the same `--out`: only reps 4 and 5 run |
| measure a new config (E, after Q1/Q2) | add `bench/configs/E.yaml`, `run --configs E` on the same `--out`, then `summarize` |
| a cell looks wrong (setup flaked, a fixed task) | `run --ids <id> --configs <c> --redo` on the same `--out` |
| a new orchestrator build | a new `--out`: an old one refuses without `--allow-mixed`, so two builds are never averaged together |
| progress during a long run | `orchbench summarize <out> --stdout` in another terminal |
| retry what failed for infra reasons | the same `run` command again: `infra_error` cells are re-run, others skipped |

## 14. Answered questions (2026-10-06)

All accepted as proposed.

| # | question | answer |
|---|---|---|
| Q1 | Where do the benchmark and its tasks live? May tasks come from private (work) repositories, and where are those task files kept? | `bench/` in this repository as a separate module; public tasks in `bench/tasks/`; tasks from private repositories in a private directory outside this repository (`--tasks` accepts any path), never committed here |
| Q2 | Which repositories are task sources, and who selects tasks? | Owner names the repositories (Go: own; Python/TS: 2–3 open-source); `orchbench mine` drafts candidates, the owner approves and rewrites the text |
| Q3 | Oracle for question tasks | Deterministic `answer_checks` (regex, all must match) validated against a reference answer; no LLM judge |
| Q4 | Config A: tools, prompt, isolation | Read/Grep/Glob/Edit/Write/Bash, `acceptEdits`; the prompt names `test_command`; the whole benchmark runs in a disposable container/VM |
| Q5 | Model, authentication, budget | One model for implement and A (sonnet), other stages as in the example config; API key auth; cost from `total_cost_usd`; budget set after the pilot |
| Q6 | Oracle as files from `fix_commit` instead of a patch | Yes (§5.1) |
| Q7 | Russian tasks | As en/ru pairs of the same task (about 6 of 30) |
| Q8 | SWE-bench Verified / Live subset | Not in phase 1 (Python only, heavy environments, training-data leakage) |
| Q9 | B4 decision rule | §12: interval bound −5 pp and cost ≤ A, fixed before the run |
| Q10 | B′ (mock decision) as a fifth config | Yes, it costs no decision calls and isolates the value of the LLM decision |
| Q11 | Parallel cells | Sequential by default, interleaved; `-j N` optional |
| Q12 | Language of the spec | English, like the other specs |
| Q13 | Results from other people | Colleagues and friends outside the company; only an anonymised bundle leaves their machine (§17) |
| Q14 | Their languages | Node.js, Go, C++, PHP (defaults in §5.5, plus Python for the owner's set) |
| Q15 | When | After the owner's full run and B4; only the groundwork of §17.6 is built in B1–B2 |

## 15. Not covered

- Automatic model selection or threshold tuning from the results (MVP.md §30; G5, Q3 are
  later and print proposals only).
- A price table, multi-provider comparison (codex, gemini), other agents (aider, Cursor).
- Measuring review quality beyond the oracle (review issues are recorded, not graded).
- A web dashboard; `summary.md` is the report.

## 16. Tests

- **bench, unit:** task file parsing and validation errors; workspace creation (no object
  after `base_commit` — `git cat-file -e <fix>` fails); the final check (table over §7.5:
  pass, oracle fail, regression; a modified, a deleted and an added test file; an oracle
  file that existed at `base_commit`); result line
  round-trip; summary statistics (paired comparison, bootstrap with a fixed seed) on fixed
  data; resume of an interrupted `results.jsonl`.
- **bench, integration:** a local fixture repository with a known base/fix pair, fake
  `orchestrator` and fake `claude` executables writing canned `run.json` / stream-json; one
  cell per config end to end, no network.
- **core (B3):** `outcome` computation (table: completed, failed in test, review fail,
  stopped, no test step, resumed); usage persisted per execution and per decision call;
  strict validation of the new fields; `runs report` golden output; `decisions report`
  alias unchanged.

## 17. Contributed results (after B4)

Answered 2026-10-06: contributors are colleagues and friends **outside the company**,
working in **Node.js, Go, C++ and PHP**; this starts **after** the owner's own full run and
B4. The owner's set covers Go, Python and TypeScript; contributors add languages and
repositories the owner does not have.

### 17.1 The rule: tasks and code never leave the contributor

A contributor's repositories are often their employer's. So everything that names or shows
them stays on the contributor's machine: task files, the task text, repository names and
URLs, code, logs, run directories, generated configs. What is sent is a **bundle** of numbers
and labels, small enough to read in full before sending.

### 17.2 What a contributor does

1. Gets the **benchmark kit** the owner publishes for a round: an orchestrator release (a
   tag, so every contributor tests the same build), `bench/configs/`, the verified claude CLI
   range, and the container image (§17.4).
2. Makes 10 or more tasks from their own repositories with `mine` → editing →
   `validate` (§5.4, §6), in their language (§5.5).
3. Runs all configs on them: `orchbench run ... --configs A,B,C,D --reps 3 --contributor
   <name> --max-cost <usd>`. They use their own claude account or API key and pay for it;
   the pilot (§13.10) tells them the cost first.
4. Exports and reviews the bundle, then sends it to the owner in any way (a chat, mail, a
   pull request to a private results repository):

```
orchbench export <out> --contributor <name> > bundle-<name>-2026-11.json
```

### 17.3 `orchbench export` — the bundle

Contains `manifest.json`'s build fields (orchestrator commit, claude CLI version, model,
config hashes, contributor name) and, per result line, only:

| kept | how |
|---|---|
| task, `pair` | replaced by `HMAC-SHA256(salt, task id)`, `pair` with the id it names, so a pair stays a pair; the salt is random, created once per contributor in `<cache-dir>/export-salt` and never exported, so the same task maps to the same hash in later bundles but nobody can test guesses of repository or task names |
| `language`, `category`, `lang`, `expected_stages` | as is |
| config, rep, `result`, `fail_reason`, `oracle_exit`, `suite_exit`, `tests_reset` | as is |
| cost, tokens, time, LLM calls, decision overhead, fix iterations, review verdict, stages, complexity | as is |
| diff size | file count and line counts only |

Dropped: `failure_reason` and every other free text (it can quote paths or code),
`task_sha256` (a hash of a file someone else might have), logs, runs, configs. `started_at`
is rounded to the day. `export` prints the bundle to stdout, so what is sent is exactly what the
contributor sees.

### 17.4 Container images

Contributors use macOS as often as Linux, and bwrap is Linux only; config A also needs
isolation (Q4). So the kit includes images: `orchbench-base` (git, bwrap, the claude CLI in
the verified range, `orchestrator`, `orchbench`) and one per language on top of it
(`-go`, `-node`, `-php`, `-cpp`, `-python`). A contributor with an unusual toolchain extends
one with a two-line Dockerfile. Only the claude credentials are passed into the container;
the repositories are mounted read-only and mirrored inside.

### 17.5 Merging: `summarize` over several sources

```
orchbench summarize bench/results/2026-10-full bundles/*.json --by language
```

- Inputs are results directories and bundles alike; the owner's own directory is exported
  the same way first, so all sources look the same.
- **Pairing stays within one contributor and one task**: B against A on the same task, same
  machine, same week. That cancels most differences between contributors (hardware,
  network, task difficulty). A task without the baseline config is left out of the pairing
  and counted.
- `--by language` (default), `--by contributor`, `--by category` choose the grouping; the
  overall table is shown too.
- **No conclusion from thin data**: a language with fewer than 10 tasks (or 2 contributors,
  whichever is stricter, configurable) is listed with its numbers but marked `insufficient
  data` and kept out of the per-language verdict.
- **Different builds are never averaged**: sources with different orchestrator commits are
  reported side by side, one column per build, as `--allow-mixed` would have refused.
- Each contributor's share of the total is shown, so one prolific contributor cannot
  silently decide a language.

### 17.6 What is built now, what later

- **Now, in B1–B2** (cheap, keeps the door open): the open `language` list with the defaults
  of §5.5 for go, python, node, php and cpp; `language`, `category`, `lang` and
  `contributor` in every result line; `summarize --by language` on one directory.
- **After B4**, as a separate task: `export`, `summarize` over several sources, the container
  images, a contributor guide (`bench/CONTRIBUTING.md`, Russian and English), the first
  round with contributors.

### 17.7 Not covered

- A server, an upload endpoint or any automatic collection (TODO.md: no metrics
  infrastructure before data; MVP.md §30: no Web UI or REST API). Bundles are files.
- Telemetry from the orchestrator's everyday use; `runs report` stays local.
- Verifying a contributor's numbers. A bundle is trusted as sent; contributors are people the
  owner knows. An implausible bundle is shown by contributor and can be left out.
