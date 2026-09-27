# T13 — Mid-run per-tool approval for `manual` mode

**Phase** 4 · **Size** L · **Depends on** T03, T06, T12 · **Blocks** nothing

Per the Q4 resolution this is **in the first release**. Mechanism: `--permission-prompt-tool`
+ `--mcp-config`, with stream-json host mode as the documented fallback.

## Goal

Make `manual` mean per-tool-call approval, not just gating between steps. This is the
strongest control in the spec and the only inherently provider-specific one — hence it is
layer 4, gated behind an adapter capability, and never a substitute for §8.2.

## Step 0 — Spike (gates the rest of this task)

`--permission-prompt-tool` parses on claude 2.1.278; **the round-trip is unverified**. Before
writing any production code, prove out of tree:

1. A minimal MCP server exposing one approval tool.
2. `claude --print --permission-prompts host --permission-prompt-tool <name> --mcp-config <cfg>`
   invoking that tool for a decision.
3. Both outcomes honored: an **allow** lets the tool call proceed, and a **deny** stops it.
   Verify deny explicitly — a mechanism that only demonstrably transmits "yes" is not a
   control.

Record the result in `SPEC.md` §12.1. **If the spike fails**, switch to
`--input-format stream-json --output-format stream-json --permission-prompts host` and answer
`can_use_tool` control requests on the wire — and note that this route additionally requires
reworking how `internal/action` parses each step's stdout as a JSON artifact, which is the
main reason the MCP route is preferred.

## Files

- `internal/interaction/approval.go` — **new**: the prompt, the bounded wait, `approvals.log`
- `internal/provider/claude/` — the mechanism, `ApprovalAsk: true` once verified
- `internal/app/app.go` — deadline handling, wiring the approver
- `internal/persistence/filesystem.go` — `approvals.log` (path added in T12)

## What to do

1. The prompt is rendered by `internal/interaction` on the orchestrator's own stdin/stdout.
   This works because `execution.Spec.Stdin` is unset for provider calls, so exactly one
   component talks to the user. If the fallback route is taken, stdin *is* used for
   stream-json — call that out, because it invalidates this property and the approver then
   needs a different channel (e.g. `/dev/tty`).
2. **Deadline (Q3).** Approval prompts run under a context that is **not** derived from the
   run's deadline: machine time is bounded by `execution.timeout`, human time is not.
   Cancellation must still propagate, so derive from the signal-cancelled root rather than
   `context.Background()` — Ctrl-C at a prompt must still abort the run. Document in MVP.md
   §25 (T14).
3. Each prompt has its own bounded wait and **denies on expiry**. Never allow-on-timeout —
   that inverts the control precisely when nobody is watching.
4. Audit: append every decision to `logs/approvals.log` — tool, argument summary, allow/deny,
   who, elapsed. Arguments may contain repository content, so summarize and truncate rather
   than dumping; the log is also the artifact an auditor reads, so "summary" must still
   identify what was approved (e.g. the path written, the command line).
5. `Capabilities().ApprovalAsk` becomes `true` for claude only after the spike passes; codex
   and gemini keep `false`. A policy requiring `ApprovalAsk` against an adapter without it
   fails per §8.1 with a message naming provider, action, and **both** ways out:
   `--mode accept-edits`, or route that action at a provider that supports it.
6. Keep the per-step gate (`interaction.TerminalGate`). Per-tool approval refines it; it does
   not replace it, and removing it would change `manual` mode's existing documented behavior.
7. Watch for approval fatigue, which is a real risk rather than a theoretical one: a long run
   that prompts dozens of times gets rubber-stamped, which manufactures a false audit trail.
   Mitigation within scope: the prompt shows what is being approved specifically enough to be
   worth reading, and `approvals.log` records elapsed time per decision so a reviewer can see
   which decisions took no thought.

## Acceptance

- Spike result recorded in §12.1 before production code lands.
- `manual` mode prompts per tool call; allow proceeds, deny stops the call.
- Prompt timeout denies, and the denial is logged.
- A long human deliberation (longer than `execution.timeout`) does **not** fail the run —
  assert with a short configured timeout and a slow fake prompter.
- Ctrl-C during a prompt still aborts, exit code 5.
- `logs/approvals.log` records every decision with elapsed time.
- `ApprovalAsk` against codex/gemini fails with a `PolicyError` naming both remedies, before
  any AI call.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
