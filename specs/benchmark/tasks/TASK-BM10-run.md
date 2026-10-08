# BM10 — `orchbench run`: cells, results, resume, budget

**Size** M (2 d) · **TODO** B2 · **Depends on** BM07, BM08, BM09 · **Blocks** BM12, BM13

## Goal

One command that runs every task × config × repetition, continues where it stopped, and
never spends more than it was allowed (`SPEC.md` §7.1, §7.3, §9, §13.5, §13.8).

## Files

- `bench/internal/results` + test: result line type (schema 1, all fields of §9 incl.
  `language`, `category`, `lang`, `contributor`), append-only writer, reader, manifest
- `bench/internal/run` + test: planning, scheduling, the cell
- `bench/cmd/orchbench`: the `run` subcommand and its flags

## Steps

1. Load tasks (`--tasks`, `--ids`, `--limit`), refuse any whose hash is not in
   `validated.json`; load configs; `--max-cost` required.
2. Manifest: create `<out>/manifest.json` (the `orchestrator version` output and the
   binary's sha256 — `version` may not name a commit until TODO P9 —, the claude CLI version, configs and their hashes, contributor) or compare with the existing
   one; a different build or CLI → refuse unless `--allow-mixed`.
3. Plan: cells in task order, configs interleaved per task, reps outermost per task
   (`task1: A1 B1 C1 D1 A2 …`); skip cells with a `pass`/`fail` line unless `--redo`;
   `--dry-run` prints the plan and the count.
4. Cell: workspace (BM05) → setup → runner (BM09) → final check (BM07) or answer check →
   copy the run directory to `<out>/runs/<task>/<config>/rep<N>/` → logs → result line →
   remove the workspace (unless `--keep-workspaces`). An infra error is retried once at once,
   then written as `infra_error`.
5. Budget: before each cell, the sum of `cost_usd` in `results.jsonl` (this invocation and
   earlier) is compared with `--max-cost`; at or above it no new cell starts, running ones
   finish, exit 3.
6. `-j N` with a worker pool; the writer serialises lines; order of lines may differ from
   the plan, the reader does not care.
7. Ctrl-C: cancel the context, kill the cells' process groups, write no line for unfinished
   cells, exit 130.
8. Progress line per cell (§13.10) and the total.

## Acceptance

- Planning: order, skip, `--redo`, larger `--reps`, new config on an existing `--out`.
- Manifest mismatch refused; `--allow-mixed` accepted and recorded.
- Budget stop at the right cell with exit 3; a second invocation with a higher limit
  continues.
- Interrupt mid-cell leaves no partial line; the next run redoes that cell.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Written 2026-10-08. The four gates above pass: `scripts/check.sh` at the repository root, which
runs them in `bench/`, ended with exit 0. BM10 is ticked in `README.md` and its status line reads
BM01–BM11. `TODO.md` B2 stays `[~]`: it also covers BM12, BM13 and BM16. Differences from the
plan, and why:

- **`--configs-dir <dir>` is a new flag** (SPEC §13.5 lists it now). The plan said only "a file in
  `bench/configs/`"; a command run from another directory, and the tests, need a way to name it.
  Without the flag the first of `./bench/configs` and `./configs` that holds a `common.yaml` is
  used, else exit 2 naming both.
- **Everything wrong with the call is exit 2, before anything is written or paid for:** a missing
  `--out`, a `--max-cost` that is not a positive finite number, `--reps` or `-j` below 1, a
  negative `--limit`, a `--contributor` that is not a short `[A-Za-z0-9][A-Za-z0-9_.-]*` name, an
  empty or repeated `--configs` entry, a draft, an invalid task, a task whose file hash is not in
  `validated.json` (all such tasks are named in one error), a config that does not exist, and a
  manifest that names another build.
- **A pre-flight renders every config for every task** (`RenderConfig`, or `RenderPrompt` for A),
  and refuses task text that starts with `-` for an orchestrator config, which the orchestrator
  would read as a flag. A pairing that could never run fails now, not after the cells before it
  were paid for.
- **`--dry-run` runs before the prerequisite check** and before the manifest, so it needs no
  program and creates nothing, `--out` included. A call with no cells left prints the count and
  `nothing to run`, and also touches nothing.
- **The budget counts every line in `results.jsonl`, replaced ones too** (`Records.CostUSD`):
  money spent on a cell that `--redo` runs again is still spent. The sum is read once at the
  start; each appended line's cost is added in memory under a mutex, and checked before each cell
  is handed out. A cell that is running when the limit is crossed finishes and is recorded, so
  the total may end above `--max-cost` by the cost of up to `-j` − 1 cells plus the one that
  crossed it. Exit 3 is an `exitcode.BudgetError`; a plan that completes with the limit exactly
  reached is exit 0.
- **A manifest mismatch** is a different `orchestrator version` output, orchestrator binary
  SHA-256, claude CLI version or contributor, or a different hash for a config or task the
  manifest already lists. A config or task the manifest does not know is new, not a mismatch: it
  is merged in, which is how a new config on an existing `--out` works. `--allow-mixed` continues,
  saves the current build and CLI, and appends an entry with the differences to `mixed` in
  `manifest.json`. The manifest is written before the first cell, through a temporary file and a
  rename.
- **`OpenWriter` cuts a half-written last line.** A crash can leave `results.jsonl` without a
  final newline; `Read` tolerates that only while it is the last line, so the next appended line
  would have turned it into a parse error. The file is truncated back to its last newline when it
  is opened. A line is one `Write` of the whole line with `O_APPEND` under a mutex, then `Sync`.
- **Every failure that says nothing about the model is `infra_error`**, not only
  `runner.IsInfra`: a failed workspace, a `setup_command` that exits non-zero (`setup: exit N`),
  a failed check, diff or copy, and a runner error that is not an `*InfraError`. Each is retried
  once at once in a fresh workspace; the cost of the failed attempts is added to the line, so
  the budget sees it. A cancelled context returns its error and no line.
- **Per-cell files:** `logs/<task>/<config>/rep<N>/` holds `setup.log`, `config.stdout`,
  `config.stderr`, `diff.patch`, `oracle.log` and `suite.log` (and `answer.txt` for a question),
  and `runs/<task>/<config>/rep<N>/` the copy of the orchestrator's run directory (symlinks are
  copied as links, never followed). Both are removed first, so a cell run again never mixes with
  its earlier try. The diff is taken before the final check, which rewrites test files.
