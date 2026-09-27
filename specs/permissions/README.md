# Execution authority for provider CLI calls

Status: **implemented (v2)** on branch `feature/permissions`: T01–T08 landed; T09 (test-step
sandbox) is optional and not started.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification: findings, principles, profiles, guards, verification |
| [`DECISIONS.md`](DECISIONS.md) | D1–D14: what was decided, why, what was rejected; the fate of every v1 decision and task |
| [`tasks/`](tasks/) | 9 implementation tasks |
| [`archive/v1/`](archive/v1/) | the superseded v1 design (15 tasks), kept as the record of what was considered |

MVP.md §38 is the normative home; README "Security model" is the operator's view. Where
they disagree with this folder, MVP.md wins.

## The problem in one paragraph

Before this work every provider call — workflow steps, the translator, the LLM decision
provider — ran `claude --permission-mode bypassPermissions`, so steps that only needed to
read could run commands, write anywhere, reach the network and load the repository's own
hooks and MCP servers. The fix: each call site gets one of four fixed tool profiles (`none`, `web`,
`read`, `edit`); no profile has a shell; everything not granted is denied without prompting;
a provider that cannot enforce a profile is refused before any AI call; and the orchestrator
itself guards what `implement` changed before `test` runs anything.

## The model

| call site | authority | tools (claude) |
|---|---|---|
| translate, decide-context, decide-classify | `none` | — |
| research | `web` | WebSearch, WebFetch |
| analyze, plan, review | `read` | Read, Grep, Glob |
| implement | `edit` | Read, Grep, Glob, Edit, Write |
| test | — | the orchestrator runs `test.command` |

All claude calls: `--restricted --permission-mode dontAsk --permission-prompts none
--strict-mcp-config --setting-sources ""` plus Read deny rules for the secret deny list.
Modes do not change authority; they change how much the human sees between steps.

## Tasks

| phase | task | title | size | depends on |
|---|---|---|---|---|
| 1 | [T01](tasks/TASK-01-authority-core.md) | `domain.Authority`, provider contract, fail-fast check | S–M | — |
| 1 | [T02](tasks/TASK-02-claude-argv.md) | claude argv per profile; remove bypass; codex/gemini refuse | M | T01 |
| 1 | [T03](tasks/TASK-03-plumbing-and-record.md) | every call site passes authority; record and show it | S–M | T01, T02 |
| 2 | [T04](tasks/TASK-04-env-scrub.md) | scrub parent-session variables | S | — |
| 2 | [T05](tasks/TASK-05-worktree-and-guard.md) | clean worktree, restore point, protected-path guard | M | T03 |
| 2 | [T06](tasks/TASK-06-prompt-fencing.md) | fence repository-derived text in prompts | S | — |
| 2 | [T07](tasks/TASK-07-live-verification.md) | live verification suite | M | T02 |
| 2 | [T08](tasks/TASK-08-documentation.md) | MVP.md §38, README, CLAUDE.md | S–M | T01–T05, T07 |
| 3 | [T09](tasks/TASK-09-test-sandbox.md) | optional: `test.command` under bwrap | M | T04 |

```
T01 ── T02 ──┬── T03 ── T05 ──┐
             └── T07 ─────────┴── T08
T04, T06  independent          T09 ← T04 (optional, needs an AppArmor profile for bwrap)
```

Phase 1 (T01–T03) removes `bypassPermissions` and is shippable on its own. Rough size:
phase 1 ≈ 1.5 days, phase 2 ≈ 2 days, phase 3 ≈ 1 day.

## Ground rules for every task

- Flags live only in `internal/provider/<name>/buildArgs`; `workflow` and `policy` import
  only `domain`.
- A profile is `Supports == true` only with a live check behind it (`SPEC.md` §11).
- Refusing before any AI call is always better than a silent downgrade.
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .`
  empty.

## Deferred

- **Shell for `implement`**: only via Claude Code's OS sandbox with `failIfUnavailable`;
  needs `socat` and an AppArmor profile for bwrap on this machine (D6).
- **codex/gemini**: not installed; refuse every profile until verified (D13).
- **Worktree isolation per step**: out of scope; never via `claude --worktree` (v1 Q8).
- **Untrusted repositories**: outside the scenario; `SPEC.md` §10 lists what they would need.
