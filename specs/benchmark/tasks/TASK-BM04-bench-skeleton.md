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

## As built

Done 2026-10-06. Differences from the plan, and why:

- **The glob matcher is `task.Match`** (`internal/task/glob.go`), not in `lang`: `task`
  needs it to check `oracle.files` against `test_globs`, and `lang` imports `task`. It
  handles `**/`, `/**`, `**`, `*`, `?` and nested `{a,b}`; an unbalanced brace matches nothing.
- **`task.Defaults` avoids the import cycle.** `task` defines `Defaults` (and
  `Defaults.Apply`, which fills only the empty fields and names those still missing);
  `Task.Validate(d Defaults)` applies them to a copy before the glob check. `lang` holds the
  §5.5 table, `Lookup`, `Resolve(*task.Task)` and `Validate(task.Task)`, the one call for a
  loaded task.
- **A draft is an accessor, not an error:** `Task.IsDraft()`. `Load` and `Validate` accept a
  draft; `validate` and `run` refuse it (BM08, BM10).
- **`Validate` returns `*task.ValidationError`** holding every problem. Checks beyond the
  plan: `timeout` is required and positive, `pair` matches the id pattern and is not the task
  itself, `oracle.files` entries are clean relative paths, `oracle.command` is required for a
  non-question task, and `answer_checks` entries are not empty.
- **The flags of every subcommand are already registered** (§13.3–§13.6, including
  repeatable `--tasks` and `--test-glob`), so `--help` lists them and a bad value is a usage
  error now. Flags may follow the positional `<out>` of `summarize`. A command's needs are
  declared in the command table (`mine` git; `validate` git, bwrap; `run` all four;
  `summarize` none) and tested, but not checked until the commands are built.
- **Help goes to stdout, mistakes to stderr once:** the `flag` package's own output is
  discarded; `--help` prints the synopsis, the flags and the exit codes to stdout and exits 0;
  a bad flag is a `UsageError` printed by `main`. No arguments prints the usage to stderr
  and exits 2.
- **`--cache-dir` default** is `$XDG_CACHE_HOME/orchbench` when that is an absolute path,
  else `~/.cache/orchbench` (`os.UserCacheDir` is not used: it fails instead of falling
  back).
- **Gates were not run in the session that wrote this**, which had no shell; run the four
  gates in `bench/` and at the root first, and `git check-ignore -v bench/results/x/a.jsonl`
  (ignored) and `git check-ignore bench/results/x/summary.md` (not ignored).
- TODO.md B1 and B2 stay open: they finish with BM15 and BM16.
