# T03 — Every call site passes its authority; record and show it

**Phase** 1 · **Size** S–M · **Depends on** T01, T02 · **Blocks** T05

## Goal

All three call sites (`SPEC.md` F2) send the right authority and deny list; each step's
authority is visible before approval and recorded in `run.json` (F10).

## Files

- `internal/action/context.go` — `Dispatcher.call` sets `Authority: domain.AuthorityFor(actionName)`
- `internal/translate/translator.go`, `internal/decision/llm/provider.go` — `AuthorityNone`
- `internal/projectctx/secrets.go` — export `DenyPatterns(extra []string) []string`
- `internal/domain/run.go` — `StepExecution.Authority`
- `internal/workflow` — `StepOutcome.Authority` copied onto the appended `StepExecution`
- `internal/logging` — authority in the workflow block (`Decided`), `--verbose` step line
- tests, including `internal/logging` golden files (`-update`)

## Steps

1. `projectctx.DenyPatterns(extra)` returns `defaultDeny` + extra; the Context Manager uses it
   too, so there is one list. Call sites pass it as `Request.DenyRead` (`config.Context.ExtraDeny`).
2. Translate and decision/llm: `Authority: domain.AuthorityNone`, comment that they are not
   workflow actions and need no tools.
3. `StepOutcome` carries `Authority`; the engine writes it to `StepExecution.Authority`
   (`json:"authority,omitempty"` — `test` has none). `internal/workflow` still imports only
   `domain`.
4. Workflow block printed before approval gains the authority per step, e.g.:
   ```
   Workflow:
     1. analyze    read
     2. implement  edit (repository only, no commands)
     3. test       runs: go test ./...
     4. review     read
   ```
   Printed in every mode (`auto` prints without prompting). Keep MVP.md §27's shape
   otherwise; T08 updates §27.

## Acceptance

- Test doubles reject an empty `Authority`, so a missed call site fails a test.
- Integration: `run.json` has `authority` on every AI step execution, including retries.
- The fake executor sees `--tools ""` for `ACTION: translate` / `ACTION: decide-*` prompts
  and `--tools Read,Grep,Glob` for `ACTION: review`.
- Golden output updated deliberately; no other output change.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
