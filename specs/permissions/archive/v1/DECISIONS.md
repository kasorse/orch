# Decisions: execution authority and permission policy

Resolutions for `SPEC.md` §13, decided 2026-09-21. Each entry states the answer, why, what
was rejected, and what it changes in `tasks/`. Where a decision rests on something checked
against the real CLI rather than on reasoning, the evidence is quoted.

---

## Q1 — Escape-hatch strictness

**Refuse.** `--dangerously-bypass-permissions` exits with `*apperr.ConfigError` (code 2)
unless `ORCHESTRATOR_SANDBOX=1` is set or `/.dockerenv` exists. No typed-confirmation
fallback.

A typed confirmation is a speed bump, and speed bumps get scripted around: the first time
someone needs the flag on a laptop they will find the confirmation string and paste it, and
from then on the "sandbox required" property is folklore rather than a control. Refusal has
the opposite failure mode — the flag is unavailable until the operator does the thing that
makes it survivable — and the remedy (`export ORCHESTRATOR_SANDBOX=1`) is one line, so an
operator who genuinely accepts the risk is not blocked, only made to say so explicitly and
in a place that is visible to whatever launched the process.

There is a second, mechanical reason. Verified on claude 2.1.278:

```
$ claude --print --restricted --permission-mode bypassPermissions --tools "" "hi"
Error: bypassPermissions not supported in restricted mode
```

Every non-bypass policy passes `--restricted`, and `--restricted` itself refuses
`bypassPermissions`. So bypass is not "the same argv with one flag relaxed" — it is a
distinct branch that must drop `--restricted` to work at all. A branch that deliberately
discards the CLI's own refusal should be the hardest one in the codebase to reach.

Rejected: warn + typed confirmation (above); a config key (a security downgrade that
persists in a file, invisible at the call site, and copy-pasteable between repositories).

→ **T11.**

---

## Q2 — Environment allowlist vs auth

**Two parts.** The allowlist as drafted was wrong for this machine and had a defect; the
smoke test is declined.

### The allowlist was insufficient and unsafe as drafted

Inspecting the development environment (names only, values never printed) showed the
drafted list would both break auth and leak an IPC channel. Set here:

```
ANTHROPIC_BASE_URL        ANTHROPIC_API_KEY         AWS_PROFILE
ANTHROPIC_DEFAULT_OPUS_MODEL    ANTHROPIC_DEFAULT_SONNET_MODEL    AWS_REGION
ANTHROPIC_DEFAULT_HAIKU_MODEL   ANTHROPIC_DEFAULT_FABLE_MODEL
CLAUDE_CODE_MESSAGING_SOCKET    CLAUDE_CODE_MESSAGING_TOKEN
CLAUDE_CODE_SESSION_ID          CLAUDE_CODE_CHILD_SESSION          CLAUDECODE
```

Two consequences, both now in `SPEC.md` §8.5:

1. **Insufficient.** The draft forwarded `ANTHROPIC_API_KEY` and `AWS_REGION` only. This
   setup routes through `ANTHROPIC_BASE_URL` and resolves model aliases through
   `ANTHROPIC_DEFAULT_*_MODEL`, with `AWS_PROFILE` for the credential source. A run under
   the drafted allowlist would have failed auth or silently used different models — and
   "the security change broke my auth" is exactly how a security change gets reverted.
2. **Unsafe (new finding F11).** The draft's `CLAUDE_CODE_*` **wildcard** would have
   forwarded `CLAUDE_CODE_MESSAGING_SOCKET` and `CLAUDE_CODE_MESSAGING_TOKEN` — a live,
   authenticated IPC channel to the Claude Code session that launched the orchestrator —
   into a spawned CLI. An allowlist whose entries are prefixes cannot be audited by
   reading it, because what it admits depends on what happens to be in the environment.
   The allowlist is now explicit names only, and the `CLAUDE_CODE_MESSAGING_*` /
   `CLAUDE_CODE_SESSION_*` / `CLAUDECODE` / `CLAUDE_PID` names are on the deny list, which
   overrides `security.env_allowlist` so a config cannot re-admit them.

### The honest limit, which must be documented rather than papered over

`HOME` must be forwarded — an OAuth-authenticated `claude` reads credentials from its
config file, not the environment — and forwarding `HOME` exposes `~/.aws/credentials`,
`~/.ssh`, `~/.config/gh` to any step that can read files. Denying `AWS_SECRET_ACCESS_KEY`
while forwarding `HOME` and `AWS_PROFILE` therefore removes the *easy* path (a variable
visible to any command the CLI runs, and to anything that dumps `os.Environ()`), not every
path. What makes the residual exposure inert is §8.2: read-only actions have no exec and no
network, so there is no channel to send a file out through.

