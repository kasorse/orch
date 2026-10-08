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

## As built

Done 2026-10-06. Differences from the plan, and why:

- **Signatures:** `Final(ctx, ws, task, logDir) (Result, error)`. The mirror is `ws.Mirror`
  and the cell's `logs/` directory is known only to the caller. `Answer(ctx, task, answer,
  ws) (Result, error)`, because `git status` can fail. Only infrastructure failures (git,
  bwrap, the disk) are errors, which BM10 maps to `infra_error`. A red or timed-out command is
  a `Result`; a timeout is exit -1 and noted in the log.
- **The steps are exported for `validate`:** `ResetTests`, `WriteOracle`, `OracleEntries`,
  `RunCommand`, `Sandboxed` (any directory, with or without network: setup), `Decide` (the
  table of §7.5) and `MatchChecks`.
- **The reset does not use `git checkout`.** It compares every base_commit test file with
  the tree: content, executable bit, symlink target, ancestors that are real directories.
  Each one that differs is rewritten from the mirror's blob. So `tests_reset` does not depend
  on the workspace's index or commits (a committed weakening and a staged deletion count). No
  smudge filter or hook from the workspace's `.git` runs, and a symlink the model put in place
  of a test file or directory is replaced, never written through.
- **The oracle files** come from the mirror the same way. A file missing at fix_commit is a
  `*MissingOracleFileError`, reported by `validate`. The workspace never gets the fix
  commit's objects.
- Logs: `oracle.log` and `suite.log` hold the command, the combined output and `# exit N
  after D` (or `# timed out after D`).
- A question reports both exit codes as -1: neither command runs.
