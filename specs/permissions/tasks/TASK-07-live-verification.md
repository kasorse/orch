# T07 — Live verification suite against the real claude CLI

**Phase** 2 · **Size** M · **Depends on** T02 · **Blocks** T08

## Goal

Turn `SPEC.md` §11 from manual checks into a repeatable suite, and close V8–V11. Unit tests
prove flags are passed; only this proves they are enforced.

## Files

- `test/security/*_test.go` — **new**, `//go:build security`, skipped under `-short`
- `specs/permissions/SPEC.md` §11 — results with version and date

## Steps

1. The suite calls the adapter's own argv builder (not a copy), runs the real `claude` with
   a cheap model (`haiku`), each case in a fresh temporary git repository.
2. Every negative has a paired positive under a wider profile, so "it didn't happen" is
   evidence. Cases:
   - V2 exec: `edit` cannot create a file via shell (`touch`) — positive: the same file via
     Write succeeds.
   - V3/V4 `edit` writes inside; refused outside (`$TMPDIR/x`, `$HOME/x`).
   - V5 `edit` refused on `.git/hooks/pre-commit` and `.claude/settings.json`.
   - V6 deny list: `read` cannot Read `.env` or find its content with Grep; reads `a.txt`.
   - V8 `none` completes a call and has no tool (asked to read `a.txt`, cannot quote its
     sentinel content).
   - V9 `web`: WebSearch works; cannot quote a sentinel from a repository file.
   - V10 planted `.claude/settings.json` with a `PreToolUse` hook writing a marker file, and
     a planted `.mcp.json` → no marker, no MCP tool.
   - V11 `read` cannot WebFetch (positive: `web` can).
   - Environment scrubbing needs no live case: no profile can read the environment, and T04
     asserts the spawned `Spec.Env` directly.
3. Assertions are on filesystem state and sentinels, never on the model's prose.
4. Record each result in §11 with CLI version and date. A failing case flips that profile's
   `Supports` to false in T02's adapter until fixed.
5. codex/gemini cases exist and skip with "codex not on PATH" / "gemini not on PATH".

## Acceptance

- `go test ./...` unaffected; `go test -tags security ./test/security/...` runs the suite.
- §11 rows V2–V11 are ✅ with version 2.1.283 (or the version in use), or the corresponding
  profile is unsupported.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `go test -tags security ./test/security/...` · `gofmt -l .` empty
