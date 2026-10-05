# H08 — `internal/secretscan`

**Size** M (1.5 d) · **TODO** S5 · **Depends on** — · **Blocks** H09

## Goal

A pure, standard-library-only scanner that finds likely secrets in text and reports rule, line
and a stable fingerprint — never the value (`SPEC.md` §9.2).

## Files

- `internal/secretscan/scan.go`, `rules.go`, `entropy.go`
- `internal/secretscan/scan_test.go`, `testdata/positive/*`, `testdata/negative/*`,
  `testdata/*.golden` (`-update` flag like `internal/jira`)

## Steps

1. API:
   ```go
   type Finding struct {
       Rule        string
       Line        int    // 1-based, in the content passed in
       Fingerprint string // hex sha256(rule \0 path \0 hex(sha256(match)))
   }
   func Scan(path string, content []byte) (findings []Finding, skipped bool)
   func ScanLines(path string, lines []NumberedLine) []Finding // for diff "+" lines
   func Redact(text string) string // replaces every match with [REDACTED:<rule>]
   func RuleIDs() []string
   ```
   `skipped` = binary (NUL in first 8 KiB) or > 1 MiB.
2. Rules from `SPEC.md` §9.2, compiled once, each with an ID. Order matters only for
   overlapping matches: report each (line, rule) once.
3. `generic-assignment`: regex for `name <op> "value"`/`'value'`/`` `value` `` where the name
   contains one of the keywords (case-insensitive); value ≥ 16 chars, Shannon entropy ≥ 3.5,
   not a placeholder, not an env lookup on the same line.
4. Exclusions: AWS documentation example values; placeholder rules.
5. `Redact` uses the same rules and exclusions so a value reported is a value redacted.
6. Golden tests: every rule has ≥ 2 positives and ≥ 2 near-miss negatives (e.g. `AKIA` + 15
   chars, `sk-ant-` + 5 chars, a low-entropy password literal, `password: ${DB_PASSWORD}`,
   `token = os.Getenv("TOKEN")`). The golden file lists `file:line rule` only.
7. Fingerprint test: the same secret moved 10 lines down keeps its fingerprint; the same value
   in a different file gets a different one.

## Acceptance

- Golden tests green; no finding's `String()`/`%v` contains the matched value (test with a
  sentinel secret and `fmt.Sprintf("%+v", findings)`).
- `Redact` removes every positive from `testdata/positive/*` and changes nothing in
  `testdata/negative/*`.
- Benchmark: 1 MiB of Go source scans in < 50 ms on a laptop (guards against catastrophic
  regex).
- `go list -deps ./internal/secretscan` shows standard library only.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
