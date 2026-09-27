# Spec: Execution authority and permission policy

Status: **accepted** — all §13 questions resolved (see `DECISIONS.md`); ready to implement
via the task breakdown in `tasks/`.
Scope: how much authority each provider CLI invocation is granted, and how that authority
is chosen, enforced, audited, and documented. No change to classification, workflow
selection, transitions, limits, static routing, or what is trusted (exit codes + git diff).
Extends MVP.md §3 (architecture), §7 (fail fast), §13–15 (provider abstraction), §22
(domain model), §27 (CLI output); adds a new normative §36.

---

## 1. Reported symptom

Every provider invocation — classification included — runs fully unattended with all
permission checks disabled:

```go
// internal/provider/claude/client.go:27
func buildArgs(model, prompt string) []string {
	return []string{
		"--print",
		"--permission-mode", "bypassPermissions",
		"--model", model,
		prompt,
	}
}
```

That single constant is the orchestrator's entire authority model. It grants arbitrary
command execution, unrestricted filesystem writes (not confined to the repository),
network egress, and use of every credential in the inherited environment — to all six
actions, in every run mode.

## 2. Findings (each is an independent defect)

| # | Finding | Location |
|---|---------|----------|
| F1 | `bypassPermissions` is hardcoded for **every** action. Five of six need far less: `analyze`/`plan`/`review`/`answer` only read the repo and emit JSON; `classify` reads only `task.Description` and needs no tools at all. Only `implement` legitimately writes. | `internal/provider/claude/client.go:30,42` |
| F2 | `domain.RunMode` never reaches `provider.Request`. Authority is identical in `manual`, `accept-edits`, and `auto`. `manual` gates only *between* steps; once a step starts the CLI is unconstrained. `accept-edits` is behaviorally identical to `auto` — the name promises a permission level that is never delivered. | `internal/app/app.go:212-265`, `internal/interaction/gate.go:26`, `internal/provider/client.go:12` |
| F3 | Untrusted repository content reaches a model running with all checks disabled. `executeReview` embeds the real `git diff`; `analyze`/`implement` read repo files. In any repo accepting contributions that content is attacker-influenced, making prompt injection a direct path to code execution with the operator's credentials. | `internal/action/review.go:17`, `internal/action/prompts.go` |
| F4 | The child process inherits the **entire** environment. `execution.Spec` has no `Env` field, so `SSH_AUTH_SOCK`, `AWS_*`, `GH_TOKEN`, and every other credential are visible to a CLI that can run arbitrary commands. | `internal/execution/process.go:25,47` |
| F5 | The spawned CLI loads the **target repository's** agent configuration: no `--strict-mcp-config`, no `--setting-sources`. A malicious repo's `.claude/settings.json` hooks, `.mcp.json` servers, and `CLAUDE.md` all take effect, and `-p` documentedly skips the workspace-trust dialog. | `internal/provider/claude/client.go:27` |
| F6 | Codex and Gemini declare **no** policy at all — no `--sandbox`, no `--ask-for-approval`, no `--approval-mode`. Whatever those CLIs default to becomes the security policy by accident, and it differs per provider, so identical config yields different authority depending on routing. | `internal/provider/codex/client.go:29`, `internal/provider/gemini/client.go:29` |
| F7 | The `test.command` trust assumption is weaker than documented. It is shell-executed as "trusted operator configuration", but the default config path resolves relative to cwd, so running the orchestrator inside an untrusted repo that ships an `orchestrator.yaml` makes `test.command` attacker-controlled shell. | `internal/testexec/runner.go:25`, `internal/app/app.go:35` |
| F8 | No spend bound per call or per run. `--max-budget-usd` is unused; only `max_steps`/`max_fix_iterations` bound the run. | `internal/provider/claude/client.go:27` |
| F9 | The effective authority of a run is neither printed nor persisted. `run.json`, `workflow.json`, and the artifacts record provider/model but not what the CLI was permitted to do, so a completed run cannot be audited. | `internal/domain/run.go`, `internal/persistence/filesystem.go` |
| F10 | A dirty worktree is only warned about, so `git diff` is not a reliable undo boundary for a write-capable step — contradicting MVP.md §31.3's premise that git is the source of truth for changes. | `internal/app/app.go:155-159` |
| F11 | *(added during resolution)* The `CLAUDE_CODE_*` wildcard originally proposed for §8.5's allowlist would forward the **parent** Claude Code session's IPC channel — `CLAUDE_CODE_MESSAGING_SOCKET` plus `CLAUDE_CODE_MESSAGING_TOKEN` — into the spawned CLI, handing a child process a live authenticated channel to the session that launched it. Observed set on the development machine. The allowlist must therefore be explicit names, never a prefix wildcard. | §8.5 as originally drafted |
| F12 | *(added during resolution)* `claude --permission-prompts` **defaults to `host`**, verified on 2.1.278. Removing `bypassPermissions` without passing `--permission-prompts none` would therefore convert every unattended step from "silently allowed" into "blocks forever waiting for a host that is not listening" — a hang, not a denial. `none` is mandatory in every non-`ApprovalAsk` policy, not decoration. | `claude --help`, 2.1.278 |

## 3. Goals

- G1 **Least privilege by construction.** Each action receives the minimum authority its
  job requires, chosen by a deterministic table in Go code — never by the model, never by
  accident, never by provider default.
- G2 **Fail closed.** A provider that cannot express a requested policy is a hard error
  before the call, not a silent downgrade.