This belongs in README's "what is not protected" verbatim. An operator who believes the env
allowlist protects their SSH key is worse off than one who knows it does not.

### No smoke test in `validate`

Declined, consistent with `SPEC-diagnostics.md` Q8. A real provider call in `validate`
costs money and latency on every invocation, needs network in CI, and is exactly the
preflight probe that was already rejected. Cheaper substitute with most of the value:
`orchestrator validate` **prints the forwarded variable names** for each configured
provider. That turns "my auth variable is not on the list" into something an operator sees
by reading output, with no backend call — and it is the same list the audit record stores,
so it is verified by being used rather than by being separately tested.

→ **T07** (allowlist + deny list), **T14** (README limits section).

---

## Q3 — Approval-wait deadline

**Separate non-deadlined context.** `execution.timeout` bounds machine time only, stated
normatively in MVP.md §25.

The alternative — extending the run deadline by the measured human wait — makes the
deadline a moving target. `runCtx` is created once at `app.go:141` and threaded everywhere;
"extending" it means either recreating the context mid-run (so every already-derived child
context still carries the old deadline, and which children were derived when becomes
load-bearing) or tracking an accumulated offset that every deadline computation must
remember to add. Both are the kind of subtlety that works in testing and fails at 2am on
the one run where it matters.

A prompt that runs under its own `context.Background()`-rooted context with its own bounded
timeout (§8.8b requirement 2) is trivially correct, and the sentence an operator needs —
"the timeout covers compute, not the human" — is one line of documentation. Cancellation is
preserved: the prompt context still derives from the signal-cancelled root, so Ctrl-C at a
prompt still aborts the run; only the *deadline* is dropped.

→ **T13.**

---

## Q4 — Mid-run approval: mechanism and phase

**In the first release.** Mechanism: `--permission-prompt-tool` with `--mcp-config`,
gated on a spike; stream-json host mode is the documented fallback.

The MCP route is preferred for one concrete reason: `internal/action` parses each step's
stdout as a JSON artifact today. Stream-json output would change the shape of every
provider response, so the fallback drags `internal/action`'s parsers, `StepOutcome.RawStdout`
and the `--verbose` tee into a change that is supposed to be about permissions. The MCP
route leaves stdout alone.

It is, however, the one translation in the spec whose round-trip is unverified — flag
parsing was confirmed on 2.1.278, the actual call-back was not. So **T13 begins with a
spike** that must demonstrate the CLI invoking the orchestrator's approval tool and
honoring allow and deny, before the rest of T13 is written. If the spike fails, T13 switches
to stream-json host mode and grows by the parser work; that branch is priced into T13's
estimate rather than discovered inside it.

One thing this decision makes mandatory rather than optional — new finding **F12**, verified
on 2.1.278:

```
--permission-prompts <target>   ... (choices: "host", "none", default: "host")
```

The default is `host`. Removing `bypassPermissions` without passing
`--permission-prompts none` would convert every unattended step from "silently allowed" to
"blocked forever on a host that is not listening" — a hang, not a denial, and precisely the
failure that would make someone put `bypassPermissions` back. `none` is therefore required
in every policy that is not `ApprovalAsk`, and T03 asserts it in argv tests.

Rejected: shipping §8.8b later. It was the recommendation, but the run-wide deadline
interaction (Q3) and the `--permission-prompts` wiring are both easier to get right once
than to retrofit around code written assuming nothing ever prompts.

→ **T13** (spike first), **T03** (`--permission-prompts none` in every non-ask row).

---

## Q5 — `implement` exec default

**No exec by default; one global `security.implement_exec_allowlist`.**

The no-exec default follows from the workflow: `implement → test`, and `test` is run by
`internal/testexec`, which is the only trusted source of pass/fail (MVP.md §18/§31.4). An
implement step never needs to run anything to do its job. Exec is also the single largest
jump in authority available — with it, every other restriction in this spec is advisory,
because a command can write outside the write roots, read anything `HOME` reaches, and open
a socket.

A per-action map was rejected for the reason §5 rejected configurable policy generally: it
would let an operator widen `analyze` or `review`, which are the two actions that consume
the most attacker-influenced text (repo files, a contributed `git diff`). A map whose only
defensible value is `{implement: [...]}` should be spelled `implement_exec_allowlist`, so
that widening a read-only action is not something the schema invites.

The expected friction is real — a project whose change does not compile without running a
generator — and the opt-in is one key. If a second action ever genuinely needs exec, that is
a spec change with a threat-model paragraph, not a config key added quietly.

