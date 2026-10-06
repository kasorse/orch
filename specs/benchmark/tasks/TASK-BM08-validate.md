# BM08 — `orchbench validate`

**Size** M (1 d) · **TODO** B1 · **Depends on** BM05, BM07 · **Blocks** BM10, BM15

## Goal

Prove each task is a working oracle before money is spent on it; no model is called
(`SPEC.md` §6, §13.4).

## Files

- `bench/internal/validate` + test
- `bench/internal/cache`: `validated.json` (task id → file hash, time)
- `bench/cmd/orchbench`: the `validate` subcommand

## Steps

1. Per task, the checks of §6 in order, stopping at the first failure with a hint:
   schema and not a draft → commits exist, base is an ancestor of fix → workspace at base,
   `setup_command` exits 0, `git status` clean → `test_command` green at base → final-check
   procedure (BM07 steps 1–3) on the unchanged base tree: `oracle_exit ≠ 0` → workspace at
   fix: `oracle.command` green three times, `test_command` green → no oracle file at base
   with the fix's content → questions: patterns match `reference_answer`, none matches the
   bare task text.
2. Output one line per task (`ok` with timings / `FAIL` with check and hint, §13.9) and the
   count; exit 1 if any failed.
3. On success record the task's hash in `validated.json`; on failure remove any old entry.
4. `-j N`: tasks in parallel, output lines in task order.

## Acceptance

- Fixture tasks, one per failure kind of §6 plus a valid code task and a valid question:
  each fails at the expected check with its hint; the valid ones are recorded.
- Editing a recorded task's file makes `run` (BM10) treat it as unvalidated (hash differs).

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-06. Differences from the plan, and why:

- **Check order:** draft → schema → commits → setup → clean → tests-base → oracle-base →
  oracle-fix (×3) → tests-fix → leak. A draft is reported before its schema, because a
  draft from `mine` usually fails both and the draft hint is the useful one. The leak check
  (§6 step 6) runs last, as §6 lists it. A question runs commits, setup, clean and tests-base,
  then the answer checks; the oracle checks are skipped. `merge-base --is-ancestor X X` is
  true, so a question may have base_commit = fix_commit.
- **The fix workspace runs `setup_command` too:** dependencies installed into ignored paths
  are not shared between workspaces. The base workspace is removed before the fix workspace
  is created, unless `--keep-workspaces` is set.
- **Hints** are one line: the §13.9 text, then the exit (`exit 1`, `timed out`) and the
  log, under `<cache-dir>/logs/validate/<id>/`. The `(2 failing)` of the §13.9 sample would
  need per-language output parsing, so it is left out.
- **The recorded hash is of the file as it was read at the start of the check,** so a file
  edited during validation is unvalidated afterwards. An interrupted task is neither recorded
  nor forgotten. Failures of git, bwrap or the disk are `FAIL  error: …`: the task is not
  proven, and its entry is removed.
- **CLI:** a command now has a `run` func; the commands without one stay `not implemented`.
  `validate` checks its prerequisites (`git`, `bwrap`) and probes bwrap once before anything
  is written, and refuses with the AppArmor hint (exit 2). `--tasks` globs expand a leading
  `~`. A glob without a match, an `--ids` entry without a file, two files with one id, and
  `-j` < 1 are usage errors. Tasks run in id order. Output streams in that order with `-j N`;
  `-v` goes to stderr. Exit 1 with `<k> of <m> tasks invalid`. `selectTasks` is meant for
  BM10's `run` too.
- `cache.Validated` (validated.json) re-reads the file and replaces it atomically on every
  change, under a mutex. Like the mirrors, it is safe within one process only.