- G3 **Mode means something.** The selected run mode changes actual authority, not just
  how often the user is asked.
- G4 **No unattended bypass.** `bypassPermissions` (and per-provider equivalents) is
  reachable only through an explicitly named opt-in that also requires evidence of
  isolation, and is recorded in the run.
- G5 **Auditable.** The effective policy for every step is printed and persisted, so
  "what was this run allowed to do?" is answerable after the fact.
- G6 **Documented.** The policy is described normatively in `MVP.md`, operationally in
  `README.md`, and as an invariant in `CLAUDE.md`; the documented table is verified
  against code by a test (§10).
- G7 **Architecture preserved.** Authority is provider-neutral in `domain`/`workflow`/
  `action`; per-CLI flags exist only inside `internal/provider/<name>`.

## 4. Non-goals

- No OS-level sandbox implementation (containers, seccomp, namespaces). The spec makes
  isolation *detectable and requestable*; providing it stays the operator's job (§8.6).
- No dynamic or model-chosen permissions. The action→policy table is fixed Go code, for
  the same reason workflows are (MVP.md §31.2).
- No per-user roles, no policy DSL, no signed policy files (MVP.md §30).
- No change to static provider routing (MVP.md §31.1) or to what is trusted (§31.4).
- No secret scanning or egress filtering of provider traffic.
- Prompt-injection defense is limited to §8.7 (fencing) plus least privilege; no
  classifier or filter over untrusted text.

## 5. Chosen approach and rejected alternatives

**Chosen: a provider-neutral capability policy, derived deterministically from
`(action, mode)`, translated to CLI flags only inside each adapter, enforced in four
independent layers.**

Layers, strongest-first in terms of risk removed per unit of work:

1. **Least privilege per action** (§8.2) — removes most exposure, provider-neutral,
   requires no interaction, works in CI.
2. **Mode-derived approval level** (§8.3) — makes `manual`/`accept-edits`/`auto` honest.
3. **Process hardening** (§8.5–8.7) — env allowlist, config isolation, prompt fencing,
   revertible worktree. Independent of any approval flow.
4. **Mid-run per-tool approval** (§8.8) — strongest control, inherently provider-specific,
   optional, declared as an adapter capability.

Rejected alternatives:

- **Mid-run approval as the primary mechanism.** Only `claude` can do it headlessly;
  `gemini` has no equivalent. It requires a TTY, breaks `--mode auto`/CI, and consumes the
  run-wide deadline while waiting on a human. Worse, approving every tool call in a long
  run degrades into rubber-stamping, which manufactures a false audit trail. It is the
  right *refinement* of least privilege, not a substitute for it — hence layer 4, not 1.
- **Configurable action→policy mapping in YAML.** A security policy that an operator can
  widen per action is a footgun, and it breaks the determinism the rest of the system is
  built on. The table is fixed; only the narrow knobs in §9 are configurable.
- **Keeping `bypassPermissions` with a scary warning.** A warning is not a control, and
  the warning would fire on every run, so it would be ignored.
- **A single global "strict/lax" switch.** Collapses six genuinely different authority
  requirements into one, so either `classify` is over-privileged or `implement` is broken.

## 6. Threat model

| Adversary / source | Reaches the system via | Mitigated by |
|---|---|---|
| Malicious or compromised repository content (source files, `CLAUDE.md`, `README`) | `analyze`/`implement` reading files | §8.2 read-only, no exec; §8.6 settings isolation; §8.7 fencing |
| Attacker-authored `git diff` (contributed branch under review) | `executeReview` prompt | §8.2 review is read-only; §8.7 fencing; MVP.md §31.4 (only exit codes trusted) |
| Repo-supplied agent configuration (`.claude/settings.json` hooks, `.mcp.json`, plugins) | CLI startup in the repo dir | §8.6 `--strict-mcp-config`, `--setting-sources`, `--restricted` |
| Repo-supplied `orchestrator.yaml` (`test.command` via `sh -c`) | config load from cwd | §8.9 config-provenance warning + open question Q6 |
| Model error or hallucinated destructive action (not adversarial) | any write/exec-capable step | §8.2 confined writes, no exec by default; §8.4 clean-worktree + snapshot |
| Credential theft from the environment | any exec-capable step | §8.5 env allowlist |
| Cost exhaustion | loops within `max_steps` | §8.10 advisory budget |

Explicitly **out of the threat model**: a hostile provider CLI binary itself, kernel-level
escape from an operator-supplied sandbox, and a compromised operator config passed via an
explicit `--config` path outside the repo.

## 7. Terminology

- **Policy** — the provider-neutral authority granted to one provider invocation.
- **Mandatory field** — a policy field an adapter MUST be able to enforce; inability is a
  hard error (§8.1).
- **Advisory field** — a best-effort field an adapter MAY ignore, recorded as ignored in
  the audit record.
- **Capability** — what an adapter declares it can enforce, checked before any AI call.
- **Grant** — the user's approval, up-front (§8.8a) or per tool call (§8.8b).

## 8. Design

### 8.1 `domain.Policy` and the mandatory/advisory split

New file `internal/domain/policy.go`. No provider knowledge, no config import.

