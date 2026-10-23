# DL07 — Headless: no terminal, `--output json`, exit codes

**Size** S–M (1.5 d) · **Depends on** DL04 · **Blocks** DL10, DL11

## Goal

A CI caller gets an immediate error instead of an EOF prompt, one JSON line on stdout, and a
documented exit code table (`SPEC.md` §11.1–§11.3).

## Files

- `internal/app/app.go`: for `run`, `resume`, `review`: no `--mode` (or `--mode manual`) and a
  non-terminal stdin → usage error before anything is called (`isTerminal`).
- `--output json`: parsed for the three commands; the reporter forced to the plain one on stderr;
  `internal/app/result.go` builds the result line from the run (or from the error when there is
  none) and writes it to stdout last, once, on every exit path after argument parsing.
- `internal/exitcode`: the table as a doc comment, code 6 (DL04) included.

## Acceptance

- A pipe stdin without `--mode` → exit 2 and the message, no provider call.
- `--output json`: stdout is exactly one valid JSON line for a completed run, a failed run, a
  config error before the run, an MR skip (exit 6), an MR error (exit 6); nothing else on stdout (the test reads both
  streams).
- The field set is stable: a golden of the line with the run ID and sha masked.

## Gates

`scripts/check.sh`
