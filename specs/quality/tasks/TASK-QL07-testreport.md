# QL07 — `internal/testreport`: failing test names

**Size** M (2 d) · **TODO** Q7.2 · **Depends on** — · **Blocks** QL08, QL09

## Goal

Extract failing test names from common runners' output so a retry prompt can name them, and
so the progress check (QL08) can compare failures by name. Parsing never decides pass/fail
(`SPEC.md` §8.2).

## Files

- `internal/testreport/report.go`: `Parse(stdout, stderr string) Report{Format string;
  Failed []string}`
- `internal/testreport/{gojson,gotext,pytest,jest}.go`
- `internal/testreport/testdata/*`: real outputs (passing, failing, truncated, interleaved)
- `internal/domain/artifact.go`: `TestResult.FailedTests`, `TestResult.ReportFormat`
- `internal/action/testaction.go`: fill them after the run (test step and pre-run test)
- `internal/action/prompts.go`: `Failing tests: …` above the fenced output
- `internal/persistence/validate.go`

## Steps

1. One parser per format, tried in a fixed order: go JSON, jest JSON, pytest summary, go
   text, jest text. The first that recognises its format wins. Each parser is pure and
   tolerant: unparseable lines are skipped, never an error.
2. Names are de-duplicated, sorted, at most 200, each at most 300 bytes. Go names are
   `pkg.TestName` (subtests `pkg.TestName/sub`). Pytest names are `path::name`. Jest names
   are `file › suite › name`.
3. The package imports only `domain` (if at all) and the standard library. Add a test that
   fails if it imports anything else (parse the package's imports with `go/parser`).
4. Fill `FailedTests` and `ReportFormat` only for a failed run. They are absent for a pass.
5. Retry prompt: `Failing tests (parsed from the output): a, b, c` with at most 30 names and
   `… and N more`.

## Acceptance

- Golden tests per format: failures found, passes ignored, truncated output still yields the
  names before the cut, and an unknown format gives `Format == ""` with no names.
- A fuzz test: `Parse` never panics and returns at most 200 names.
- A green test result has no `failed_tests`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-10)

Done as planned, except:

- **Gates:** the change was written without being able to run anything, so the four gates
  (`scripts/check.sh`) are for the operator's run to confirm. The testdata files are
  hand-written samples in the shape of each runner's output, not captures, and the `.golden`
  files were written by hand from the parsers: if one differs, read the diff before changing
  either side. There is no `-update` flag for them.
- **Layout:** `testdata/<name>.txt` is the input and `<name>.golden` the expectation (the
  format on the first line, then one name per line), for each format in the cases passing,
  failing, truncated and interleaved, plus `unknown`. `FuzzParse` is seeded from the same
  files and from cuts of them.
- **Formats:** `gojson`, `jestjson`, `pytest`, `gotext`, `jesttext` (`testreport.Format*`,
  `KnownFormat`). jest's two parsers share `jest.go`. Pytest reads `FAILED` lines only, not
  `ERROR`, as the spec says; the text after ` - ` is cut, except inside `[...]`.
- **Recognised, no names:** a format that is recognised but lists no failure (a green run, a
  build error) gives `Format != ""` and no names. `guardedTest` records that format only for a
  failed run.
- **Truncation:** go JSON and the text formats keep every name before the cut. `jest --json` is
  read as a token stream, so a cut keeps the files, and the tests of the file being cut that
  were complete. jest writes a file's name after its assertions, so those last tests lose the
  file prefix.
- **jest text:** the `✕` lines of a file's tree carry the failures, with the describe blocks
  taken from the indentation. A file with no tree (jest prints one only for a single file or
  `--verbose`) uses its `● Suite › name` headings. The `✕` after the first failure detail is
  ignored as output of the code under test.
- **Names:** a name is one line (control characters become spaces), cut to 300 bytes on a
  rune boundary. Sorting is bytewise. The parent of a failed go subtest is listed with it.
- **Persistence:** `validateReport` runs on `pre_run_test` and every `test_result`: only on a
  failed result, a known format, at most 200 names, each 1 to 300 bytes with no control
  character. It checks the line break too because the names reach a prompt outside a fence.
- **Retry prompt:** `Failing tests (parsed from the output): a, b, c`, at most 30 names, then
  `, … and N more`. It is written by `writeTestOutput`, so the pre-run failure shown to the
  first implement has it too. The prompt golden files did not change: their test results have
  no names.
- **Not done here:** the progress check (QL08) and the old-vs-new split (QL09) that use the
  names; `summary.md` does not show them yet.
