# Permission & execution-authority model

Status: **accepted** — all §13 open questions resolved, ready to implement. No code written yet.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification: findings, threat model, design (§8), config (§9), verification (§12) |
| [`DECISIONS.md`](DECISIONS.md) | Q1–Q9: what was decided, why, and what was rejected |
| [`tasks/`](tasks/) | 15 implementation tasks, each with files, steps, acceptance, gates |

MVP.md §36 will be the **normative** home of this model once T14 lands; this folder stays as
the design record and rationale. Where the two disagree, MVP.md wins.

## The problem in one paragraph

Today every provider call runs with the same authority regardless of action or mode:
`claude` is invoked with `--permission-mode bypassPermissions`, so `analyze` and `review` —
steps that only need to read — can write files, run commands, and reach the network. The
run mode (`plan`/`manual`/`accept-edits`/`auto`) changes when the orchestrator asks the human,
not what the model is permitted to do. The fix is a provider-neutral `domain.Policy` derived
from a fixed `(action, mode)` table, translated to CLI flags only inside each provider
adapter, with adapters that cannot enforce a mandatory field **refusing** rather than
silently downgrading.

## Decisions (detail in [`DECISIONS.md`](DECISIONS.md))

| # | question | answer |
|---|---|---|
| Q1 | bypass outside a sandbox | **Refuse.** Requires `ORCHESTRATOR_SANDBOX=1` or `/.dockerenv`; no typed-confirmation fallback |
| Q2 | is the env allowlist right | **No** — corrected: missing vars added, and the drafted `CLAUDE_CODE_*` wildcard removed (it forwarded the parent session's IPC token, F11). No smoke-test call in `validate` |
| Q3 | approval waits vs `execution.timeout` | Separate non-deadlined context; `execution.timeout` bounds machine time only |
| Q4 | mid-run per-tool approval | **In the first release** (T13), via `--permission-prompt-tool` + `--mcp-config`, gated on a spike |
| Q5 | exec allowlist shape | No exec by default; one global `security.implement_exec_allowlist` |
| Q6 | config file inside the worktree | Refuse if untracked or modified; warn if tracked and clean |
| Q7 | mandatory vs advisory split | Accepted; `MaxBudgetUSD` is the only advisory field |
| Q8 | worktree isolation | Out of scope, recorded as a future option; do **not** use `claude --worktree` |
| Q9 | MVP.md §36 | Yes, normative; this folder is the design record |

## Tasks

| phase | tasks | outcome |
|---|---|---|
| 1 | T01–T06, T12 | policy exists, is enforced for claude, and is recorded |
| 2 | T07–T08 | env allowlist, prompt fencing |
| 3 | T09–T11 | worktree/provenance, authority block, bypass flag |
| 4 | T13 | mid-run per-tool approval (spike first) |
| 5 | T14–T15 | documentation and live verification |

| task | title | size | depends on |
|---|---|---|---|
| [T01](tasks/TASK-01-domain-policy.md) | `domain.Policy` + the `PolicyFor` table | S–M | — |
| [T02](tasks/TASK-02-provider-capabilities.md) | provider capabilities, `Supports`, `PolicyError` | S–M | T01 |
| [T03](tasks/TASK-03-claude-translation.md) | claude policy→argv translation | M | T01, T02 |
| [T04](tasks/TASK-04-codex-gemini-unverified.md) | codex/gemini declare nothing; re-point example config | S | T02 |
| [T05](tasks/TASK-05-security-config-validation.md) | `security:` config + `ValidatePolicies` in the fail-fast chain | M | T01, T02 |
| [T06](tasks/TASK-06-action-classifier-plumbing.md) | build and thread policy through action/classifier | M | T01, T02, T03, T05 |
| [T07](tasks/TASK-07-env-allowlist.md) | `execution.Spec.Env` + allow/deny lists | M | T05, T06 |
| [T08](tasks/TASK-08-prompt-fencing.md) | nonce-fenced untrusted content in prompts | S | — |
| [T09](tasks/TASK-09-worktree-and-provenance.md) | clean-worktree rule, `pre_run_ref`, config provenance | M | T05, T06 |
| [T10](tasks/TASK-10-authority-block.md) | authority block in output, per-step policy line | S–M | T06, T07 |
| [T11](tasks/TASK-11-bypass-flag.md) | `--dangerously-bypass-permissions` + isolation check | S | T03, T05, T10 |
| [T12](tasks/TASK-12-audit-record.md) | `policy.json`, per-step policy, `approvals.log` path | S–M | T06 (fields from T07, T09, T11) |
| [T13](tasks/TASK-13-midrun-approval.md) | mid-run per-tool approval (**spike gates it**) | L | T03, T06, T12 |
| [T14](tasks/TASK-14-documentation.md) | MVP.md §36, README security model, drift test | M | T01–T12 |
| [T15](tasks/TASK-15-live-verification.md) | live verification suite | M | T03, T07 |

```
T01 ── T02 ──┬── T03 ──────────┐
             ├── T04           ├── T06 ──┬── T07 ──┬── T10 ── T11 ──┐
             └── T05 ──────────┘          │        │                ├── T12 ── T13
                                          └── T09 ─┴────────────────┘
T08  independent (parallel with T07)
T15 ← T03, T07                T14 ← everything (lands in fragments, see its schedule)
```

T12 appears late in the graph only because three of its fields come from T07/T09/T11; the
record itself lands in phase 1, right after T06.

Two things gate more than they look like they do: **T02** (until adapters can refuse, nothing
downstream is fail-closed) and **T13's spike** (if the MCP round-trip does not work, `manual`
mode's mechanism changes and `internal/action`'s stdout parsing has to be reworked).

## Ground rules that apply to every task

- Nothing outside `internal/provider/<name>/buildArgs` may contain provider-specific flag
  knowledge. `internal/workflow` keeps importing only `domain`.
- A capability is declared `true` only after it has been verified against the real CLI at a
  named version (§12 rule 1). Unverified ⇒ zero-value ⇒ refuse.
- Silent downgrade is the one unacceptable failure mode. Refusing loudly before any AI call is
  always preferable.
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .`
  empty.

## Deferred

- **Worktree isolation** (Q8): running each step in a throwaway `git worktree` instead of the
  user's checkout. Larger and orthogonal; if built, `internal/repository` owns the path, never
  `claude --worktree`.
- **OS-level sandboxing**: out of scope per MVP.md §30. The orchestrator detects and can
  require isolation; providing it remains the operator's job.
- **codex/gemini enforcement**: neither CLI is installed, so nothing about them can be
  verified. T04 makes that state explicit and safe; T15 keeps their cases as skipped tests.
