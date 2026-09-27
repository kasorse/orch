# Spec: Execution authority for provider CLI calls (v2)

Status: **accepted**, ready to implement via `tasks/`. Supersedes v1 (kept verbatim in
`archive/v1/`; what changed and why is in `DECISIONS.md`).
Scope: how much authority each provider CLI invocation gets, how that is enforced, checked
before any AI call, recorded, and documented. No change to the decision flow, workflow
construction, transitions, limits, static routing, or what is trusted (exit codes + git diff).
MVP.md gains a normative §38 once T08 lands; where the two disagree, MVP.md wins.

---

## 1. Problem

Every provider call — every workflow step, the translator and the LLM decision provider —
runs with all permission checks off:

```go
// internal/provider/claude/client.go:27
"--print", "--permission-mode", "bypassPermissions", "--model", model, prompt
```

So `analyze`, `plan` and `review`, which only need to read, can run any command, write
anywhere the user can, reach the network, and load the target repository's own hooks and MCP
servers. The only thing keeping them read-only today is a sentence in the prompt
(`readOnlyRule`, `internal/action/prompts.go:15`) — an instruction, not a control.

## 2. Findings

| # | Finding | Where |
|---|---|---|
| F1 | `bypassPermissions` for every call, regardless of what the step needs. | `internal/provider/claude/client.go:30,42` |
| F2 | Three call sites construct `provider.Request`: `action.Dispatcher.call`, `translate.Translator`, `decision/llm.Provider`. The latter two send only the user's request and need no tools at all, yet get full authority. | `internal/action/context.go:110`, `internal/translate/translator.go:112`, `internal/decision/llm/provider.go:115` |
| F3 | Repository agent configuration is loaded by the spawned CLI: `.claude/settings.json` hooks, `.mcp.json` servers, plugins. `-p` skips the workspace-trust dialog. | claude argv |
| F4 | The Read tool can read `.env`, `*.pem`, `*.key` inside the repository and send them to the model provider, contradicting the "secrets never leave the machine" invariant that `internal/projectctx` enforces for project context. | claude argv; `internal/projectctx/secrets.go:13` |
| F5 | **The test step executes code the agent wrote**, with the user's full authority, via `sh -c test.command`. Denying `implement` a shell postpones execution of agent-written code; it does not prevent it. | `internal/testexec/runner.go:25` |
| F6 | A write-capable step can change files that grant execution later: `.claude/settings.json` (hooks for the next interactive session), CI workflows, `.envrc`, the orchestrator config itself (`test.command`), `.git/config` (`core.fsmonitor` runs on the orchestrator's own `git status`), `.git/hooks/*`. | — |
| F7 | A dirty worktree is only warned about (`app.go:156`), so after a run there is no clean point to return to. | `internal/app/app.go:152-158` |
| F8 | The child inherits the parent environment, including the IPC channel of a Claude Code session the orchestrator may have been launched from (`CLAUDE_CODE_MESSAGING_SOCKET`/`_TOKEN`, `CLAUDECODE`, …). | `internal/execution/process.go:47` |
| F9 | codex/gemini adapters pass no sandbox/approval flags; their authority is whatever the CLI defaults to. | `internal/provider/{codex,gemini}/client.go:29` |
| F10 | What a step was allowed to do is not recorded anywhere in the run directory. | `internal/domain/run.go` |

## 3. Principles

- **P1 — The tool set is the boundary.** A step is restricted by the tools it is *given*
  (`--tools`), not by rules about how it may use them. A model without a shell tool cannot
  run a command, whatever its prompt says. Rules on arguments (`Bash(go build *)`) are
  documented by Claude Code itself as bypassable and are never used as a boundary here.
- **P2 — Never the whole trifecta in one step.** No step gets all three of: private data
  (the repository), untrusted content (web pages; attacker-influenced repo text), and an
  exfiltration channel (network, shell). Research gets the web but not the repository;
  every repository step gets no network and no shell.
- **P3 — Deny by default, never hang.** Anything not granted is refused immediately
  (`dontAsk`, `--permission-prompts none`), so unattended runs neither gain authority nor
  block on a prompt nobody will answer.
- **P4 — Fixed in Go, fail closed.** Authority per call site is a fixed table in code. A
  provider that cannot enforce a profile refuses it, and the refusal happens in the
  fail-fast chain before any AI call. Silent downgrade is the one unacceptable failure.
- **P5 — The orchestrator guards what the CLI cannot.** Checks that are provider-neutral
  (clean worktree, protected paths after `implement`, environment scrubbing) live in the
  orchestrator and apply to every provider.

## 4. Non-goals

- Mid-run per-tool-call approval (v1 T13). See `DECISIONS.md` D4.
- A bypass escape hatch. See D5.
- Shell access for `implement`, in any form, until an OS sandbox is available (D6).
- An environment *allowlist* (D7) — a small denylist of parent-session variables only.
- Configurable per-action authority, a policy DSL, per-user roles.
- Running on repositories the operator does not trust. The model is designed for the
  operator's own repositories; §10 states what an untrusted repository would additionally
  need.

## 5. Authority profiles

New file `internal/domain/authority.go`. (`domain.Policy`/`DecisionPolicy` and
`internal/policy` already mean the decision thresholds, so the permission concept is named
**authority** throughout.)

```go
// Authority is the fixed set of tools one provider call may use. It is chosen by Go code
// per call site (AuthorityFor), never by a model, a provider default or config.
type Authority string

const (
	AuthorityNone Authority = "none" // no tools: text in, text out
	AuthorityWeb  Authority = "web"  // web search/fetch; no file access at all
	AuthorityRead Authority = "read" // read/search files under the working directory
	AuthorityEdit Authority = "edit" // read + create/modify files under the working directory
)
```

There is no field for shell or network-from-shell: no profile has them (P1, D6).

### 5.1 The fixed table

| call site | authority | why |
|---|---|---|
| `translate` | `none` | translates the request text |
| `decide-context`, `decide-classify` (LLM decision provider) | `none` | answers questions about the request (+ project context JSON in the prompt) |
| `research` | `web` | external knowledge; the prompt carries only the task, so there is no repository data to leak (P2) |
| `analyze` | `read` | inspects the repository |
| `plan` | `read` | inspects the repository |
| `review` | `read` | the diff is in the prompt; reads surrounding code |
| `implement` | `edit` | the only step that changes code; no shell — compiling and testing is `test`'s job |
| `test` | — | no AI call; the orchestrator runs `test.command` (§8) |

`domain.AuthorityFor(ActionType) Authority` implements the rows for workflow actions from a
package-level map. An action with no row gets `AuthorityNone`, so a future action added
without a row fails closed. `translate` and the decision provider pass `AuthorityNone`
explicitly — they are not workflow actions.

### 5.2 Modes do not change authority

| mode | effect on authority | what the mode changes |
|---|---|---|
| `plan` | nothing executes | — |
| `manual` | same table | gate before every step; after `implement` the gate shows the change summary and guard result (§7.3) |
| `accept-edits` | same table | no gates |
| `auto` | same table | no gates, no prompts at all |

Deny-by-default already makes every mode safe to run unattended, so there is nothing left
for a mode to widen or narrow per call. The difference between modes is how much the human
sees *between* steps. `accept-edits` and `auto` differ as they do today (whether the
workflow-approval prompt is shown). D3 records why v1's "mode changes per-call authority"
was dropped.

## 6. Provider translation

### 6.1 Contract

- `provider.Request` gains `Authority domain.Authority`. The zero value `""` is **invalid**
  and refused, so a call site that forgets to set it fails a test instead of running with
  something by accident.
- `provider.Client` gains `Supports(domain.Authority) bool` — what the adapter can enforce,
  declared in the adapter.
- `Execute` refuses (`*apperr.AuthorityError`, no process spawned) when the authority is
  empty or unsupported. This is the belt; §6.5 is the braces.
- Flags exist **only** in each adapter's `buildArgs`. `domain`, `workflow`, `action`,
  `translate`, `decision` stay provider-neutral.

### 6.2 claude

Common to every profile (all verified on 2.1.283, §11):

```
--print
--restricted                  # no Bash/REPL/WebFetch unless --tools names them; ignores
                              # user/project/local settings files; confines file tools to
                              # the working directory; refuses bypassPermissions
--permission-mode dontAsk     # anything not pre-approved is denied, not prompted
--permission-prompts none     # nobody answers prompts (default is "host": would hang)
--strict-mcp-config           # no MCP servers (none are passed)
--setting-sources ""          # load no settings sources
--settings <json>             # Read deny rules from the secret deny list (§6.3)
--model <model>               # MUST directly precede the prompt (see below)
<prompt>
```

Per profile:

| authority | added flags |
|---|---|
| `none` | `--tools ""` |
| `web` | `--tools WebSearch,WebFetch --allowed-tools WebSearch,WebFetch` |
| `read` | `--tools Read,Grep,Glob` |
| `edit` | `--tools Read,Grep,Glob,Edit,Write --allowed-tools Edit,Write` |

Notes:

- `--tools`, `--allowed-tools`, `--add-dir`, `--mcp-config` are **variadic**. A variadic flag
  directly before the prompt would swallow the prompt as another value. The prompt is
  always last and always directly preceded by `--model <model>`; a test pins the order.
- In `dontAsk`, reads inside the working directory need no rule; edits and web tools do,
  hence `--allowed-tools` for `edit` and `web`.
- `.git/` and `.claude/` are protected paths in every mode but `bypassPermissions`: writes
  there prompt, and with `dontAsk` + `--permission-prompts none` a prompt is a denial
  (verified: `.git/hooks/pre-commit` refused).
- `--setting-sources ""` also drops the operator's `~/.claude/settings.json`. Auth still
  works on the development machine (OAuth / environment). An operator whose auth depends on
  `env` or `apiKeyHelper` in user settings loses it — README must say so, and the fix is to
  export the variable, not to re-enable user settings.
- `bypassPermissions` appears nowhere in `internal/`.

### 6.3 Secret deny rules (F4)

The deny patterns of `internal/projectctx/secrets.go` (`.env`, `.env.*`, `*.pem`, `*.key`,
`credentials*`, `secrets*`) plus `context.extra_deny` become provider-neutral data on the
request (`Request.DenyRead []string`, filled by the call sites from one exported function in
`projectctx`, so there is exactly one list). The claude adapter renders each segment
pattern `p` as `Read(**/p)` and `Read(**/p/**)` in
`--settings '{"permissions":{"deny":[...]}}'`. A Read deny rule also blocks Edit/Write on
the same path. Verified: `Read(**/.env*)` blocks `Read .env` and makes `Grep SECRET_TOKEN`
return nothing, while other files stay readable.

This is enforcement for the built-in file tools only; it is complete here because no
profile has a shell (P1).

### 6.4 codex and gemini (F9)

Neither CLI is installed, so nothing about them can be verified. Both declare
`Supports(...) == false` for every authority. Consequence: any route to them fails the
fail-fast check with exit 2 and a message naming the action, the provider and the remedy
(route to claude, or verify a translation). The intended translations, for whoever installs
them, are recorded as comments in the adapters:

- codex: `exec --sandbox read-only` (read), `--sandbox workspace-write` (edit; network is off
  by default), `--ask-for-approval never`. Caveat: codex's read-only sandbox can read the
  whole disk (e.g. `~/.ssh`) through its shell, and the model provider sees what it reads —
  `read` is therefore **not** equivalent to claude's and needs its own analysis before
  `Supports` may return true.
- gemini: `--approval-mode default` plus a policy-engine deny for shell tools; its headless
  policy handling has open issues (google-gemini/gemini-cli#20469). Same rule.

### 6.5 Fail-fast position

`config.Load` → `Validate` → `ValidateDependencies` → **`ValidateAuthorities`**, all before
any provider call. `ValidateAuthorities(cfg, checker)` walks every configured route
(the six actions, translation, an LLM decision provider) and asks
`checker.Supports(providerID, authority)`; `*provider.Registry` implements the checker
(registry construction makes no calls and moves above the check). Because authority does
not depend on mode (§5.2), this single check is complete — there is no second,
post-mode-selection pass. `orchestrator validate` runs it too.

`*apperr.AuthorityError` maps to exit code 2 (`ConfigOrDependency`): routing an action to a
provider that cannot enforce it is misconfiguration.

## 7. Orchestrator-side guards

### 7.1 Environment scrub (F8)

`execution.Spec` gains `Env []string` (nil = inherit, unchanged for git calls).
`execution.ScrubbedEnv()` returns the process environment minus a fixed denylist of
parent-agent-session variables: `CLAUDECODE`, `CLAUDE_CODE_MESSAGING_SOCKET`,
`CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`,
`CLAUDE_PID`. Used by every provider adapter **and** by `testexec` (agent-written test code
must not reach the parent session either). Names are exact, never prefixes.

Why not an allowlist: with no shell tool, a provider step cannot read its environment, so
an allowlist protects nothing for the model, while it is the part most likely to break auth
(v1 already found three missing variables). The one case where environment does matter —
agent-written code run by `test` — is what §8 addresses.

### 7.2 Clean worktree and `pre_run_ref` (F7)

When the built workflow contains `implement` and `security.require_clean_worktree` is true
(default), a dirty worktree fails the run **before the approval prompt** with
`*apperr.ConfigError` (exit 2), naming both remedies (commit/stash, or set the key to `false`).
Workflows without `implement` keep today's warning. The decision is taken after
`workflow.Build` — the first point where "contains implement" is known — but the tree's
state is read at startup, before the run directory exists: `.orchestrator/runs/` may live
inside the worktree, and the run must not count its own files as uncommitted changes.

Before the first step, `run.PreRunRef` records `git rev-parse HEAD` (clean tree) or, when a
dirty tree is allowed by config, a **snapshot commit** of everything the run starts on top
of — tracked changes, deletions and untracked files, minus ignored files and the run
directory — built on a throwaway index (`GIT_INDEX_FILE`: `read-tree HEAD`, `add -A`,
`write-tree`, `commit-tree -p HEAD`). The working tree, the real index, the stash list and
all refs stay untouched. (`git stash create` was rejected: it leaves untracked files out, so
the "restore point" would silently lose them.) The commit is unreferenced, so `git gc` may
prune it after its grace period. Persisted in `run.json`, printed as
`Restore point: <sha>`; failing to record it is a warning, not a failure.

### 7.3 Protected-path guard after `implement` (F6)

Around every `implement` call, the guard takes a snapshot before and compares after —
before the outcome is accepted, and so before `test` can execute anything:

1. Files that differ from HEAD **or are ignored** (`git status -uall
   --ignored=traditional`, limited to the protected patterns as glob pathspecs, so files are
   listed individually and ignored build output is not walked) are matched against the
   protected patterns. Ignored files matter: this repository ignores its own
   `orchestrator.yaml`, and Claude ignores `.claude/settings.local.json` — both invisible to
   a plain status, both able to change what runs next. The patterns: `.claude/**`, `.mcp.json`,
   `.github/workflows/**`, `.gitlab-ci.yml`, `.envrc`, `.vscode/**`
   (`domain.DefaultProtectedPaths`), the in-use config file when it is inside the worktree,
   plus `security.protected_paths`. Patterns match at any depth (`.claude/**` also covers
   `sub/.claude/…`). Each matching file's content is hashed, so a protected file that was
   already changed before the call (possible only with `require_clean_worktree: false`) is
   a violation only if the call touches it.
2. A fingerprint of the repository config (from `--git-common-dir`, so worktrees work) and
   of every non-`.sample` hook in the effective hooks directory (`--git-path hooks`, which
   honours `core.hooksPath`) must be unchanged — git status never reports `.git/` itself.

A violation fails the step with `*apperr.WorkflowError` (exit 4) listing the paths; the run
stops before `test`. The changes stay on disk for inspection; the message prints the restore
point. The guard fails closed: a dispatcher without one, or a snapshot that cannot be taken,
refuses `implement` before the provider call. Provider-neutral: it checks the result, not
the CLI.

In `manual` mode, the gate before the step that follows a completed `implement` first prints
the working tree's changes (`git status --short`, run directory excluded) and
`Protected paths: unchanged`.

### 7.4 Untrusted text in prompts

Repository-derived spans in action prompts (the git diff in review, the test stdout/stderr
in an implement retry) go through one `fence` helper: a delimiter with a 128-bit random
nonce (`crypto/rand`) plus a line stating the content is data. Content containing the
drawn delimiter gets a fresh nonce. The task
description is operator input and is not fenced. Defense in depth only — P1/P2 are what make
a followed injection achieve little.

## 8. Test step isolation (optional, phase 3)

F5 is the remaining real hole: `test` runs agent-written code with the operator's full
authority. The orchestrator owns this process, so it can isolate it itself, provider-neutrally:

```yaml
test:
  command: "go test ./..."
  sandbox: none            # none (default) | bwrap
```

`bwrap`: `testexec` runs
`bwrap --ro-bind / / --tmpfs $HOME --bind <repo> <repo> --ro-bind <each test.ro_binds>
--bind <each test.rw_binds> --dev /dev --proc /proc --unshare-net --unshare-pid
--die-with-parent --chdir <repo> sh -c <command>` with `ScrubbedEnv()`. Network off,
home hidden, writes only to the repository and explicitly bound caches (e.g. `GOCACHE`,
module cache read-only). Mount order is load-bearing: bwrap applies mounts in order, so
`--tmpfs $HOME` must come **before** the repository and cache binds, or it would hide a
repository that lives under `$HOME`.

Preconditions, checked in `ValidateDependencies` when `sandbox: bwrap`: `bwrap` on PATH and
a one-shot probe `bwrap --ro-bind / / --unshare-net true` succeeds. On Ubuntu ≥ 24.04 the
default AppArmor policy blocks it (observed here: `setting up uid map: Permission denied`)
until an AppArmor profile for `bwrap` is installed — the error message must say exactly that.

This amends MVP.md §30 ("no OS-level sandboxing") for the test step only; that amendment is
part of T09. Until T09 lands, `none` is the behavior and README states F5 as unprotected.

## 9. Configuration

```yaml
security:
  require_clean_worktree: true   # refuse workflows with implement on a dirty worktree
  protected_paths: []            # extra glob patterns implement must not change

test:
  command: "go test ./..."
  sandbox: none                  # T09: none | bwrap
  ro_binds: []                   # T09: e.g. ["~/go/pkg/mod"]
  rw_binds: []                   # T09: e.g. ["~/.cache/go-build"]
```

No knob widens a step's authority. `require_clean_worktree` needs `*bool` in the YAML shape
(absent → true, explicit false → false).

## 10. What is not protected (goes into README verbatim)

- **Agent-written code runs in `test`** with your authority, unless `test.sandbox: bwrap`.
  Reviewing the diff in `manual` mode before `test` is the non-sandboxed mitigation.
- **Repository contents go to the model provider** — that is the job. Only the secret deny
  list is withheld, and only from the file tools.
- **Prompt injection is reduced, not eliminated.** Its possible effects are limited to what
  the step's tools allow: a wrong analysis, or a wrong edit inside the repository, which the
  guard, `review` and the diff expose.
- **`test.command` is trusted config** executed through `sh -c`. Running on a repository you
  do not control additionally needs: `--config` from outside the repository, `test.sandbox:
  bwrap`, and ideally the whole run in a container. That is outside this design's scenario.
- **A hostile provider CLI binary** is out of scope.

## 11. Verification

Rule: a profile is declared supported only after a live test shows both that the permitted
thing works and that the forbidden thing is refused, against a named CLI version (T07 turns
the manual checks below into a build-tagged suite).

| # | CLI | check | result | date |
|---|---|---|---|---|
| V1 | claude 2.1.278 | `--restricted` refuses `bypassPermissions` | ✅ `Error: bypassPermissions not supported in restricted mode` | 2026-09-21 |
| V2 | claude 2.1.283 | `dontAsk` + `--permission-prompts none`, Bash in `--tools`, asked to `touch` a file | ✅ denied immediately, no file, no hang | 2026-09-27 |
| V3 | claude 2.1.283 | `edit` profile writes `./inside.txt` | ✅ written | 2026-09-27 |
| V4 | claude 2.1.283 | `edit` profile writes outside the working dir | ✅ refused: "--restricted confines the file tools to the working directory" | 2026-09-27 |
| V5 | claude 2.1.283 | `edit` profile writes `.git/hooks/pre-commit` | ✅ refused by `dontAsk` | 2026-09-27 |
| V6 | claude 2.1.283 | `read` + `Read(**/.env*)` deny: Read `.env`, Grep for its content, Read `a.txt` | ✅ refused / no match / readable | 2026-09-27 |
| V7 | claude 2.1.283 | native sandbox with `failIfUnavailable: true` | ✅ refuses to start (socat missing; bwrap blocked by AppArmor userns restriction) — fail-closed works, sandbox itself unavailable here | 2026-09-27 |
| V8 | claude 2.1.283 | `none` argv exactly as built (`--tools ""` then `--model`, prompt last): completes, cannot quote a file sentinel | ✅ | 2026-09-27 |
| V9 | claude 2.1.283 | `web` argv as built: WebFetch works (`example.com` title), no file tool | ✅ | 2026-09-27 |
| V10 | claude 2.1.283 | planted `.claude/settings.json` hooks (SessionStart, PreToolUse) and `.mcp.json` server do not run; positive control: the same files do act under `--setting-sources project` / `--mcp-config` | ✅ | 2026-09-27 |
| V11 | claude 2.1.283 | `read` argv as built: WebFetch not among its tools; `.env` denied | ✅ | 2026-09-27 |
| V12 | codex, gemini | anything | ⬜ not installed | — |

V2–V6 and V8–V11 are repeatable as `go test -tags security ./test/security/...` (T07): six
tests, each negative paired with a positive control, run through the adapter itself (same
argv, same scrubbed environment); passed twice in a row on 2.1.283, 2026-09-27, ~30 s and
about a dozen `haiku` calls per run. A failing case flips that authority to unsupported in
`claude.Supports` until fixed.

## 12. Acceptance

1. `grep -rn bypassPermissions internal/` is empty.
2. Every `provider.Request` in the codebase carries a non-empty authority from §5.1; an
   empty one is refused without spawning a process.
3. claude argv per profile matches §6.2 exactly (full-slice tests), prompt last and preceded
   by `--model`.
4. A route to a provider that does not support its authority fails `run` and `validate`
   with exit 2 before any AI call (asserted: zero `Execute` calls).
5. `implement` changing a protected path or `.git/config`/hooks stops the run before `test`.
6. A workflow with `implement` on a dirty worktree fails before the approval prompt;
   `run.json` has `pre_run_ref` for every run that executes steps.
7. Provider and test children receive no `CLAUDECODE`/`CLAUDE_CODE_MESSAGING_*`/… variable.
8. `run.json` records the authority of every AI step execution (`provider.authority`); an
   `authority:` line printed after the workflow line, before approval, shows each step's
   authority and the test command.
9. V2–V11 pass in the live suite (T07); README, MVP.md §38 and CLAUDE.md state the model and §10.
10. `internal/workflow` and `internal/policy` still import only `domain`; existing
    integration scenarios pass except assertions on the old argv.
