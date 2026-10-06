# BM07 — Final check: reset, oracle, suite, answer checks

**Size** S (1 d) · **TODO** B2 · **Depends on** BM05 · **Blocks** BM08, BM10

## Goal

One function that decides a cell's result from exit codes only, and is reused by
`validate` (`SPEC.md` §7.5, §5.3).

## Files

- `bench/internal/check` + test: `Final(ctx, ws, task, mirror) (Result, error)`,
  `Answer(task, answer string, ws) Result`

## Steps

1. **Reset**: every path matching `test_globs` that exists at `base_commit` (`git ls-tree -r
   --name-only <base>` filtered) is restored with `git checkout <base> -- <path>` — modified
   and deleted ones; the count of those that differed is `tests_reset`. Files the model
   added stay.
2. **Oracle files**: `git show <fix>:<path>` written over the tree (directories created).
   The fix commit's objects come from the mirror, never fetched into the workspace before
   this point (it is after the config ran).
3. `oracle.command` sandboxed → `oracle_exit`; `test_command` sandboxed → `suite_exit`;
   both always run; outputs to the cell's `logs/` (`oracle.log`, `suite.log`).
4. `Result{Pass bool, FailReason (oracle|regression|answer|tree_changed), OracleExit,
   SuiteExit, TestsReset, AnswerChecksFailed}` by the table of §7.5.
5. Questions: `Answer` — the tree must be unchanged outside `.orchestrator`
   (`tree_changed`), then every `answer_checks` pattern must match case-insensitively
   (`answer` with the failed patterns listed).

## Acceptance

- Table over a fixture repo: correct fix → pass; no change → `oracle`; correct fix plus a
  broken unrelated function → `regression`; model weakened an existing test → it is reset
  and the regression is caught; model deleted a test → restored; model added a test →
  kept and run; an oracle file that existed at `base_commit` with an old expectation →
  replaced, no false regression.
- Questions: all checks match → pass; one missing → `answer`; tree changed → `tree_changed`.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
