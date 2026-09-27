# Decisions: execution authority (v2)

Decided 2026-09-27, after reviewing v1 (`archive/v1/`) against the current code (post-Jev
integration), Claude Code's own documentation, and live checks on claude 2.1.283
(`SPEC.md` §11). Each entry: the decision, why, what was rejected. The last section maps
every v1 decision and task to its fate.

---

## D1 — Four fixed authority profiles instead of a field-by-field `Policy` struct

**Decision.** `domain.Authority` is an enum (`none`, `web`, `read`, `edit`), chosen per call
site by a fixed table. Adapters declare which profiles they support.

**Why.** v1's `Policy` struct (ReadFiles, WriteFiles, Exec, ExecAllowlist, Network,
Approval, WriteRoots, InheritAgentConfig, Bypass, MaxBudgetUSD) plus `Validate` rules for
incoherent combinations, a `Capabilities` struct and `Supports` field-matching exists to
describe combinations that no call site uses. Every real call site needs exactly one of four
shapes. An enum makes incoherent combinations unrepresentable instead of validated, and a
capability check becomes `Supports(profile) bool`.

**Rejected.** v1 `Policy` (above). Also the name `Policy`: `domain.DecisionPolicy` and
`internal/policy` already mean decision thresholds.

## D2 — `research` gets the web and nothing else; the three non-v1 call sites get `none`

**Decision.** `research` → `web` (WebSearch, WebFetch, no file tools). `translate`,
`decide-context`, `decide-classify` → `none`.

**Why.** v1 predates the Jev integration: its table has `classify` and `answer` (both gone)
and no `research`. The research prompt asks for "external knowledge … approaches, libraries,
tools", so v1's "no network for anything" would have broken it. The lethal-trifecta rule
(`SPEC.md` P2) resolves it: research receives only the task text, so giving it the web but
no repository access leaves nothing private to leak. The translator and the LLM decision
provider were added after v1 and today also run with `bypassPermissions`.

## D3 — Modes do not change per-call authority

**Decision.** The same profile table applies in `manual`, `accept-edits` and `auto`. Modes
differ in human visibility between steps (gates, the post-implement change summary).

**Why.** v1 goal G3 ("mode means something") mapped `manual` to per-tool approval and the
others to deny. Once every profile is deny-by-default with no shell, there is nothing left
that a per-tool prompt would usefully ask about: edits inside the repository are the
`implement` step's job, and everything else is already refused. Making authority
mode-independent also removes v1's two-phase capability check (mode-independent fields
first, `Approval` re-checked after `resolveMode`) — one check at startup is complete.

**Rejected.** Mode-dependent authority (v1 §8.3).

## D4 — No mid-run per-tool approval

**Decision.** Dropped (v1 T13, size L, gated on an unverified MCP round-trip spike).

**Why.** It would ask about tool calls that the profiles already allow (edits in the repo) or
already deny (everything else). v1 itself noted approval fatigue turns long runs into
rubber-stamping. The cost was the largest in v1: an MCP server, a separate non-deadlined
context for human waits (v1 Q3), `approvals.log`, and a fallback that would rework
`internal/action`'s stdout parsing. The useful part — a human looking at what `implement`
did before it is executed — is delivered by the `manual` gate showing `git diff --stat` and
the guard result (`SPEC.md` §7.3), which is provider-neutral.

**Revisit if** a profile ever gains a shell tool.

## D5 — No bypass escape hatch

**Decision.** `bypassPermissions` disappears from the codebase. No
`--dangerously-bypass-permissions`.

**Why.** v1 kept a flag gated on `ORCHESTRATOR_SANDBOX=1`/`/.dockerenv` (v1 Q1, T11). No
scenario needs it: nothing in the workflow requires authority beyond the four profiles.
An unused escape hatch is attack surface and a standing temptation. If a containerised
"full authority" mode is ever wanted, it is a new decision with its own threat model.

## D6 — No shell for `implement`, and no Bash-pattern allowlist ever

**Decision.** `implement` has no Bash tool. v1's `security.implement_exec_allowlist`
(`Bash(go build *)` patterns) is dropped. If shell access becomes necessary, the only
acceptable mechanism is Claude Code's OS sandbox with `sandbox.failIfUnavailable: true`,
`allowUnsandboxedCommands: false`, network off.

