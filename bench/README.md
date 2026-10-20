# orchbench

`orchbench` measures the orchestrator against bare Claude Code on replayed commits. It makes
tasks from a repository's history, checks them without a model, runs configurations over them
and reports pass rate, cost and time side by side. It drives `orchestrator` and `claude` as
programs and reads only what they write (`run.json`, `decision.json`, stream-json): `bench/` is
a separate Go module with the path `orchbench` and never imports `orchestrator/internal/...`
(`bench/boundary_test.go` checks it too). It never selects models: it only measures.

This file is the manual of what is built. The design, the reasons and the questions it answers
are in [`specs/benchmark/SPEC.md`](../specs/benchmark/SPEC.md) (§13 is the usage this file
follows); the tasks are in [`specs/benchmark/`](../specs/benchmark/README.md).

Not built yet, so not documented as commands: the 30 public tasks (`bench/tasks/`), the first
full run and `bench/DECISION.md`, and everything for results contributed by other people
(`orchbench export`, `summarize` over several sources, language images; SPEC.md §17).

## Prerequisites and build

```
go build -o bin/orchestrator ./cmd/orchestrator
(cd bench && go build -o ../bin/orchbench ./cmd/orchbench)
```

`orchbench` finds `orchestrator` and `claude` on `PATH`, or takes `--orchestrator <path>` and
`--claude <path>`. Every command checks the programs it needs before it does anything, and
refuses with the list of what is missing (exit 2):

| command | needs |
|---|---|
| `mine` | `git` |
| `validate` | `git`, `bwrap` (a sandbox must be creatable) |
| `run` | `git`, `bwrap`, `orchestrator`, `claude` |
| `summarize` | nothing |

Besides these: the claude CLI must be logged in and inside the version range the orchestrator
verified (MVP.md §41.4; the orchestrator refuses a CLI outside it, `orchbench` only looks that
the program is there), and the toolchains of the task languages must be installed (Go, Python,
Node and so on). Each result line records the orchestrator's build and the claude CLI version,
so results of different builds are never mixed silently.

Model-written code runs on the machine, and config A has a shell. Run the benchmark in the
container (below), not on a developer machine.

## The workflow in three steps

| step | command | calls a model? | costs | how often |
|---|---|---|---|---|
| 1. make tasks | `mine`, then edit the YAML by hand | no | your time | once per task source |
| 2. check tasks | `validate` | no | seconds to minutes per task | after every task edit |
| 3. measure | `run` (a pilot, then the full run), then `summarize` | yes, every cell | money and hours | per orchestrator change worth measuring |

Step 3 is one command: `run` goes through every task × config × repetition by itself.

## Commands

### Common flags

Accepted by every command.

| flag | default | meaning |
|---|---|---|
| `--cache-dir <dir>` | `$XDG_CACHE_HOME/orchbench`, else `~/.cache/orchbench` | mirrors, workspaces, `validated.json` |
| `--orchestrator <path>` | `orchestrator` on `PATH` | the binary under test |
| `--claude <path>` | `claude` on `PATH` | config A, and what the orchestrator's config routes to |
| `-v` | off | print every command `orchbench` runs and its output live (to stderr) |

### Exit codes

The same for every command.

| code | meaning |
|---|---|
| `0` | done |
| `1` | done, but something failed (an invalid task, a cell with `infra_error` left) |
| `2` | usage or a missing prerequisite, nothing ran |
| `3` | stopped by `--max-cost` |
| `130` | interrupted (Ctrl-C) |

`orchbench --help` lists the commands, the common flags and the exit codes;
`orchbench <command> --help` shows the synopsis and the flags of one command.

### `orchbench mine`

Draft tasks from a repository's history. It reads git only: nothing is checked out, no model
is called.

```
orchbench mine --repo <path> --out <dir> [--since <date>] [--rev <rev>]
               [--max-files 10] [--max-lines 400] [--language <name>] [--test-glob <glob>...]
               [--remote <url>]
```