```go
// Approval is how a permission decision the policy does not pre-answer is resolved.
type Approval string

const (
	// ApprovalDeny answers every such decision with "no", without blocking. This is
	// the only approval level valid without an interactive terminal.
	ApprovalDeny  Approval = "deny"
	// ApprovalAsk suspends the call and asks the user (§8.8b).
	ApprovalAsk   Approval = "ask"
)

// Policy is the authority granted to one provider invocation. Fields are split into
// mandatory (an adapter that cannot enforce the field must refuse the call — §8.1) and
// advisory (best effort; an adapter may ignore the field, and records that it did).
type Policy struct {
	// --- mandatory ---
	ReadFiles  bool     // may read files under WorkingDir
	WriteFiles bool     // may create/modify files under WorkingDir, and nowhere else
	Exec       bool     // may run commands
	// ExecAllowlist, when non-empty, restricts Exec to these command patterns
	// (e.g. "go build *"). Non-empty with Exec==false is invalid.
	ExecAllowlist []string
	Network       bool   // may make outbound requests from a tool (WebFetch and similar)
	Approval      Approval
	// WriteRoots is the absolute set of directories writes may touch. Always exactly
	// the run's repository root unless Bypass is set.
	WriteRoots []string
	// InheritAgentConfig, when false, forbids loading user/project/local agent
	// settings, MCP servers, hooks, and plugins from the target repository (§8.6).
	InheritAgentConfig bool
	// Bypass disables all of the above. Only reachable via §8.4's explicit opt-in.
	Bypass bool

	// --- advisory ---
	MaxBudgetUSD float64 // 0 = unset
}
```

Rationale for the split: `MaxBudgetUSD` is expressible by `claude --max-budget-usd` and
(as far as is documented) by neither `codex` nor `gemini`. Making it mandatory would
render those providers unusable for a cost control that is a guardrail, not a security
boundary. Making authority fields advisory would be the actual security bug. The split is
therefore explicit in the type, not left to adapter discretion.

`Policy.Validate()` rejects incoherent policies (`WriteFiles` without `ReadFiles`;
`ExecAllowlist` without `Exec`; `WriteFiles` with empty `WriteRoots`; `Bypass` combined
with any other restriction) so a wiring bug cannot produce a policy whose meaning is
ambiguous.

### 8.2 The fixed action→policy table (least privilege)

`domain.PolicyFor(action ActionType, mode RunMode, repoRoot string) Policy` is a pure
function over a fixed table — the same shape and the same guarantee as
`workflow.Build`: identical inputs always produce identical authority.

| action | read | write | exec | network | rationale |
|---|---|---|---|---|---|
| `classify` | no | no | no | no | consumes `task.Description` only, returns JSON; any tool is pure downside |
| `analyze` | yes | no | no | no | inspects the repo, returns JSON |
| `plan` | yes | no | no | no | inspects the repo, returns JSON |
| `review` | yes | no | no | no | the diff is already supplied in the prompt; MVP.md §31.5 |
| `answer` | yes | no | no | no | answers a question about the repo |
| `implement` | yes | **yes** (repo only) | no by default (§9 opt-in) | no | the only action that may change code |
| `test` | — | — | — | — | no AI call; the orchestrator runs the command itself |

`InheritAgentConfig` is `false` for every action. `WriteRoots` is `[repoRoot]` for
`implement` and empty for everything else.

**Why `implement` has no `exec` by default.** The workflow is `implement → test`, and
`test` is executed by the orchestrator (`internal/testexec`), which is the only trusted
source of pass/fail (MVP.md §18/§31.4). An implement step therefore never needs to run
anything to do its job, and exec is the single largest jump in authority available. Where
a project genuinely needs a formatter or code generator to run, §9's
`security.implement_exec_allowlist` grants exactly those commands and nothing else.

### 8.3 Mode→approval mapping

| mode | approval | effect |
|---|---|---|
| `plan` | — | nothing executes (unchanged: `app.go:236`) |
| `manual` | `ApprovalAsk` | per-step gate (unchanged) **plus** per-tool-call approval where the adapter supports it (§8.8b) |
| `accept-edits` | `ApprovalDeny` | writes inside `WriteRoots` are pre-approved by the policy; anything else is denied without prompting |
| `auto` | `ApprovalDeny` | fully unattended, deny-by-default. **Not** bypass |

`ApprovalDeny` is what makes unattended operation safe *and* non-hanging: a step that
wants authority it was not granted is refused immediately rather than blocking on a prompt
that nobody will answer. This is the property `bypassPermissions` was standing in for, and
it achieves it without granting the authority.

`ApprovalAsk` requires an interactive terminal. Selecting `manual` with a non-interactive
stdin is already an error in `interaction.TerminalPrompter.Ask`; the same error text
(pointing at `--mode auto`) covers this case.

### 8.4 The one escape hatch

New flag `--dangerously-bypass-permissions`. Rules, all mandatory:

1. Refused unless the process is plausibly isolated: either `ORCHESTRATOR_SANDBOX=1` is
   set in the environment, or `/.dockerenv` exists. Otherwise `*apperr.ConfigError`
   (exit 2) naming which of the two to satisfy.
2. Refused in combination with `--mode manual` (the two are contradictory).
3. Emits an unmissable banner on stderr before the first call.
4. Recorded as `"bypass": true` in `policy.json` and in every step's audit record, so a
   run performed this way is never mistaken for a constrained one.

Whether the isolation check is a hard refusal or a typed confirmation is Q1.

### 8.5 Environment allowlist (F4)

`execution.Spec` gains `Env []string`. `OSProcessExecutor` sets `cmd.Env = spec.Env` when
non-nil (nil keeps today's inheritance, so `repository` git calls and `testexec` are
unchanged unless explicitly opted in).

Provider invocations get an allowlist assembled in `internal/action`/`internal/classifier`
(not in the adapters — the *values* are environment policy, while only the provider knows
which auth variable it needs):

