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
