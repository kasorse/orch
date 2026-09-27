# T02 — claude argv per profile; remove `bypassPermissions`; codex/gemini refuse

**Phase** 1 · **Size** M · **Depends on** T01 · **Blocks** T03, T07

## Goal

Replace the hardcoded bypass with `SPEC.md` §6.2's translation — the task that actually
removes the defect.

## Files

- `internal/provider/claude/client.go` (+ test)
- `internal/provider/codex/client.go`, `internal/provider/gemini/client.go` (+ tests)
- `orchestrator.example.yaml`

## Steps

1. One builder `args(a domain.Authority, denyRead []string, model, prompt string) []string`
   used by both `buildArgs` and `redactedArgs` (the latter passes
   `<prompt: N chars>`), so the two cannot drift. Common flags, then the profile's flags
   (§6.2 table), then `--model <model>`, then the prompt — **last, directly after
   `--model`**. Comment why: `--tools`/`--allowed-tools` are variadic and would swallow a
   following prompt.
2. `--settings` JSON from `denyRead`: each pattern `p` → `Read(**/p)` and `Read(**/p/**)`,
   built with `encoding/json` (never string concatenation). Omit `--settings` when
   `denyRead` is empty.
3. `Supports`: all four profiles `true` — each backed by a §11 row (V3–V6 now; V8–V11 are
   confirmed by T07, and a failing T07 case flips its profile to `false`).
4. `Execute`: empty or unsupported authority → `*apperr.AuthorityError`, no process spawned.
5. Package doc comment: drop the "bypassPermissions to run fully unattended" sentence;
   state the model in two lines and point to `SPEC.md` §6.2.
6. codex/gemini: `Supports` returns false for everything, with a comment giving the reason
   (§6.4, D13) so nobody "fixes" it by filling it in; the intended flags as a comment;
   `Execute` refuses like claude.
7. ~~`orchestrator.example.yaml`: route `review` to claude~~ — done in T01, since T01's
   check already refuses codex.
8. The three call sites set `Authority` here, not in T03: once `Execute` refuses an empty
   authority, leaving them to T03 would break every run in between. T03 keeps `DenyRead`,
   recording and display.
9. T01 left `claude.Supports` returning true for every valid authority with a
   `TODO(T02)`: this task makes that declaration true. Remove the TODO.

## Acceptance

- One full-slice argv test per profile (not `Contains`), including the `--settings` element.
- For every profile: `--permission-mode dontAsk`, `--permission-prompts none`,
  `--restricted`, `--strict-mcp-config` present; the last two elements are `--model <m>`
  then the prompt (then `<prompt: N chars>` for `redactedArgs`).
- `grep -rn bypassPermissions internal/` is empty.
- Empty authority and codex/gemini with any authority → `AuthorityError`, fake executor
  records zero runs.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
