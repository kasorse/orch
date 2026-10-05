# H01 — Prompt on stdin

**Size** S (0.5–1 d) · **TODO** S1 · **Depends on** — · **Blocks** H06, H10

## Goal

Every claude call gets its prompt on stdin, never in argv, so no prompt can hit Linux's
128 KiB per-argument limit (`SPEC.md` §4).

## Files

- `internal/provider/claude/client.go` (+ `client_test.go`): `args`, `buildArgs`,
  `redactedArgs`, `Execute`
- `internal/apperr` (ProviderError rendering, if it prints argv)
- `internal/testutil/fakeexec/harness.go`: `ProviderScript`
- integration tests that assert `FAKE_ARGS_DIR` content with a `<prompt>` line
- `specs/permissions/DECISIONS.md` (record of the live re-check)

## Steps

1. `args(...)` loses the `prompt` parameter and ends with `--model <model>`. Add
   `--input-format text` next to `--output-format`. Remove the comment about the prompt
   having to follow `--model`.
2. `Execute` sets `spec.Stdin = req.Prompt`. `buildArgs`/`redactedArgs` become the same
   function (keep one; `redactedArgs` callers use it). `ProviderError` gains (or reuses) a
   field so the message reads `prompt: N bytes on stdin`; the prompt text is never in it.
3. Fake CLI (`ProviderScript`):
   - read stdin once: `prompt=$(cat)`;
   - find the action from the first line of `$prompt` (`ACTION: <name>`); keep scanning argv
     as a fallback so codex/gemini fakes (prompt in argv) still route;
   - `FAKE_PROMPTS_DIR` saves `$prompt`;
   - `FAKE_ARGS_DIR` writes argv only (no `<prompt>` line any more).
   Note: `$(cat)` strips trailing newlines; prompts are compared by content in tests, so
   make the saved file `printf '%s'` of the variable and adjust the two assertions that
   rely on it.
4. Update unit tests of `args` (no prompt, `--input-format text` present, `--model` last) and
   the integration assertions that expected `<prompt>`.
5. Run the live suite on the installed CLI:
   `go test -tags security ./test/security/...`. Record in `specs/permissions/DECISIONS.md`:
   date, CLI version, "prompt on stdin, all authorities hold".

## Acceptance

- A unit test calls `Execute` through `execution.FakeExecutor` with a 1 MiB prompt: the
  recorded `Spec.Args` contain no element longer than 4 KiB and `Spec.Stdin` equals the prompt.
- An integration test with a 300 KiB task text (via a file-based Jira fake or a long argv to
  the orchestrator itself, whichever is simpler) completes a research-only run.
- `ProviderError` for a failed call shows argv and `prompt: N bytes on stdin`, never prompt text.
- Live suite green on the installed CLI; DECISIONS.md updated.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty ·
`go test -tags security ./test/security/...`