- Always: `PATH`, `HOME`, `TERM`, `LANG`, `LC_ALL`, `TMPDIR`, `USER`, `XDG_CONFIG_HOME`,
  `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` (and their lowercase spellings).
- Per provider, from a fixed map in `internal/provider`. **Explicit names only — no
  prefix wildcards** (F11):
  - `claude` → `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`,
    `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`,
    `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`, `CLAUDE_CODE_USE_BEDROCK`,
    `CLAUDE_CODE_USE_VERTEX`, `AWS_PROFILE`, `AWS_REGION`, `AWS_DEFAULT_REGION`,
    `CLOUD_ML_REGION`, `GOOGLE_CLOUD_PROJECT`, `ANTHROPIC_VERTEX_PROJECT_ID`.
  - `codex` → `OPENAI_API_KEY`, `OPENAI_BASE_URL`.
  - `gemini` → `GEMINI_API_KEY`, `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`,
    `GOOGLE_CLOUD_PROJECT`.
- Plus `security.env_allowlist` from config (§9), for project-specific needs such as
  `GOCACHE`, `GOPATH`, `GOFLAGS`.

Never forwarded, and denied even if named in `security.env_allowlist`: `SSH_AUTH_SOCK`,
`GH_TOKEN`, `GITHUB_TOKEN`, `GITLAB_TOKEN`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`,
`AWS_SESSION_TOKEN`, `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`,
`CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDECODE`, `CLAUDE_PID`, and
anything not matched above. The allowlist is name-based; the variable *names* forwarded are
recorded in the audit record and values are never logged (MVP.md §23's redaction rule).

Two honest limits, both of which belong in README's "what is not protected" (§10.2):

1. `HOME` must be forwarded — an OAuth-authenticated `claude` reads credentials from the
   config file/keychain, not the environment. Forwarding `HOME` therefore also exposes
   `~/.aws/credentials`, `~/.ssh`, `~/.config/gh` and every other on-disk credential to a
   step that can read files. The env allowlist removes the *easy* path (a variable in
   `os.Environ()` visible to any command), not all paths. Only §8.2's "no exec, no
   network" and the read-root confinement make that exposure inert.
2. Consequently `AWS_PROFILE` is forwarded (Bedrock routing needs it) while
   `AWS_SECRET_ACCESS_KEY` is denied. That asymmetry is deliberate — a profile name is not
   a secret — but it is not a boundary on its own.

### 8.6 Refusing repo-supplied agent configuration (F5)

`InheritAgentConfig: false` translates to, for `claude`: `--strict-mcp-config` (ignore
every MCP config but an explicit one), `--setting-sources` limited to none, and
`--restricted`, which additionally ignores user/project/local settings files, strips the
command-running tools and `WebFetch` unless `--tools` names them, confines the file tools
to the working directories, and refuses `bypassPermissions`. That last property is
valuable on its own: for read-only actions it makes the previous failure mode
unreachable even if a future edit reintroduced the flag.

For `codex`/`gemini` the equivalents are `--sandbox read-only` plus whatever
config-isolation flags those CLIs expose; see §12 — unverified translations must not ship
as verified.

### 8.7 Untrusted content in prompts (F3)

`internal/action/prompts.go` fences every span of repository-derived text (file contents,
`git diff`, test output) in an explicitly-labelled block stating that its contents are
data to be analyzed and never instructions to follow, with a nonce-suffixed delimiter so
the fence cannot be closed by the content itself:

```
<<<UNTRUSTED-DIFF-7f3a9c
...diff...
>>>UNTRUSTED-DIFF-7f3a9c
```

This is defense in depth, not a boundary: it reduces the chance that injected text is
*followed*, while §8.2 ensures that following it achieves nothing. The existing invariant
that only exit codes and the real diff are trusted (MVP.md §31.4) already limits the blast
radius of a compromised `review`, and this makes that reasoning explicit in the prompt
builders.

### 8.8 Grants

**(a) Up-front grant — default, all interactive modes.** After `workflow.Build`, the set
of steps and therefore the exact authority of the run is known. `interaction.
ReviewAndSelectMode` gains a policy summary printed immediately after the workflow, before
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

This deliberately reuses the existing approve/edit/cancel loop rather than adding a second
prompt: the plan the user approves now includes the authority it will run with. In
`--mode auto` the same block is printed (not prompted) so non-interactive runs still
record it in their output.

**(b) Mid-run per-tool approval — `manual` mode, adapter-capability-gated.** `claude`
supports two headless routes; the spec requires exactly one implementation, chosen at
implementation time on the criterion of which needs less plumbing:

- `--permission-prompt-tool <tool>` with `--mcp-config`: the orchestrator runs a minimal
  MCP server exposing one approval tool; the CLI calls it for each decision. The flag
  parses on the installed CLI (2.1.278) but the full round-trip is **not** yet verified
  (§12).
- `--input-format stream-json --output-format stream-json` with `--permission-prompts
  host`: the orchestrator answers `can_use_tool` control requests on the wire.

Either way the prompt is rendered by `internal/interaction` on the orchestrator's own
stdin/stdout — which stays free, because `execution.Spec.Stdin` is unset for provider
calls — so there is exactly one place that talks to the user.

Three requirements specific to this codebase:

1. **Deadline.** The run-wide `execution.timeout` context is created at `app.go:141`, so
   time spent waiting on a human would consume the run budget and kill the run mid-prompt.
   Time spent blocked on an approval prompt MUST be excluded from the run and action
   deadlines. Mechanism is Q3 (extend the deadline by the measured wait, versus a separate
   non-deadlined approval context).
2. **Timeout and default.** Each prompt has its own bounded wait and denies on expiry.
   Never allow-on-timeout.
3. **Audit.** Every decision (tool, argument summary, allow/deny, who, elapsed) is
   appended to `logs/approvals.log` in the run directory.

If the resolved policy requires `ApprovalAsk` and the adapter does not declare that
capability, the run fails per §8.1 with a message naming the provider, the action, and the
two ways out (`--mode accept-edits`, or route that action to a provider that supports it).

### 8.9 Fail-closed capability checking (G2) and where it runs

`provider.Client` gains:

```go
// Capabilities declares what the adapter can actually enforce. Anything not declared
// causes Supports to reject the policy, and the orchestrator refuses the call.
type Capabilities struct {
	ConfinedWrite      bool
	DenyExec           bool
	ExecAllowlist      bool
	DenyNetwork        bool
	RefuseAgentConfig  bool
	ApprovalAsk        bool
	BudgetUSD          bool // advisory
}

