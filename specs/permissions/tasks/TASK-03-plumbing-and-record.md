# T03 — Every call site passes its authority; record and show it

**Phase** 1 · **Size** S–M · **Depends on** T01, T02 · **Blocks** T05

## Goal

All three call sites (`SPEC.md` F2) send the right authority and deny list; each step's
authority is visible before approval and recorded in `run.json` (F10).

## Files

- `internal/action/context.go` — `DenyRead` (the `Authority` field is set since T02)
- `internal/projectctx/secrets.go` — export `DenyPatterns(extra []string) []string`
- `internal/domain/run.go` — `ProviderRef.Authority`
- `internal/logging` — `authority:` line after `workflow:`, `--verbose` step line
- `internal/testutil/fakeexec` — `FAKE_ARGS_DIR` records each call's argv
- tests (the decision golden files are unchanged: the authority line has its own renderer)

## Steps

1. `projectctx.DenyPatterns(extra)` returns `defaultDeny` + extra; the Context Manager uses it
   too, so there is one list. Call sites pass it as `Request.DenyRead` (`config.Context.ExtraDeny`).
2. ~~Translate and decision/llm: `Authority: domain.AuthorityNone`~~ — done in T02.
3. As built: the authority goes on `domain.ProviderRef` (`provider.authority` in run.json),
   which `Dispatcher.call` already attaches to every outcome, failures included — no new
   field threaded through `internal/workflow`, and `test` (no provider) naturally has none.
4. As built: one line after the existing `workflow:` line, printed in every mode before any
   prompt, instead of reshaping the workflow line into a block:
   ```
   workflow: analyze → implement → test → review
   authority: analyze=read, implement=edit, review=read; test runs: go test ./...
   ```
   `--verbose` adds `Authority: <a>` under each AI step's header. Translator and decision
   calls get no `DenyRead`: with no file tools there is nothing to deny.

## Acceptance

- (done in T02) Test doubles reject an empty `Authority`, so a missed call site fails a test.
- Integration: `run.json` has `provider.authority` on every AI step execution.
- The fake executor sees `--tools ""` for `ACTION: decide-*` prompts, `--tools
  Read,Grep,Glob` plus the deny rules for analyze and review, the edit tools for implement,
  and the prompt last after `--model` (`TestDecision_LLMProvider`).
- No other output change.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
