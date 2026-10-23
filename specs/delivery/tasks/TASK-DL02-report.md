# DL02 — `report.md`

**Size** M (2.5 d) · **Depends on** — · **Blocks** DL04, DL08

## Goal

A reviewer-facing `report.md` next to `summary.md` at every terminal status, rendered by Go from
`run.json` and `decision.json` (`SPEC.md` §5). It is later the MR description and the review note.

## Files

- `internal/domain/call.go`: `StepDiff.RunFiles []FileStat` (`run_files`), ≤ 200, validated (clean
  repository-relative paths, counts ≥ 0, binary without line counts).
- `internal/action` (where `diff.run` is computed, after the guard): fill `RunFiles` from the same
  `--numstat -z` output; no extra git call.
- `internal/report/report.go`: `RunReport(run domain.Run, trace *domain.DecisionTrace) string` —
  sections of §5, empty ones left out; the review form for a run with `review_target` (DL08 adds
  the field; until then only the run form). Bounds of A8, total ≤ 60 000 bytes, lists cut first.
- `internal/report/escape.go`: `mdText(s)` — HTML (`<`, `>`, `&`), `[`, `]`, `!`, `` ` ``,
  `@`, `#`, `~`, `%` before a word character, `!` before a digit, a leading `/` indented. Used for
  every task, ticket and model string.
- `internal/persistence/filesystem.go`: write `report.md` beside `summary.md` (same atomic write,
  same "failing fails nothing").
- `internal/app`: print the path at the end next to the summary's.

## Acceptance

- Golden tests: a completed run with a fix iteration and criteria; a run failed on tests; a run
  with path rules needing approval and a secret finding (no value anywhere); a `--stage` run; a
  report that hits the byte bound.
- Escape tests: `<img src=x>`, `[link](http://x)`, `![i](x)`, `@all`, `#1`, `!2`, `~bug`,
  `/merge` at a line start, a fenced block in the task — none survive as Markdown.
- `internal/report` still imports only `domain` (the existing boundary test).
- `run.json` with `run_files` round-trips; an invalid path is refused on load.

## Gates

`scripts/check.sh`
