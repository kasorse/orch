# QL06 — Live security checks for verify, decision D16

**Size** S (1 d) · **TODO** Q2 · **Depends on** QL05 · **Blocks** QL14

## Goal

The real claude CLI shows that, with verify on, the implement session can run the configured
test command and nothing else. Hooks planted mid-session are refused, and the verify
directory is out of the model's reach (`SPEC.md` §6.10). The design decision is recorded as
D16.

## Files

- `test/security/verify_test.go` (new, `//go:build security`)
- `test/security/claude_test.go`: reuse `call`/`callDenying`, plus a helper that builds the
  verify extension the way the dispatcher does (or runs the dispatcher's builder directly)
- `specs/permissions/DECISIONS.md`: D16
- `specs/permissions/SPEC.md`: §5.1 note under `edit`

## Steps

1. Test fixture: a temporary git repo with a `test.command` such as
   `echo SENTINEL-<random> > "$TMPDIR/ran" ; cat "$TMPDIR/ran"`, plus a check `vet` that
   echoes another sentinel. Build the orchestrator binary once (`go build -o`) so claude can
   start `mcp-verify`. bwrap must be available, otherwise skip with the reason.
2. V-verify-1 … V-verify-4 exactly as in `SPEC.md` §6.10. Pair each negative assertion with
   a positive control, and assert on the filesystem and sentinels, never on the model's
   wording (the suite's rule, `claude_test.go` header).
3. D16 in `DECISIONS.md`: "Verify tools are not a shell". Record the decision, why (the §6.9
   argument), how it relates to D6, the CLI version and date it was checked live, and the
   results.
4. In `specs/permissions/SPEC.md` §5.1, add a line saying `edit` may add the two `orch`
   MCP tools when `implement.verify` is on, with a reference to `specs/quality/SPEC.md` §6.

## Acceptance

- `go test -tags security ./test/security/...` is green on the installed CLI (all old
  checks plus V-verify-1…4).
- D16 is written with the version and date.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty ·
`go test -tags security ./test/security/...`
