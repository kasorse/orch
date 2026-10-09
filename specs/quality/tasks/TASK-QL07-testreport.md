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
