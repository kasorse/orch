# BM06 — `orchbench mine`

**Size** M (1.5 d) · **TODO** B1 · **Depends on** BM04 · **Blocks** BM15

## Goal

Draft task files from a repository's history, reading git only (`SPEC.md` §5.4, §13.3).

## Files

- `bench/internal/mine` + test
- `bench/cmd/orchbench`: the `mine` subcommand and its flags

## Steps

1. `git log --no-merges --format=%H <rev>` with `--since`; for each commit `git show
   --numstat --format=` against its single parent.
2. Classify each changed file: test (the language's `test_globs` or `--test-glob`), excluded
   (lock files, `vendor/`, `node_modules/`, generated: `*.pb.go`, `*_generated.*`,
   `*.min.js`, …), code (the rest; documentation files count as neither).
3. Filters of §5.4, each with a counter for the skip summary: one parent; ≥ 1 code and ≥ 1
   test file; tests added or modified, not only deleted; no excluded file; code change within
   `--max-files` / `--max-lines`.
4. Language: `--language` or the majority by extension of the code files.
5. Draft: fields of §5.4's example — `id` `<repo-name>-<short-sha>`, `repo` from `--remote` /
   `origin` / the path, `base_commit` the parent, `fix_commit`, `category` guessed (`fix`,
   `bug` in the subject → `bugfix`, else `feature`), `task` the full commit message,
   `oracle.files` the changed test files, `oracle.command` per language (Go: the packages of
   the test files and the names of the added `Test…` functions; others per §5.5),
   `draft: true`, the `# stat:` comment.
6. Never overwrite an existing file in `--out`; report it as kept.

## Acceptance

- Against a fixture repository built in the test (commits: code+test, code only, test
  deletion only, lock-file bump, too large, merge): exactly the expected drafts and skip
  counts.
- A draft loads with `task.Load` and fails `Validate` only for being a draft.
- A second `mine` over the same `--out` writes nothing new and overwrites nothing.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-07. Differences from the plan, and why:

- **The history:** `git log --format=%H%x00%P [--since=…] <rev>` instead of `--no-merges`,
  and `git diff-tree -r --no-renames -z` (`--name-status`, `--numstat`) against the parent
  instead of `git show`. The NUL-separated forms are safe for any file name, and a rename is
  a delete and an add, so a moved test is not taken for a modified one. A commit without
  exactly one parent counts as `merge`, so the root commit is counted there too.
- **Skip summary:** `skipped: no test change: N, only deleted tests: N, too large: N, lock
  file: N, merge: N`. `only deleted tests` is its own counter (the §13.3 sample has none).
  Each skipped commit counts once, under the first filter in the order merge, no test change,
  only deleted tests, lock file, too large. Before it, one `wrote <file>` or `kept <file>`
  line per draft. A draft is created with `O_EXCL`, so a file that exists is never touched.
- **Documentation** is `*.md`, `*.rst`, `*.txt`, `docs/**`, `README*`, `LICENSE*`,
  `CHANGELOG*`. It counts neither as code nor as test and is listed in the `# stat:` line as
  `other`. Known gap: `*.txt` also catches `CMakeLists.txt` and `requirements.txt`.
- **Language detection** counts every non-excluded, non-documentation file by extension,
  test files included: what a test file is depends on the language. `--language` overrides
  it. No language, or one without defaults and without `--test-glob`, is a usage error
  (`mine.ErrNoLanguage`) before anything is written.
- **The draft** is a local struct in the §5.4 field order, not a `task.Task`, which would
  write every empty optional field. Setup and test commands come from `lang.Lookup`; a Node
  repository with `pnpm-lock.yaml` or `yarn.lock` at the parent gets that tool's setup. The
  timeout is a placeholder: 30m, 60m for C++.
- **`oracle.command`** per §5.5: Go runs the test files' packages with `-run` over the added
  `Test…` functions; Python is pytest on the files; Node is jest, vitest or mocha from
  `scripts.test`, else `npm test`; PHP filters on the added `test…` methods; C++ is `ctest -R`
  over the added suites. A language without a recipe gets an empty command. Paths are not
  quoted. A human reviews every draft.
- **`--since`** must be a date with a year from 1970 to 2099. git ignores a date it cannot
  read, including any year from 2100 on, and would walk the whole history.
- The implementation came from an orchestrator run whose test step ran the core's
  `go test ./...`, which does not include the `bench/` module. The `--since` test failed, and
  was fixed while closing the task.
