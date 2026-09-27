# T15 — Live verification suite (`test/security`)

**Phase** 5 · **Size** M · **Depends on** T03, T07 · **Partly blocked**: codex/gemini need
those CLIs installed

## Goal

Turn the per-CLI translations from claims into evidence. `SPEC.md` §12: a translation that
has not been exercised against the real CLI is a guess, and a guess in this layer either
silently grants or silently denies. Unit tests on argv prove the flags are *passed*; only
this suite proves they are *enforced*.

## Files

- `test/security/` — **new**, build-tagged, skipped under `-short`
- `specs/permissions/SPEC.md` §12.1 — matrix updates

## What to do

1. Build-tagged (`//go:build security`), skipped under `-short`, requires a real CLI and
   network. Not part of `go test ./...`; document the invocation in README so it is runnable
   rather than theoretical.
2. **Every case is a positive *and* a negative assertion.** "It didn't write the file" is only
   evidence if the same harness shows it *can* write the file under a wider policy —
   otherwise a typo'd prompt, a refusal for an unrelated reason, or a CLI that ignored the
   whole request all look like success. This is the single most important property of this
   suite; a one-sided assertion here is how a security test passes while enforcing nothing.
3. Cases, per §12 rule 3:
   - read-only policy: cannot write a file / cannot run a command / cannot fetch a URL
   - write policy: writes inside the root; refused outside it (`/tmp`, `$HOME`)
   - exec allowlist: permits a listed command, refuses an unlisted one
   - `InheritAgentConfig: false`: a planted `.claude/settings.json` hook and a planted
     `.mcp.json` do not take effect
   - env allowlist: ask the CLI to print one forwarded and one denied variable
   - `classify` policy: no tools available at all
4. Each test runs in a temporary git repository it creates, and asserts nothing outside it was
   touched. A test that can write to `$HOME` is a test that will, eventually.
5. Update §12.1 with provider × policy × verified date × CLI version on each pass. Flags
   change between releases, so an entry without a version is not a result. Only after a pass
   may the corresponding `Capabilities()` field be declared `true` (§12 rule 1).
6. **codex/gemini are blocked** — neither is installed. Leave their cases written but skipped
   with a clear skip message naming the missing executable, so installing the CLI is all that
   is needed to get a result. Do not delete them; a skipped test is a to-do that survives.

## Acceptance

- Each case has a paired positive assertion demonstrating the harness can detect the action
  when it is permitted.
- The suite is excluded from the default `go test ./...` run and does not slow it down.
- §12.1 records every claude result with date and version 2.1.278.
- `claude`'s `Capabilities()` fields are `true` only where a passing case backs them.
- codex/gemini cases skip with an actionable message.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` (default run unaffected) ·
`go test -tags security ./test/security/...` · `gofmt -l .` empty
