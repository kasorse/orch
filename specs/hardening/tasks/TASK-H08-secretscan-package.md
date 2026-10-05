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

## As built

Done 2026-10-05. API as planned (`Scan`, `ScanLines`, `Redact`, `RuleIDs`, `Finding` with
`String() = "line N (rule)"`), rules as in `SPEC.md` §9.2, with these differences:

- **No `testdata/` golden files.** Realistic tokens (`AKIA…`, `ghp_…`, `glpat-…`, `sk-ant-…`)
  committed to the repository would trip secret scanning and GitHub push protection on this
  very repository. The test secrets are assembled at run time from harmless pieces
  (`"AK" + "IA" + …`) and the expected findings are a table in `scan_test.go`: every rule has
  at least two positives and two near-miss negatives, and `TestScan_EveryRuleHasCases` fails
  if a new rule has none. There is no `-update` flag.
- **One finding per value.** Rules run specific-first, and a value one rule reported is not
  reported again by a later one on the same line: `aws_secret_access_key = "…"` is one
  `aws-secret-key` finding, not also `generic-assignment`; an Anthropic key is not also an
  OpenAI key.
- **Speed.** Each rule runs on a line only when one of its hints occurs in it (`hints`,
  optionally case-folded). `generic-assignment` also has a hand-written shape check
  (`assignmentShape`: keyword, name characters, optional quote, `=` / `:=` / `:`, quote)
  before its pattern — the capturing pattern alone cost ~7 µs per line. 1 MiB of the worst
  ordinary line (several hints, a quoted call) scans in ~25 ms (benchmark
  `BenchmarkScan1MiB`); `FuzzAssignmentShape` (60 s, ~486k execs) checks the shape check never
  rejects a line the pattern matches.
- **Exclusions** beyond the spec: `url-credentials` ignores common documentation passwords
  (`password`, `pass`, `secret`, `test`, `admin`, `root`, `postgres`, …) and placeholders;
  the placeholder words include `redacted`, `your_`, `your-`; env lookups include
  `os.LookupEnv(`, `os.getenv(`, `System.getenv(`, `ENV[`. The JWT rule requires the
  `eyJ….eyJ…` header and payload shape.
- **`Redact` handles a private key as a block**: from `BEGIN … PRIVATE KEY` to its `END`
  line, or to the end of the text when the block is cut off (a bounded diff).
- The value never leaves the package: `Finding` holds no matched text, and a test formats
  findings with `%v`, `%+v`, `%#v` and `%s` and checks no value appears.
