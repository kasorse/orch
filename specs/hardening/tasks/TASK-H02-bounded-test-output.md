# H02 — `limits.prompt` and bounded test output

**Size** S (1 d) · **TODO** S1 · **Depends on** — · **Blocks** H03

## Goal

The implement retry prompt embeds at most `limits.prompt.max_test_output_bytes` of the
failing test's output, cut deterministically with the failures kept (`SPEC.md` §5.1, §5.2).

## Files

- `internal/config/config.go`, `validator.go` (+ tests): `LimitsConfig.Prompt`
- `orchestrator.example.yaml`
- `internal/action/bound.go` (new) + `bound_test.go`
- `internal/action/prompts.go`: `BuildImplementPrompt`
- `internal/action/dispatcher.go` / `implement.go`: pass the limit and the log path
- golden/prompt tests in `internal/action`

## Steps

1. Config: `limits.prompt.{max_diff_bytes, max_test_output_bytes, diff_summarize}` with
   defaults 262144 / 32768 / empty; ranges 16 KiB–4 MiB and 4 KiB–1 MiB; each
   `diff_summarize` entry must be a valid `path.Match` pattern (call `path.Match(p, "")` and
   reject `ErrBadPattern`). Declare the keys in the strict schema. `max_diff_bytes` and
   `diff_summarize` are only read by H03; adding them here keeps the section in one place.
2. `BoundTestOutput(stdout, stderr string, budget int) (outOut, outErr string, omitted Omitted)`
   exactly as `SPEC.md` §5.2: shared budget with spill-over, head 20% / failure lines 30% /
   tail rest, line boundaries, UTF-8-safe cut of an over-long line, in-place markers
   `[... L lines, B bytes omitted ...]`. `Omitted{Bytes, Lines int}` per stream for the
   prompt note. Pure function, no config import.
3. `BuildImplementPrompt` gains `MaxTestOutput int` and `TestLogPath string` in its input;
   when anything was omitted it adds one line:
   `The output above was shortened; the full log is <path>.` (path relative to the repo, as
   it is shown elsewhere). Fences stay (`fence` unchanged).
4. The resume path (`restore.go`) rebuilds `LastTestResult` from the artifact; it goes
   through the same builder, so nothing extra — add a test that a resumed implement prompt
   equals the original one for an oversized log.

## Acceptance

- Table tests: both streams under budget (unchanged); one huge stream and one empty (gets the
  whole budget); both huge; failure lines in the middle kept; a single 100 KiB line; multibyte
  text cut on a rune boundary; `budget` exact edge.
- Fuzz test: result is valid UTF-8, `len(out)+len(err) <= budget + markers`, and calling twice
  gives the same bytes.
- Config validation errors for each out-of-range value and a bad glob, with the key named.
- The test artifact and `logs/NNN-test.*.log` still hold the full output.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-05. As planned, with these differences:

- **No log path in the prompt** (step 3). The runs directory is denied to every step's file
  tools (`Dispatcher.denyRead`), so pointing implement at `logs/NNN-test.*.log` would invite a
  read that is always refused. `ImplementPromptInput` gained only `MaxTestOutput`; when
  anything was cut the prompt says how many lines and bytes were left out and what was kept.
  `MaxTestOutput: 0` embeds the output whole (only a direct caller of the builder can pass
  it; the dispatcher always passes `limits.prompt.max_test_output_bytes`).
- **A marker after a line cut short continues that line** (`xxxx[... 94208 bytes omitted
  ...]`) instead of starting a new one, so every byte outside the markers is original output
  and `Omitted.Bytes` is exact. `BoundTestOutput` returns one combined `Omitted` for both
  streams.
- **Two passes for the tail.** The first pass sizes the end with the failure share held back
  and selects failure lines; the second gives the end what the failure lines did not use and
  reselects. With no failure lines the end gets 80% of the share.
- **Byte limits are `*int`** (`PromptLimitsConfig`), read through `DiffBytes()` and
  `TestOutputBytes()`, so an absent key takes the default and an explicit `0` is an error —
  the `security.require_clean_worktree` pattern. `limits.prompt.max_diff_bytes` and
  `diff_summarize` are validated here and first read by H03.
- There were no golden prompt files in `internal/action`; the prompt is covered by assertion
  tests (`bound_test.go`): table cases, `FuzzBoundTestOutput` (ran 45 s, ~880k execs), the
  dispatcher wiring, and a live-versus-restored prompt comparison for a resume.