**Why.** The workflow already has `implement → test`, and `test` is the orchestrator's own,
trusted execution; compile errors come back to `implement` through the test failure. Claude
Code's documentation states that Bash rules constraining arguments are fragile
(options before arguments, variables, redirects, `sh -c`, a program by path) and recommends
the sandbox for anything that must hold. On this machine the sandbox is unavailable (V7:
`socat` missing, bwrap blocked by AppArmor's unprivileged-userns restriction), which is
exactly why the allowlist would have been the only line of defense — and a porous one.

**Prerequisites to revisit:** `socat` installed, AppArmor profile for `bwrap`, then a
spec change that adds a fifth profile.

## D7 — Scrub parent-session variables; no environment allowlist

**Decision.** Children (providers and `test`) get the inherited environment minus an exact
denylist: `CLAUDECODE`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`,
`CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`.

**Why.** v1's allowlist (T07) protected against a step reading the environment through a
shell — which no profile has any more. It was also the fragile part: v1 Q2 found three auth
variables missing from the draft, and "the security change broke my auth" is how security
changes get reverted. v1's finding F11 remains valid and is the part kept: a child must not
inherit a live authenticated IPC channel to a parent Claude Code session. That matters for
provider children and even more for `test`, which runs agent-written code.

## D8 — Guard the result, not only the tool: protected paths and `.git` after `implement`

**Decision.** After each `implement`, the orchestrator checks changed files against protected
patterns and fingerprints `.git/config` and `.git/hooks/`, and stops before `test` on a
violation (`SPEC.md` §7.3).

**Why.** New in v2. Files like `.claude/settings.json`, CI workflows, `.envrc`, the
orchestrator config (`test.command`) and `.git/config` (`core.fsmonitor`, run by the
orchestrator's own `git status`) turn a write into later execution. claude already refuses
`.git/` and `.claude/` writes under `dontAsk` (V5), but a check on the result is
provider-neutral and does not depend on any CLI's protected-path list staying the same.
It also subsumes most of v1 Q6 (config provenance) for the operator's own repositories: a
run cannot change the config the next run will load without being stopped.

## D9 — Clean worktree only for workflows with `implement`; record `pre_run_ref`

**Decision.** Kept from v1 (Q8 context, T09 first half), simplified: checked once after
`workflow.Build`, before the approval prompt; read-only workflows keep the warning.

**Why.** A named restore point makes the guard's "changes left on disk for inspection" safe
and makes `git` the undo mechanism MVP.md §31.3 assumes.

## D10 — Test-step isolation is optional phase 3, and it is a scope change

**Decision.** `test.sandbox: none | bwrap`, default `none`, implemented in `testexec`
(`SPEC.md` §8). It amends MVP.md §30 for the test step only.

**Why.** F5 — `test` executes agent-written code with full authority — is the largest
remaining hole, and unlike provider isolation it is under the orchestrator's own control.
It is optional because it needs a host change here (AppArmor profile for bwrap) and per-
project bind configuration (Go caches), and it does not block removing `bypassPermissions`.
Docker was considered and rejected as the first backend: it needs an image carrying the
project's toolchain, which is per-project config the orchestrator should not own.

## D11 — Audit via `run.json`, no `policy.json`

**Decision.** `StepExecution.Authority` and `Run.PreRunRef` in `run.json`; the authority is
printed in the workflow block before approval. No separate `policy.json`, no drift test.

**Why.** With four profiles and a mode-independent table, "what was this step allowed to
do" is one word per step; v1's `policy.json` carried env names, ignored advisory fields,
bypass flag and provenance verdicts, most of which no longer exist. The table is small
enough to state in MVP.md §38 by hand; v1's `PolicyTableMarkdown` + drift test was sized
for the larger table.

## D12 — `MaxBudgetUSD` dropped

**Decision.** Not part of v2.

**Why.** It is a cost guardrail, not an authority control; `max_steps` and
`max_fix_iterations` already bound a run. It can be added later as an ordinary config key
without touching this model.

## D13 — codex/gemini refuse every profile until verified

**Decision.** Kept from v1 (T04): `Supports` is false for all profiles; the example config
routes every action to claude.

**Why.** Unverified translation must not ship as verified. Additionally, codex's read-only
sandbox allows reading outside the repository through its shell, so its `read` is weaker
than claude's; this needs analysis, not just flag mapping (`SPEC.md` §6.4).

## D14 — MVP.md home is §38, not §36

MVP.md §36 is already "Implementation Guidelines" and §37 the planned resume feature; v1
predates that numbering.

---

## Fate of v1

| v1 | fate in v2 |
|---|---|
| Q1 bypass refuses outside sandbox | superseded by D5 (no bypass at all) |
| Q2 env allowlist | superseded by D7; F11 kept |
| Q3 approval-wait deadline | moot (D4) |
| Q4 mid-run approval in first release | dropped (D4) |
| Q5 no exec, global exec allowlist | no exec kept; allowlist dropped (D6) |
| Q6 config provenance | mostly subsumed by D8; untrusted repos out of scenario (`SPEC.md` §10) |
| Q7 advisory `MaxBudgetUSD` | dropped (D12) |
| Q8 worktree isolation out of scope | unchanged; still do not use `claude --worktree` |
| Q9 MVP.md normative | kept, as §38 (D14) |
| F12 `--permission-prompts` defaults to `host` | kept, verified again on 2.1.283 (V2) |

| v1 task | v2 |
|---|---|
| T01 domain.Policy | T01 (`Authority` enum) |
| T02 capabilities/Supports/PolicyError | T01 |
| T03 claude translation | T02 (`dontAsk`, deny rules, no bypass branch) |
| T04 codex/gemini unverified | T02 |
| T05 security config + ValidatePolicies | T01 (check) + T05 (config keys) |
| T06 plumbing | T03 (plus translate and decision/llm) |
| T07 env allowlist | T04 (scrub denylist only) |
| T08 prompt fencing | T06 |
| T09 worktree + provenance | T05 (worktree, pre_run_ref, guard); provenance dropped |
| T10 authority block | T03 (one line per step in the existing workflow block) |
| T11 bypass flag | dropped (D5) |
| T12 policy.json audit | T03 (`run.json` fields) (D11) |
| T13 mid-run approval | dropped (D4) |
| T14 docs + drift test | T08 (no drift test) |
| T15 live verification | T07 |
| — | T09 test-step sandbox (new, optional) |