- **Ctrl-C kills the cells' processes through the context, not in `run`.** `Run` only cancels the
  context its cells run under. `runner.Run` (the `claude` and `orchestrator` processes) and
  `sandbox` (`setup_command`, oracle, suite) start every child in its own process group and
  kill the whole group on cancel; `TestClaudeTimeoutIsAResultAndKillsTheProcessGroup`,
  `TestClaudeCancelledByTheCaller`, `TestOrchestratorCancelledByTheCaller`,
  `TestTimeoutKillsTheProcessGroup` and `TestCancelStopsTheCommand` cover that in their packages.
  The `run` tests check only that a cancelled cell leaves no line; none of them starts a real
  child and checks that it is gone.
- **Exit codes of `run`:** 0 done, 1 cells left as `infra_error` (or a results line that could
  not be written), 2 usage, 3 budget, 130 interrupt. Progress lines count in completion order,
  because with `-j` cells finish out of plan order.
- **Tests:** `internal/results` (writer from many goroutines, partial-line cut, manifest
  round-trip, `Compare`, `Merge`), `internal/run` (plan order, skip, `--redo`, larger `--reps`,
  new config, budget stop and continuation, interrupt with no partial line, `-j`, infra errors,
  progress lines; the cell with real git and a fake runner, skipped where `bwrap` cannot run),
  and `cmd/orchbench/run_test.go` (usage errors, unvalidated and edited tasks, `--limit`,
  `--dry-run`, manifest refused and `--allow-mixed` recorded, budget exit 3 and a resumed run).
  The cell executor and the program probes are package variables there, so no model runs.