| flag | default | meaning |
|---|---|---|
| `--repo` | required | local clone to read; never changed |
| `--out` | required | where draft YAML files are written, one per candidate |
| `--since` | whole history | only commits after this date (`YYYY-MM-DD`, years 1970 to 2099) |
| `--rev` | `HEAD` | the branch or commit whose history is walked |
| `--max-files` | 10 | size bound on the code change in files, tests not counted |
| `--max-lines` | 400 | size bound on the code change in lines, tests not counted |
| `--language` | from file extensions | the language whose defaults the drafts get (`go`, `python`, `node`, `php`, `cpp`) |
| `--test-glob` | the language's test globs | repeatable; what counts as a test file; required for a language without defaults |
| `--remote` | the clone's `origin` URL, else `--repo` | what goes into `repo:` of the drafts |

Each commit is judged on its own against its single parent. A commit becomes a draft when it
changes code and adds or edits tests, within the size bounds, without a lock file, vendored or
generated file. Output: `wrote <file>` (or `kept <file>` for a draft that already exists and
is left alone, so mining again after editing drafts is safe) per draft, then one line counting
the skipped commits by the first filter that rejected them:

```
skipped: no test change: 140, only deleted tests: 3, too large: 22, lock file: 9, merge: 31
```

A draft has `draft: true`, a guessed `category`, the commit message as `task`, an empty
`expected_stages`, the language's `setup_command` and `test_command`, and the test files of
the commit as `oracle.files` with a guessed `oracle.command`. `validate` and `run` refuse a
draft. What a human does with it (about 10 to 15 minutes): rewrite `task` as a problem
statement, name the interface the hidden tests use, narrow `oracle.command` if needed, fill
`expected_stages`, set `category`, delete `draft: true` (SPEC.md §5.4).

### `orchbench validate`

Check tasks against their repository, without a model.

```
orchbench validate --tasks <glob> [--tasks <glob>...] [--ids a,b] [-j 1] [--keep-workspaces]
```

| flag | default | meaning |
|---|---|---|
| `--tasks` | required, repeatable | task files (quoted glob; a leading `~` is the home directory) |
| `--ids` | all | only these task ids, comma-separated |
| `-j` | 1 | tasks checked in parallel |
| `--keep-workspaces` | off | keep each workspace for inspection; the path is printed |

For each task, in a fresh workspace: the commits exist and `base_commit` is an ancestor of
`fix_commit`; `setup_command` exits 0 and leaves the tree clean; `test_command` is green at
`base_commit`; the oracle is red at `base_commit` and green three times at `fix_commit`, where
the suite is green too; no oracle file is already at `base_commit` with the fix's content;
for a question task, the `answer_checks` match `reference_answer` and not the bare task text
(SPEC.md §6). A valid task's file hash is recorded in `<cache-dir>/validated.json`; `run`
refuses a task whose current hash is not there, so edit, then validate again.

Output: one line per task, `ok` with timings or `FAIL` with the first failed check and a hint,
then `N of M valid`. Exit 1 if any task is invalid. The network is used to mirror the
repository and by `setup_command`; nothing else.

```
go-pgqueue-retry-backoff     ok    setup 14s, tests green, oracle red→green (3/3)
go-pgqueue-lease-expiry      FAIL  oracle green at base_commit: the hidden test checks nothing new
orche-b648329                FAIL  draft: rewrite the task and remove `draft`
1 of 3 valid
```

### `orchbench run`

Run every task × config × repetition and record the results.

```
orchbench run --tasks <glob> [--tasks <glob>...] --out <dir> --max-cost <usd>
              [--configs A,B,C,D] [--reps 3] [--ids a,b] [--limit N] [-j 1]
              [--redo] [--keep-workspaces] [--dry-run] [--allow-mixed]
              [--contributor owner] [--configs-dir <dir>]
```

