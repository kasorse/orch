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