→ **T05** (config + validation), **T03** (`--allowed-tools 'Bash(...)'` translation).

---

## Q6 — Config provenance

**Refuse when the in-worktree config file is untracked or locally modified; warn when it is
clean and committed.**

`test.command` goes through `sh -c` as trusted operator config, and the default config path
resolves relative to cwd. Running the orchestrator inside a repository that ships its own
`orchestrator.yaml` therefore makes that shell line attacker-controlled (F7).

The distinction that makes refuse-vs-warn tractable is git state, not location:

- **Untracked, or tracked-but-modified** → refuse. This is the shape of a planted or
  tampered file. Nothing legitimate needs it: commit the file, or pass `--config` from
  outside the worktree.
- **Tracked and clean** → warn, printing the resolved absolute path. This is the ordinary
  case (including this repository running on itself), and its content is whatever the
  operator reviewed when they cloned. Refusing here would make the tool unable to run on its
  own repository, which is how a control gets disabled wholesale.

Implementation is two git calls on the config path, both already expressible through
`internal/repository`: `git ls-files --error-unmatch <path>` and
`git status --porcelain -- <path>`. Both must be resilient to the config being outside the
worktree (the explicit-`--config` case), where the check simply does not apply.

Rejected: refuse whenever the config is inside the worktree (breaks self-hosting, so it
would be turned off); warn only (a warning on a path that leads to shell execution is not a
control — and this one would fire on every self-hosted run, which is how warnings become
invisible).

→ **T09.**

---

## Q7 — Advisory fields

**Split accepted; `MaxBudgetUSD` is the only advisory field.**

The test for whether a field may be advisory: does ignoring it grant authority? A budget cap
does not — exceeding it costs money, which is recoverable and observable, and
`max_steps`/`max_fix_iterations` already bound the run. Every other field on `Policy` does
grant authority when ignored, so every other field is mandatory.

Making the budget mandatory would make `max_budget_usd` unusable the moment any action is
routed to a provider that cannot express it, which is the wrong trade for a guardrail: the
operator's real alternative is no cap at all, not a cap plus a broken route.

The visible obligation is that "advisory" must never be silent. `policy.json` records
`ignored_advisory: ["max_budget_usd"]` per action, and the up-front authority block says so
for the affected steps, so nobody concludes from a config key that a cap is in force when it
is not.

→ **T01** (type + `Validate`), **T12** (`ignored_advisory` in `policy.json`).

---

## Q8 — Worktree isolation for `implement`

**Out of scope. Recorded as a future option.**

MVP.md §16's repository model is the live checkout, and §8.11 (clean-worktree requirement
plus `pre_run_ref`) already delivers the property that motivated the question: a named point
to return to. The incremental gain from a throwaway worktree is confinement of a *wrong*
write rather than confinement of a *malicious* one — a step that can write also inherits
`HOME` (Q2), so a worktree is not a security boundary either, only a better undo.

The cost is not small: `test` would run in the copy (so `test.command` must work there —
build caches, `vendor/`, generated files, absolute paths), the reported `git diff` becomes a
diff of the copy, review reads the copy, and merge-back on success is a new failure mode
directly on the success path.

One note for whoever picks this up: **do not use `claude --worktree`.** The flag exists
(verified on 2.1.278), but it has the CLI create the worktree, so the orchestrator would not
know the path it must run tests in, diff, and merge back — inverting the ownership this
codebase depends on. If this is ever built, `internal/repository` creates the worktree and
passes it as `WorkingDir`.

→ No task. Listed under "deferred" in `README.md`.

---

## Q9 — MVP.md as home for the policy table

**Yes — new normative §36 in MVP.md.**

Project `CLAUDE.md` designates MVP.md the source of truth and this folder a design record.
Authority-per-action is exactly the kind of fixed table MVP.md already carries for actions,
workflows and transitions; keeping it only in `specs/` would create a second source of truth
for the one table where drift is a security defect rather than a documentation nit.

The division of labor across the four documents:

| document | role |
|---|---|
| `MVP.md` §36 | normative: the table, the mode mapping, the fail-closed rule, the escape hatch |
| `specs/permissions/` | design record: findings, threat model, rejected alternatives, decisions, tasks |
| `README.md` | operator-facing: what this permits, what it does not protect, how to extend |
| `CLAUDE.md` | invariants that keep future changes aligned |

`domain.PolicyTableMarkdown()` plus `TestDocsPolicyTableCurrent` (§10.5) makes divergence
between the code table and both documents a failing test rather than a discovery six months
later. That test is what makes three copies of the table safe to have.

→ **T14.**