| flag | default | meaning |
|---|---|---|
| `--tasks` | required, repeatable | task files (quoted glob); each must be valid with the same hash |
| `--out` | required | results directory; if it exists, the run continues it |
| `--max-cost` | required | dollars; no new cell starts once the recorded cost reaches it (there is no unlimited default) |
| `--configs` | `A,B,C,D` | which configs, comma-separated; each name is a file in the configs directory |
| `--reps` | 3 | repetitions per task and config |
| `--ids` | all | only these task ids, comma-separated |
| `--limit` | 0 (all) | only the first N tasks by id, after `--ids` |
| `-j` | 1 | cells in parallel |
| `--redo` | off | run the selected cells again even if they have a result; the new line replaces the old |
| `--keep-workspaces` | off | keep each cell's workspace; the path is in the progress line |
| `--dry-run` | off | print the cells in order and their count, run nothing |
| `--allow-mixed` | off | continue an `--out` whose `manifest.json` names another orchestrator build, CLI version, config or task file |
| `--contributor` | `owner` | a short name (letters, digits, `_`, `.`, `-`) written into `manifest.json` and every result line |
| `--configs-dir` | `./bench/configs`, else `./configs` | the directory of the config files and `common.yaml` |

Everything wrong with the call, the tasks or the configs is found before anything is written or
paid for, and is exit 2: a draft or invalid task, a task not validated with its current hash,
a config that cannot be rendered for a task, a manifest that names another build.

One cell: a fresh workspace without any commit after `base_commit` and without the oracle,
`setup_command`, the config under the task's `timeout`, then the final check (the existing
tests are reset, test files the model added are removed, the oracle files are written, `oracle.command` and `test_command` run
sandboxed; SPEC.md §7.5). A task is solved only when both exit 0; the result comes from exit
codes, never from model text. Question tasks are checked by their `answer_checks` and by the
tree staying unchanged.

Cells are run in an order that interleaves configs per task. Cells already in `results.jsonl`
with `pass` or `fail` are skipped, `infra_error` ones are run again, so the same command
continues an interrupted or partly failed run, and a larger `--reps` or a new `--configs` on
the same `--out` adds only the missing cells. An infra error (setup failed, git, bwrap, the
disk, a crashed runner) is retried once at once in a fresh workspace; if it persists the cell
is left as `infra_error`, its cost counted, and the run goes on. The account's usage limit (`usage_limit_reached`, "You've hit your session limit") is not an attempt: the cell prints when it will run again, waits for the reset the provider reported plus a minute (5 minutes when it reported none), and runs again in a fresh workspace, as often as it takes; the cost of the refused tries is counted. The orchestrator configs set `execution.rate_limit_wait: 0s`, so the orchestrator stops at once instead of waiting inside the task's timeout. A 429, an overloaded API or an empty balance stays an infra error. Ctrl-C stops the running cells, waiting ones included,
without writing their lines. `--max-cost` is checked before each cell starts; running cells
finish, then the run exits 3 and the same command with a higher `--max-cost` continues it.

Output: a header with the number of cells, one progress line per finished cell and a total.

```
[ 1/20] go-pgqueue-retry-backoff  A  rep1  pass   $0.62   3m10s
[ 2/20] go-pgqueue-retry-backoff  B  rep1  pass   $0.91   6m02s  analysis,development,testing
[ 4/20] go-pgqueue-retry-backoff  D  rep1  fail   $0.55   4m20s  oracle: hidden tests exit 1
...
total $18.70 of $30.00, 1h47m0s, 19 results, 1 infra error
```

(The numbers are invented; only the shape is real.)

### `orchbench summarize`

Write the report of a results directory.

```
orchbench summarize <out> [--baseline A] [--seed 1] [--by language] [--stdout]
```

| flag | default | meaning |
|---|---|---|
| `--baseline` | `A` | the config every other one is paired against |
| `--seed` | 1 | the bootstrap's seed, so the same results give the same report |
| `--by` | `language` | what the second breakdown groups by: `language`, `category` or `contributor` |
| `--stdout` | off | print instead of writing `<out>/summary.md` |

It reads `results.jsonl` only and calls nothing, so it can run at any time, also during a run,
on the cells done so far. A missing `<out>` or `results.jsonl`, or a bad `--by`, is exit 2; a
malformed line is exit 1. Without `--stdout` it writes `<out>/summary.md` atomically and prints
`wrote <path> (N cells)`.

The report: the overall table per config (pass rate with min to max over repetitions, cost per
task and per solved task, time, LLM calls, fix iterations, `infra_error` count), failures by
`fail_reason`, a breakdown by category and one by `--by`, the paired comparison of every config
against the baseline (wins, ties, losses per task by majority over repetitions, and the
difference in pass rate with a bootstrap 95% interval), decision overhead, B against C cost,
stage choice against `expected_stages`, complexity, and the translation pairs. A group under
three tasks is shown with its numbers and marked `insufficient data`. The header says how many
cells the report is based on and whether the grid of tasks × configs × reps is complete.

