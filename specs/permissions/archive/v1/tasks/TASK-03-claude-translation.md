# T03 — `claude` adapter: policy → argv, and the death of `bypassPermissions`

**Phase** 1 · **Size** M · **Depends on** T01, T02 · **Blocks** T06, T15

## Goal

Replace the hardcoded `--permission-mode bypassPermissions` with a translation of
`domain.Policy` into argv, and declare honestly which fields the adapter enforces. This is
the task that actually removes the reported defect; everything before it is scaffolding.

## Files

- `internal/provider/claude/client.go` — `buildArgs`, `redactedArgs`, `Capabilities`
- `internal/provider/claude/client_test.go`

## What to do

1. Rewrite `buildArgs(model, prompt string, p domain.Policy) []string` per `SPEC.md` §8.10.
   All flags below are verified present on claude **2.1.278**:

   | policy | argv |
   |---|---|
   | no tools (`classify`) | `--print --tools "" --permission-prompts none --strict-mcp-config --setting-sources ""` |
   | read-only | `--print --restricted --tools Read,Grep,Glob --permission-prompts none --strict-mcp-config --setting-sources ""` |
   | write in repo | the above plus `--tools Read,Grep,Glob,Edit,Write --permission-mode acceptEdits --add-dir <root>` |
   | + exec allowlist | adds `Bash` to `--tools` and `--allowed-tools 'Bash(go build *)' …` |
   | `ApprovalAsk` | `--permission-prompts host` + T13's mechanism, replacing `none` |
   | `Bypass` | `--permission-mode bypassPermissions`, **and no `--restricted`** |
   | `MaxBudgetUSD > 0` | `--max-budget-usd <amount>` |

2. **`--permission-prompts none` is mandatory in every non-`ApprovalAsk` row** (finding
   F12). The flag defaults to `host`; omitting it turns "denied immediately" into "blocked
   forever on a host that is not listening". Assert this in argv tests for every policy, not
   just one — a hang in unattended mode is the failure most likely to get
   `bypassPermissions` reinstated.
3. `Bypass` must drop `--restricted`. Verified on 2.1.278:
   `claude --print --restricted --permission-mode bypassPermissions` →
   `Error: bypassPermissions not supported in restricted mode`. Put that quote in a comment
   at the branch so nobody "tidies up" by making `--restricted` unconditional.
4. Keep `redactedArgs` mirroring `buildArgs` exactly — it is what puts the policy flags into
   `--verbose` output and `ProviderError` messages, which is most of G5 for free. The
   cleanest way to guarantee they cannot drift is to have both call one shared builder with
   the prompt as a parameter, so the mirroring is structural rather than remembered. If you
   keep two functions, add a test that asserts they produce identical argv except for the
   final element.
5. Declare `Capabilities()`. Per §12 rule 1, a field may be `true` only once a live test
   demonstrates enforcement — but T15 is the live suite and lands later. Resolve this by
   declaring `true` for the fields whose flags are verified present **and** whose
   enforcement claim is the CLI's own documented behavior (`ConfinedWrite`, `DenyExec`,
   `DenyNetwork`, `RefuseAgentConfig`, `ExecAllowlist`, `BudgetUSD`), and leave
   `ApprovalAsk: false` until T13's spike passes. Record in `SPEC.md` §12.1 that these are
   flags-verified/enforcement-pending, so the matrix does not claim more than was tested.
6. `Execute` re-checks `provider.Supports` on entry and refuses (§8.9 step 3), so a future
   call path that skips T05's validation cannot leak authority.
7. Update the package doc comment — it currently states `bypassPermissions` is used "to run
   fully unattended", which becomes false and would otherwise mislead the next reader.

## Acceptance

- `grep -rn "bypassPermissions" internal/` matches only the `Bypass` branch here, its
  comment, and tests.
- One argv test per policy shape, asserting the full expected slice, not a `Contains`.
- `--permission-prompts none` present for every non-ask policy; `--restricted` absent only
  in the `Bypass` branch.
- `Execute` returns `*apperr.PolicyError` without spawning a process when the policy exceeds
  declared capabilities (assert with a fake executor that records invocations — zero
  invocations).
- Classification's no-tools policy produces argv already smoke-verified to work:
  `--print --tools "" --permission-prompts none --strict-mcp-config --setting-sources ""`
  ran successfully on 2.1.278.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
