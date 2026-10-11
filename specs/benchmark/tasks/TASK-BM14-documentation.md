# BM14 — Documentation

**Size** S (0.5 d) · **TODO** B1–B3 (documents them; B3 is ticked, B1 and B2 wait for BM15 and BM16) · **Depends on** BM03, BM12 · **Blocks** BM16

## Goal

What was built is documented where users and future work look for it.

## Files

- `bench/README.md` (new): `SPEC.md` §13 as built — prerequisites, the three steps, each
  command with its flags, configs, results directory, recipes, the container
- `MVP.md`: §21 (persistence) — `executions[].usage`, `decision.json` `usage`,
  `run.json` `outcome`; the CLI section — `runs report [--dir]`, `decisions report` as its
  alias; §30 — a sentence that benchmarking lives in `bench/`, outside the core, and never
  selects models
- `CLAUDE.md`: run artifacts (`outcome`, usage), build & test (`cd bench && go test ./...`),
  the black-box rule for `bench/`
- `README.md`: `runs report`; a pointer to `bench/README.md`
- `TODO.md`: tick B3 (and B1/B2 when BM15/BM16 are done), link this spec
- `specs/benchmark/README.md`, `SPEC.md`: status

## Acceptance

- Every command and flag in `bench/README.md` exists in `orchbench --help`, and the reverse.
- The CLAUDE.md artifacts paragraph matches what a run writes (checked against an
  integration run directory).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty (root and `bench/`)

## As built (2026-10-08)

The files were written by an orchestrator run with no shell, so nothing below was run by it.
Two things are left to the operator, and this task is not ticked in `README.md` until they are
done:

1. `scripts/check.sh` outside the sandbox (build, vet, test and `gofmt -l` in the root and in
   `bench/`; the new test below is part of it).
2. The second acceptance item: run the integration suite (or any real run with the fake
   executables of `test/integration`), open one run directory and check the "Run artifacts"
   paragraph of `CLAUDE.md` against its `run.json` and `decision.json` — `outcome` and its
   fields, `executions[].usage`, `decision.json` `usage`. It was written from `internal/domain`
   (`outcome.go`, `trace.go`, `run.go`) and the assertions of scenarios 29, 30 and 32 in
   `test/integration`, not from a run directory.

When both pass: tick BM14 in `README.md`, change its status line to BM01–BM14, and update the
status line of `SPEC.md`.

**Both done (2026-10-11).** `scripts/check.sh` passed outside the sandbox, after rebuilding
`bin/orchestrator` (the `bench/` test that runs `orchestrator validate` uses that binary, and a
stale one rejects the keys of configs E and F). The run directory of a scenario-32 run (the fake
executables of `test/integration`, kept by a throwaway test) matched the "Run artifacts"
paragraph of `CLAUDE.md`: `outcome` with `status`, `fix_iterations`, `tests_passed_final`,
`review_verdict`, `duration_ms`, `active_ms`, `usage` (with `calls`), `decision_overhead` and
`diff`; `executions[].usage`, `provider.authority`, `provider.model_source`, `call`, an
implement's `diff` and `tree_after`; `decision.json` `usage` for `translation`,
`context_depth` and `classification`, and `models`. What that run did not write (`pre_run_test`,
`secret_scan_baseline`, `attempt`, `options`, `partial_calls`, `*.stderr.log`) is
`omitempty` or written only when it has something to record.

- **The first acceptance item is a test, not a one-off script.** `bench/cmd/orchbench/readme_test.go`
  runs `orchbench --help` and `orchbench <command> --help` and compares them with
  `bench/README.md`: the same commands (the `### \`orchbench <name>\`` headings), the same
  common flags, the same flags per command (the first column of each flag table) and each
  command's synopsis as the help prints it. It fails when a flag is added to the code without
  the manual, or the other way round.
- **`bench/README.md` follows the code, not §13, where they differ.** `mine` also prints `kept`
  for a draft that exists and counts `only deleted tests` among the skips; `run` exits 3 on
  `--max-cost` and 1 for `infra_error` cells left; `summarize` always writes the by-category
  breakdown and, unless `--by category`, the `--by` one; groups under three tasks are `insufficient
  data`; `Bmock.yaml` is the B′ config and is not in the default `--configs`. The container
  section is the as-built `run.sh`, with the options `docker run` needs left to `run.sh` and
  `TASK-BM13`.
- **Documented as not built:** `bench/tasks/`, `bench/DECISION.md`, `orchbench export` and
  `summarize` over several sources (BM15, BM17, BM18). The walkthroughs of §13.9–§13.12 were
  not copied; the manual has short samples of the real output shapes instead.
- **`MVP.md`:** §4 lists `runs report [--dir]` and calls `decisions report` the alias that
  prints only the complexity comparison, which is what the code does; §21 lists `usage` of
  decision calls and of steps and the `outcome` fields; §30 got a paragraph after the list, not
  a changed list entry; the package-layout line of `report/` names both reports.
- **`CLAUDE.md`:** the black-box rule is a paragraph under Architecture; the "Run artifacts"
  sentence "`outcome` adds `active_ms` and the net `diff`" now lists all of `outcome`.
- **`TODO.md`:** B3 was already `[x]`; it now links the spec. B1 and B2 stay `[~]`.