## Benchmark configs

A config is a file named after it in `bench/configs/`; `common.yaml` is shared by all.

| config | what | file |
|---|---|---|
| `A` | bare Claude Code, the baseline: `claude -p` with Read, Grep, Glob, Edit, Write and Bash, the task text and one line naming `test_command` on stdin | `A.yaml` |
| `B` | the orchestrator as the owner runs it (`~/orchestrator_v2.yaml`): `--mode auto`, decision and translation on opus, `by_complexity` routes for analyze, plan, implement and review, implement `escalate` to opus, analysis threshold 0.86. Left out: jira, providers, the graphify extension, `auto_commit`, secret-scan allow paths (see the file's comment) | `B.yaml` |
| `C` | the orchestrator with its defaults: `--mode auto`, everything from `orchestrator.example.yaml` | `C.yaml` |
| `D` | the orchestrator, `development` and `testing` only (`--stage`), C's template otherwise | `D.yaml` |

Every orchestrator config sets `execution.rate_limit_wait: 0s`: the orchestrator stops at once on
a usage limit and orchbench waits for the reset (a test holds it). B against A asks whether the
pipeline as it is used beats bare Claude Code; C against A the same for the defaults; B against C
what the owner's choices change; D against C what the decision and the read-only stages add.
Another setting is measured by editing B and running into a new `--out`, not by a new config;
the config's hash is in every result line, so the two B files stay apart.

```yaml
# bench/configs/common.yaml
model: sonnet                    # the code-writing model: A's --model, implement's model
```

```yaml
# bench/configs/A.yaml
kind: claude
tools: [Read, Grep, Glob, Edit, Write, Bash]
prompt_suffix: "Run the tests with: {{.TestCommand}}"
```

An orchestrator config has `kind: orchestrator`, optional `stages` (each becomes a `--stage`
flag; omitted means the decision chooses) and a `config:` text that is an orchestrator config
template. Placeholders filled per task: `{{.Model}}`, `{{.TestCommand}}`, `{{.RoBinds}}`,
`{{.RwBinds}}` and `{{.Timeout}}`; nothing else, and never the oracle. A file is read strictly
(an unknown key is an error) and its templates are run once at load time, so a typo fails
before a benchmark starts. The config generated for a task is saved as
`<out>/configs/<task>-<config>.yaml` and its hash goes into the result line. To add a config,
add `<name>.yaml` and run `--configs <name>` on the same `--out`.

## Task files

One YAML per task; the file name is the id (`[a-z0-9-]`). `mine` writes the draft; the fields
are in SPEC.md §5.1: `repo`, `base_commit`, `fix_commit`, `category` (`bugfix`, `feature`,
`refactor`, `question`), `language`, `lang` (`en` or `ru`), optional `pair`, `task`,
`expected_stages`, `setup_command`, `test_command`, optional `test_globs`, `oracle` (`files`
taken from `fix_commit`, `command`), `sandbox` (`ro_binds`, `rw_binds`), `timeout`, and for a
question `answer_checks` and `reference_answer`. Defaults per language (`go`, `python`, `node`,
`php`, `cpp`) are in SPEC.md §5.5; any other language must set `test_globs`, `setup_command`
and `test_command` itself. Tasks from private repositories stay outside this repository;
`--tasks` takes any path.

## The results directory

```
<out>/
  manifest.json                 orchestrator build, claude CLI version, config and task hashes
  results.jsonl                 one line per cell (SPEC.md §9); the only input of summarize
  summary.md                    written by summarize
  configs/<task>-<config>.yaml  the orchestrator config each cell actually used
  logs/<task>/<config>/rep<N>/  setup.log, config.stdout, config.stderr, diff.patch,
                                oracle.log, suite.log (answer.txt for a question)
  runs/<task>/<config>/rep<N>/  the orchestrator's run directory (run.json, decision.json, artifacts)
```

Only `summary.md` is committed; `bench/results/*/*` is gitignored except `summary.md`. A
results directory of private tasks lives with them, outside this repository.

Because every orchestrator cell keeps its run directory, the core's own report works on a
whole benchmark, and so does reading a surprising cell's `run.json` by hand:

```
orchestrator runs report --dir <out>/runs
```

## Recipes

| want | do |
|---|---|
| debug one task in one config | `orchbench run --tasks <t> --ids <id> --configs B --reps 1 --max-cost 5 --out /tmp/dbg --keep-workspaces -v`, then look at the kept workspace and `/tmp/dbg/logs/` |
| look at the plan first | the same `run` with `--dry-run` |
| a pilot before the full run | `--ids a,b,c,d,e --reps 1 --max-cost <usd>` on a new `--out`; it shows the cost and time per cell and whether setup and the oracle work |
| more repetitions after the fact | the same `run` with a larger `--reps` and the same `--out`: only the missing reps run |
| a cell looks wrong (setup flaked, a fixed task) | `run --ids <id> --configs <c> --redo` on the same `--out` |
| a new orchestrator build | a new `--out`: an old one refuses without `--allow-mixed`, so two builds are never averaged together |
| progress during a long run | `orchbench summarize <out> --stdout` in another terminal |
| retry what failed for infra reasons | the same `run` command again: `infra_error` cells are run again, others are skipped |
| the budget was reached | the same `run` command with a higher `--max-cost` |
| a claude subscription's limit was reached (`You've hit your session limit`) | nothing: the cells wait for the reset and go on by themselves (a config of your own for the orchestrator needs `execution.rate_limit_wait: 0s`, or the orchestrator waits inside the task's timeout and the cell ends as a `fail` by timeout). Ctrl-C while waiting, and the same `run` command later continues. Results written before 2026-10-12 by an older orchbench recorded A, C and D cells as `fail` with $0: remove those lines from `results.jsonl` and `run --redo --ids <their tasks>` |

## The container

Config A runs a model with a shell, so the whole benchmark runs in a disposable container
(`bench/docker/Dockerfile`), started only through `bench/docker/run.sh`:

```
bench/docker/run.sh build
ANTHROPIC_API_KEY=... bench/docker/run.sh <tasks-dir> <results-dir> orchbench validate --tasks '/tasks/*.yaml'
ANTHROPIC_API_KEY=... bench/docker/run.sh <tasks-dir> <results-dir> orchbench run --tasks '/tasks/*.yaml' --out /results/pilot --max-cost 20
```

`build` builds the image from the committed tree (`git archive HEAD`), so uncommitted edits
never get in, and records the commit in it. The second form runs the command in a fresh
container (default `orchbench --help`). The container gets only `ANTHROPIC_API_KEY` or, for a
claude subscription, `CLAUDE_CODE_OAUTH_TOKEN` (the token `claude setup-token` prints; by name),
`<tasks-dir>` read-only at `/tasks`, `<results-dir>` at `/results` and a named volume
(`ORCHBENCH_CACHE_VOLUME`, default `orchbench-cache`) at `/home/bench/.cache`, where the
mirrors, workspaces and `validated.json` live. Not the host's home, SSH agent or Docker socket;
it runs as the caller's uid, never root, and `run.sh` refuses a tasks or results directory that
is the home directory or contains it. The image includes git, bwrap, Go, Python, Node and the
claude CLI pinned inside the verified range; `orchbench` finds its configs in
`/opt/orche/bench/configs`. The baseline `claude` (config A) gets the operator's environment as
it is, not the orchestrator's `--claude-auth` resolution (MVP.md §46, `specs/claude-auth/`
§13 #20): give both the same environment, so that both bill the same account. The options `docker run` needs for bwrap inside a container, and why
each is there, are in `run.sh` and in
[`TASK-BM13`](../specs/benchmark/tasks/TASK-BM13-container.md).

## Development

```
cd bench && go test ./...
```

or `scripts/check.sh` from the repository root, which runs build, vet, test and `gofmt -l` in
the root module and here. The tests need no network and no real `claude`: `bench/test` runs
the whole pipeline once against a local fixture repository with fake `orchestrator` and
`claude` programs. `bench/cmd/orchbench/readme_test.go` keeps this file and `orchbench --help`
listing the same commands and flags.
