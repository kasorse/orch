# BM04 — `bench/` module, CLI frame, task file, languages

**Size** M (1.5 d) · **TODO** B1, B2 · **Depends on** — · **Blocks** BM05, BM06, BM09, BM11

## Goal

The frame every other bench task fills: a separate module that cannot import the core, the
`orchbench` command with its common flags and exit codes, the task file with strict
parsing, and the language defaults (`SPEC.md` §4, §5.1, §5.5, §13.1–§13.2).

## Files

- `bench/go.mod` (`module orchbench`, `gopkg.in/yaml.v3` only), `bench/.gitignore`
  (`results/*/` except `summary.md`)
- `bench/cmd/orchbench/main.go`: subcommands `mine | validate | run | summarize` (stubs
  that exit 2 "not implemented" until their task), common flags `--cache-dir`,
  `--orchestrator`, `--claude`, `-v`; exit codes `0/1/2/3/130`
- `bench/internal/task`: `Task` type, `Load(path)`, `Validate()` (schema only: no git, no
  process), `SHA256`
- `bench/internal/lang`: the defaults table of §5.5 (go, python, node, php, cpp) and
  `Resolve(task)` that fills missing `test_globs` / `setup_command` / `test_command`
- `bench/internal/prereq`: checks for `git`, `bwrap`, `orchestrator`, `claude` on `PATH`
  (each command declares which it needs)

## Steps

1. Strict YAML: unknown keys are an error (like the core's version-2 config); `id` equals
   the file name and matches `[a-z0-9-]+`; hashes are 40 hex; `category`, `lang` enums;
   `language` open, but a language without defaults must set `test_globs`, `setup_command`,
   `test_command`; `oracle.files` non-empty and inside `test_globs` for non-question tasks;
   `answer_checks` compile and `reference_answer` present for questions; `draft: true`
   loads but is reported as a draft; `timeout` parses.
2. A test in `bench/` asserts that no package imports a path starting with `orchestrator`
   (belt and braces over the module boundary).
3. Exit codes as a small package with `FromError`, mirroring the core's style.

## Acceptance

- Table tests for every validation error and for the language defaults.
- `go list -deps ./...` in `bench/` contains no `orchestrator/` path.
- `orchbench --help` and each subcommand's `--help` print the synopsis of §13.

## Gates

In `bench/`: `go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty.
Root gates unchanged (the nested module is not part of `./...`).