type Client interface {
	Execute(ctx context.Context, req Request) (Result, error)
	Capabilities() Capabilities
}
```

`provider.Supports(caps, p domain.Policy) error` returns a typed
`*apperr.PolicyError` naming the unsupported field. Ordering, honoring MVP.md §7's
fail-fast-before-any-AI-call rule:

1. `config.Load` → `config.Validate` → `config.ValidateDependencies` (unchanged).
2. **New** `config.ValidatePolicies(cfg, registry, mode)` — for every configured action,
   `Supports(registry[provider].Capabilities(), PolicyFor(action, mode, root))`. Runs
   before the classifier object is constructed.
   - With `--mode` given, `mode` is known from `parseArgs` and the check is complete.
   - Without `--mode`, the mode is chosen interactively *after* classification, so this
     pass validates every mode-independent field, and the mode-dependent `Approval` field
     is re-checked immediately after `resolveMode` returns — still before any workflow
     step. Classification itself runs under the `classify` policy (no tools, no approval),
     which every adapter must support, so no AI call precedes a completed check of the
     policy it runs under.
3. Belt and braces: `Execute` re-checks `Supports` on entry and refuses, so a future call
   path that skips step 2 cannot leak authority.

`*apperr.PolicyError` maps to `exitcode.ConfigOrDependency` (2) — it is a misconfiguration
(wrong provider for the required authority), not a provider runtime failure.

### 8.10 Per-CLI translation

Translation lives **only** in each adapter's `buildArgs`, per CLAUDE.md. `redactedArgs`
already mirrors `buildArgs`, so the policy flags appear in `--verbose` output and in
`*apperr.ProviderError` messages with no extra work — which is most of G5.

`claude` (flag availability verified on 2.1.278; enforcement verified per §12). Note F12:
`--permission-prompts` defaults to `host`, so `none` is required in every row that is not
`ApprovalAsk` — omitting it turns a denial into an indefinite hang.

| policy | argv |
|---|---|
| no tools (`classify`) | `--print --tools "" --permission-prompts none --strict-mcp-config` |
| read-only | `--print --restricted --tools Read,Grep,Glob --permission-prompts none --strict-mcp-config` |
| write in repo | `--print --restricted --tools Read,Grep,Glob,Edit,Write --permission-mode acceptEdits --add-dir <root> --permission-prompts none --strict-mcp-config` |
| + exec allowlist | adds `--allowed-tools 'Bash(go build *)' ...` and the `Bash` tool |
| `ApprovalAsk` | `--permission-prompts host` plus the §8.8b mechanism, replacing `--permission-prompts none` |
| `Bypass` | `--permission-mode bypassPermissions` (and no `--restricted`, which refuses it) |
| `MaxBudgetUSD` | `--max-budget-usd <amount>` |

`codex` — `exec --sandbox read-only\|workspace-write --ask-for-approval never\|on-request`,
`--cd <root>`. `gemini` — `--approval-mode default\|auto_edit`, plus its sandbox flag.
Both are **unverified** (neither executable is installed here; the existing package
comments already say the argv mapping is best-effort). Per §12 an unverified capability
MUST be declared `false`, which makes those providers refuse anything beyond their
verified policy rather than silently running with unknown authority.

### 8.11 Revertible worktree (F10)

`security.require_clean_worktree` (§9), default `true`: a run containing a write-capable
step refuses to start on a dirty worktree, upgrading today's warning
(`logging.DirtyRepoWarning`) to an error with the two ways out (commit/stash, or set the
key to `false`). Additionally, before the first `implement` step the orchestrator records
`git stash create`'s ref (or `git rev-parse HEAD`) in `run.json` as `pre_run_ref`, so
every run has a named point to return to. This makes MVP.md §31.3's "git is the source of
truth" operationally true rather than aspirational.

### 8.12 Config provenance (F7)

At `validate` time the resolved absolute config path is printed. When it resolves *inside*
the repository worktree **and** `test.command` is set, a warning states that
`test.command` is shell-executed and that a config inside an untrusted repository is
therefore untrusted input. Whether this should be a refusal is Q6.

### 8.13 Audit record (F9, G5)

- `domain.StepExecution` gains `Policy domain.Policy`, so `run.json` records what each
  step was allowed to do (append-only history is preserved).
- New `policy.json` per run, alongside `classification.json`/`workflow.json`: the resolved
  per-action table, mode, environment variable *names* forwarded, ignored advisory fields,
  bypass flag, `pre_run_ref`.
- `logs/approvals.log` when §8.8b is active.
- `--verbose` prints the policy line per step; the argv echo shows the flags.

## 9. Configuration surface

Deliberately minimal — the action→policy table is **not** configurable (§5). New optional
`security:` block; every key has a safe default, so existing configs stay valid:

```yaml
security:
  # Extra environment variable names forwarded to provider CLIs, added to the built-in
  # base list and the provider's own auth variables (§8.5). Names only.
  env_allowlist: [GOCACHE, GOPATH, GOFLAGS]

  # Commands the implement action may run. Empty (default) means implement may run
  # nothing at all. Each entry is a command pattern, not a shell line.
  implement_exec_allowlist: ["go build ./...", "gofmt -w *"]

  # Refuse to start a run with write-capable steps on a dirty worktree (§8.11).
  require_clean_worktree: true

  # Advisory per-call spend cap in USD. 0 disables. Ignored by providers that cannot
  # express it, which is recorded in policy.json (§8.1).
  max_budget_usd: 0
