# T10 — Up-front authority block in CLI output

**Phase** 3 · **Size** S–M · **Depends on** T06, T07 · **Blocks** T14

## Goal

Make the authority a run will use visible **before** it is used, in every mode. Goal G5's
operator-facing half: the plan the user approves now includes what the run is permitted to do.

## Files

- `internal/interaction/review.go` — print before the mode prompt
- `internal/logging/progress.go` — the renderer, the per-step policy line, the bypass banner
- `internal/logging/progress_test.go` — this file asserts output verbatim, so it changes here
- `internal/app/app.go` — print in `--mode auto` too

## What to do

1. Render §8.8a's block from the resolved policies, immediately after the workflow and before
   the mode prompt:

   ```
   Workflow:
   analyze → implement → test → review

   Authority (mode: accept-edits):
     analyze    read-only        no commands, no network
     implement  write in repo    no commands, no network   /home/user/src/orch
     test       orchestrator runs: go test ./...
     review     read-only        no commands, no network
   Environment forwarded: PATH, HOME, TERM, LANG, TMPDIR, USER, ANTHROPIC_API_KEY
   Repository agent config (settings/MCP/hooks): ignored

   Approve this plan? [a]pprove / [e]dit / [c]ancel:
   ```

2. Reuse the existing approve/edit/cancel loop — no second prompt. The authority is part of
   the plan being approved, which is the point.
3. **Mode dependency.** Without `--mode`, the mode is chosen *after* this prompt, so the block
   cannot state a single mode's approval level at that moment. Two workable shapes: render the
   mode-independent rows first and print the approval line after `resolveMode`, or render once
   per candidate mode. Pick the first (less noise) and re-print a one-line
   `Approval: deny-by-default` / `ask per tool call` confirmation after the mode is chosen.
   Note this in the code — the interaction between "approve the plan" and "then choose a mode"
   is already the trickiest part of `ReviewAndSelectMode`.
4. In `--mode auto` the same block is **printed, not prompted**, so non-interactive runs still
   record their authority in their output — which is where a CI log will preserve it.
5. `--verbose` adds a per-step policy line at step start. The argv echo already shows the
   flags (T03 kept `redactedArgs` mirroring `buildArgs`), so this is one line of context, not
   a duplicate.
6. Bypass banner (§8.4 rule 3) on stderr before the first call when `Bypass` is set. Loud,
   unmissable, and on stderr so it survives stdout redirection.
7. `logging/progress_test.go` asserts the default output shape verbatim. Update it
   deliberately, and keep MVP.md §27's existing shape intact around the addition — this is a
   documented output contract, not incidental formatting.

## Acceptance

- The block appears in all four modes; `auto` prints without prompting.
- `test` rows show the orchestrator's own command, not a provider policy — `test` makes no AI
  call, and implying otherwise would misrepresent the model.
- Forwarded variable names appear; **no values** (assert with a sentinel value in the env).
- `--verbose` shows a per-step policy line.
- Bypass banner goes to stderr and is present whenever `Bypass` is set.
- MVP.md §27's documented default shape still matches the non-verbose output (T14 updates §27
  for the addition).

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