```

`config.Validate` additions: `env_allowlist` entries must be valid variable names and must
not name a denied credential variable (§8.5); `implement_exec_allowlist` entries must be
non-empty and must not contain shell metacharacters (`;`, `|`, `&`, backticks, `$(`) —
they are argv patterns, not shell, and a pattern containing shell syntax means the author
misunderstood the field.

## 10. Documentation (G6) — required deliverable, not optional

The policy is only useful if an operator can find out what it is without reading Go.
All five of these are part of the change, not a follow-up:

**10.1 `MVP.md` — new normative §36 "Execution authority and permission policy."**
CLAUDE.md designates MVP.md the source of truth, so the table lives there. Contents:
`domain.Policy` and the mandatory/advisory split (§8.1); the fixed action→policy table
(§8.2) as a table; the mode→approval mapping (§8.3); the fail-closed rule and its
position in the fail-fast ordering (§8.9), cross-referenced from §7; the escape hatch and
its preconditions (§8.4); the audit record (§8.13). Also update §13–15 to state that
adapters declare capabilities and translate policy, §22 for the new domain types, §25 for
the approval-wait deadline exclusion, §27 for the authority block in CLI output, and §30
to record that a policy DSL and OS-level sandboxing are out of scope.

**10.2 `README.md` — new "## Security model" section**, placed after "## Configuration"
and before "## Usage", written for an operator deciding whether to run this on a given
repository. Contents:

- One-paragraph statement of the model: every action gets the least authority its job
  needs; the table is fixed in code; a provider that cannot enforce it refuses to run.
- The action→authority table, copied verbatim from the generated fragment (10.5).
- What each run mode actually permits (§8.3), explicitly correcting the prior behavior
  where mode did not affect authority.
- What is *not* protected: prompt injection is mitigated, not eliminated; there is no OS
  sandbox; `test.command` is shell-executed trusted config; a hostile provider binary is
  out of scope. An operator who believes the tool is safe against things it is not
  protected against is worse off than one who knows the limits.
- `--dangerously-bypass-permissions`: what it disables, the isolation precondition, and a
  plain statement that it is for disposable containers only.
- The environment allowlist: the base list, how to extend it, and which variables are
  never forwarded.
- Where to look afterwards: `policy.json`, `run.json` step policies, `logs/approvals.log`.
- A short "if you are running this on a repository you do not fully trust" checklist:
  pass `--config` from outside the repo, keep `implement_exec_allowlist` empty, use
  `accept-edits` or `manual`, run in a container, start from a clean worktree.

**10.3 `CLAUDE.md` — new invariants** under "Non-negotiable invariants", since that file
is what keeps future work aligned:

- Never `bypassPermissions` (or a per-provider equivalent) outside the §8.4 opt-in.
- Authority is chosen by `domain.PolicyFor`, a fixed table — never by the model, never
  per-provider default, never configurable per action.
- Policy→flag translation exists only in `internal/provider/<name>/buildArgs`; `domain`,
  `workflow`, and `action` stay provider-neutral.
- An adapter that cannot enforce a mandatory policy field must refuse the call. Silent
  downgrade is the one unacceptable failure mode.
- Add to the "Run artifacts" list: `policy.json`, `logs/approvals.log`.

**10.4 `orchestrator.example.yaml`** — the `security:` block from §9, with the same
commented explanations, and with `implement_exec_allowlist` shown empty (its default) and
the opt-in example commented out, so copying the example never widens authority.

**10.5 A generated fragment, so the docs cannot drift.** `domain` gains
`PolicyTableMarkdown() string`, rendering the §8.2 table from the same data
`PolicyFor` uses. `TestDocsPolicyTableCurrent` reads `README.md` and `MVP.md`, locates the
table between `<!-- BEGIN policy-table -->` / `<!-- END policy-table -->` markers, and
fails if it differs from `PolicyTableMarkdown()`. A stale security table in the README is
a real defect — this makes it a failing test rather than a discovery six months later.

Documentation acceptance is part of §11's gates: the change is not done until an operator
reading only `README.md` can state, correctly, what `implement` is allowed to do.

## 11. Files to change

| File | Change |
|---|---|
| `internal/domain/policy.go` | **new**: `Policy`, `Approval`, `Validate`, `PolicyFor` table, `PolicyTableMarkdown` |
| `internal/domain/run.go` | `StepExecution.Policy`, `Run.PreRunRef` |
| `internal/provider/client.go` | `Request.Policy`, `Capabilities`, `Client.Capabilities()`, `Supports`, per-provider env-var map |
| `internal/provider/claude/client.go` | policy→argv translation in `buildArgs`/`redactedArgs`, capability declaration, remove hardcoded `bypassPermissions` |
| `internal/provider/codex/client.go`, `gemini/client.go` | same, with unverified capabilities declared `false` (§12) |
| `internal/execution/process.go`, `fake.go` | `Spec.Env`, `cmd.Env` wiring |
| `internal/apperr/errors.go` | `PolicyError` with provider/action/field/remedy rendering |
| `internal/exitcode/exitcode.go` | `PolicyError` → 2 |
| `internal/config/config.go`, `loader.go`, `validator.go` | `SecurityConfig`, defaults, validation per §9 |
| `internal/config/policies.go` | **new**: `ValidatePolicies` |
| `internal/action/context.go` | build `Policy` + env allowlist per call, pass in `Request`, record in `StepOutcome` |
| `internal/action/prompts.go` | untrusted-content fencing (§8.7) |
| `internal/classifier/classifier.go` | pass the no-tools `classify` policy |
| `internal/interaction/review.go` | authority block before the mode prompt (§8.8a) |
| `internal/interaction/approval.go` | **new**: per-tool approval prompt + `approvals.log` (§8.8b) |
| `internal/logging/progress.go` | authority block rendering, per-step policy line, bypass banner |
| `internal/persistence/filesystem.go` | `SavePolicy` → `policy.json`, `approvals.log` path |
| `internal/app/app.go` | `--dangerously-bypass-permissions`, isolation check, `ValidatePolicies` call + post-`resolveMode` recheck, clean-worktree enforcement, `pre_run_ref`, config-provenance warning, approval-wait deadline handling |
| `MVP.md` | new §36; updates to §7/§13–15/§22/§25/§27/§30 (§10.1) |
| `README.md` | new "## Security model" (§10.2) |
| `CLAUDE.md` | new invariants + artifact list (§10.3) |
| `orchestrator.example.yaml` | `security:` block (§10.4) |

## 12. Verification of per-CLI translations

A translation that has not been exercised against the real CLI is a guess, and a guess in
this layer silently grants or silently denies. Rules:

1. `Capabilities()` may declare a field `true` only once a test has demonstrated the
   corresponding flags actually enforce it against the real executable.
2. Until then the field is `false`, and `Supports` refuses — the provider becomes unusable
   for that policy rather than usable with unknown authority. With the example config
   (`review` → codex, `answer` → gemini) this means those routes fail fast with an
   actionable message until verified, which is the intended behavior and must be called
   out in the release note.
3. Verification suite (`test/security`, build-tagged, skipped under `-short`, needs a real
   CLI and network): read-only policy cannot write a file, cannot run a command, cannot
   fetch a URL; write policy can write inside the root and cannot write outside it
   (`/tmp`, `$HOME`); exec allowlist permits a listed command and refuses an unlisted one;
   `InheritAgentConfig: false` ignores a planted `.claude/settings.json` hook and a
   planted `.mcp.json`; the env allowlist is observed by asking the CLI to print a
   forwarded and a denied variable. Each is a positive **and** a negative assertion —
   "it didn't happen" is only evidence when the same harness shows it can happen under a
   wider policy.
4. Results recorded in the matrix below (provider × policy × verified date/CLI version),
   since flags change between CLI releases.

### 12.1 Verification matrix

Legend: **flags** = the flags parse and the invocation runs; **enforced** = a live test in
`test/security` demonstrates the restriction holds *and* that the same harness shows the
action succeeding under a wider policy.

| provider | CLI version | policy / claim | flags | enforced | date |
|---|---|---|---|---|---|
| claude | 2.1.278 | `--restricted` refuses `bypassPermissions` (`Error: bypassPermissions not supported in restricted mode`) | ✅ | ✅ (the refusal *is* the enforcement) | 2026-09-21 |
| claude | 2.1.278 | no-tools `classify`: `--print --tools "" --permission-prompts none --strict-mcp-config --setting-sources ""` parses and completes a call | ✅ | ⬜ T15 | 2026-09-21 |
| claude | 2.1.278 | `--permission-prompts` default is `host` (F12) | ✅ | n/a (documented default) | 2026-09-21 |
| claude | 2.1.278 | read-only: cannot write, cannot exec, cannot fetch | ⬜ | ⬜ T15 | — |
| claude | 2.1.278 | write-in-repo: writes inside root, refused outside (`/tmp`, `$HOME`) | ⬜ | ⬜ T15 | — |
| claude | 2.1.278 | exec allowlist admits a listed command, refuses an unlisted one | ⬜ | ⬜ T15 | — |
| claude | 2.1.278 | `InheritAgentConfig:false` ignores planted `.claude/settings.json` + `.mcp.json` | ⬜ | ⬜ T15 | — |
| claude | 2.1.278 | env allowlist observed by the child | ⬜ | ⬜ T15 | — |
| claude | 2.1.278 | `--permission-prompt-tool` + `--mcp-config` round-trip (§8.8b) | ⬜ | ⬜ **T13 spike — gates T13** | — |
| codex | **not installed** | anything | ⬜ | ⬜ | — |
| gemini | **not installed** | anything | ⬜ | ⬜ | — |

**Consequence of rule 2 that must ship in the release note.** Neither `codex` nor `gemini`
is installed on the development machine, so every one of their capabilities is `false` and
`Supports` refuses them for every policy. `orchestrator.example.yaml` currently routes
`review` → codex and `answer` → gemini, so that example config would fail
`ValidatePolicies` with exit 2 before any AI call. T04 therefore re-points the example
config at `claude` for all actions and documents codex/gemini as unverified rather than
shipping an example that cannot run.

## 13. Resolved decisions

All nine questions are resolved; `DECISIONS.md` in this folder carries the full rationale,
the alternatives rejected, and the consequences each answer has for the task breakdown.

| # | Question | Resolution |
|---|---|---|
| Q1 | escape-hatch strictness | **Refuse** without `ORCHESTRATOR_SANDBOX=1` or `/.dockerenv`. No typed-confirmation fallback. |
| Q2 | env allowlist vs auth | Allowlist corrected against the real environment (§8.5): explicit names, no `CLAUDE_CODE_*` wildcard (F11), `ANTHROPIC_BASE_URL`/`ANTHROPIC_DEFAULT_*_MODEL`/`AWS_PROFILE` added. **No** smoke-test call in `validate`; it prints the forwarded names instead. |
| Q3 | approval-wait deadline | Approval prompts run under a **separate non-deadlined context**; `execution.timeout` bounds machine time only, stated in MVP.md §25. |
| Q4 | mid-run approval | **In the first release.** Mechanism: `--permission-prompt-tool` + `--mcp-config`, gated on a spike that verifies the round-trip; stream-json host mode is the documented fallback. |
| Q5 | implement exec default | **No exec** by default; one global `security.implement_exec_allowlist`, not a per-action map. |
| Q6 | config provenance | **Refuse** when the in-worktree config file is untracked or locally modified; warn when it is clean and committed. |
| Q7 | advisory fields | Split accepted; `MaxBudgetUSD` is the only advisory field, and `policy.json` records it as ignored where unsupported. |
| Q8 | worktree isolation | **Out of scope**, recorded as a future option with its trade-offs (`DECISIONS.md` Q8). |
| Q9 | MVP.md | **Yes** — new normative §36 in MVP.md. This folder is the design record; MVP.md is the source of truth, kept in sync by `TestDocsPolicyTableCurrent`. |

## 14. Estimate

Per the Q4 resolution, all five phases below are in the first release except phase 5, which
is blocked on `codex`/`gemini` being installed. The task-level breakdown, with dependencies
and per-task acceptance criteria, is in `tasks/` — `README.md` in this folder is the board.

| phase | content | tasks | size |
|---|---|---|---|
| 1 | `domain.Policy` + table, `Request.Policy`, claude translation, `Capabilities`/`Supports`, `ValidatePolicies`, `PolicyError`, audit record | T01–T06, T12 | M–L (~2–2.5 days) |
| 2 | env allowlist (`Spec.Env` across call sites), config isolation flags, prompt fencing | T07–T08 | M (~1 day) |
| 3 | up-front authority block, clean-worktree + `pre_run_ref`, config provenance, bypass flag | T09–T11 | M (~1–1.5 days) |
| 4 | mid-run approval (MCP server, spike first), `approvals.log`, deadline handling | T13 | L (~2 days) |
| 5 | docs (§10) and live verification (§12) | T14–T15 | M (~1 day; codex/gemini blocked) |

Phase 1 alone removes `bypassPermissions` from every call and is independently shippable.
Docs are not deferred to phase 5 wholesale: T14 states which fragment must land with which
phase, because a README that describes authority the code does not yet enforce is worse than
no README. Gates per task: `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`
empty.

## 15. Acceptance criteria

1. `grep -rn "bypassPermissions" internal/` matches only the §8.4 bypass branch and its
   tests.
2. `classify` runs with no tools; `analyze`/`plan`/`review`/`answer` cannot write, run
   commands, or make network requests; `implement` can write only inside the repository
   root and runs no commands unless `implement_exec_allowlist` names them — each asserted
   by a unit test on argv and, per §12, by a live test against the real CLI.
3. A provider configured for an action whose policy it cannot enforce fails **before any
   AI call**, with exit code 2 and a message naming provider, action, unsupported field,
   and remedy.
4. `--mode accept-edits` and `--mode auto` are unattended and never hang on a permission
   decision: undeclared authority is denied, not prompted.
5. `--mode manual` asks per tool call where supported, denies on prompt timeout, logs every
   decision, and a long human deliberation does not fail the run on `execution.timeout`.
6. `--dangerously-bypass-permissions` is refused outside a detected sandbox, refused with
   `--mode manual`, banners loudly, and is recorded in `policy.json`.
7. Provider processes receive only allowlisted environment variables; `SSH_AUTH_SOCK` and
   `AWS_SECRET_ACCESS_KEY` are demonstrably absent from a provider child.
8. A planted `.claude/settings.json` hook and `.mcp.json` in the target repository do not
   take effect.
9. Every run directory contains `policy.json`; every `run.json` step execution records its
   policy; the authority block appears in CLI output in all modes.
10. `README.md` states the per-action authority, the mode mapping, and the limits of the
    model; `MVP.md` §36 is normative; `CLAUDE.md` carries the invariants;
    `TestDocsPolicyTableCurrent` passes and fails when the table is edited in code only.
11. `internal/workflow` still imports only `domain`; no provider-specific identifier
    appears outside `internal/provider/<name>`.
12. No change to classification, workflow selection, transitions, limits, routing, or what
    is trusted — existing integration scenarios 1–13 pass unmodified except where they
    assert argv containing `bypassPermissions`.
