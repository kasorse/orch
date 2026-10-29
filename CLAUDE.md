# AI Model Orchestrator

A local Go CLI that takes one engineering task, decides which workflow stages it needs from
probabilities returned by a decision provider (the Jev decision API or an LLM CLI), builds the
workflow deterministically, and executes it by shelling out to locally-installed AI CLIs
(`claude`, `codex`, `gemini`) against the current git repository, running real tests and
persisting run state to disk. Full spec: `MVP.md` — treat it as the source of truth; the
decision layer in detail: `specs/JEV integration/SPEC.md` (normative) and
`specs/JEV integration/TASKS.md` (plan, decisions, and what was built differently and why);
the permission model: MVP.md §38, with its design record in `specs/permissions/`;
resume: MVP.md §37, with its design record in `specs/resume/`;
the task from a Jira ticket (`run --jira`): MVP.md §39, with its design record in
`specs/jira/`;
the stages chosen by the operator (`run --stage`): MVP.md §40, with its design record in
`specs/stage-select/`;
hardening - prompts on stdin and bounded, the test environment and default sandbox, the CLI
version check, the pre-run test, the secret scan: MVP.md §41, with its design record in
`specs/hardening/`;
the commit offered at the end of a completed run: MVP.md §42;
the run summary and step metrics (`summary.md`, the call report, the diff from git, active
time): MVP.md §43, which is also the field reference for `summary.md`;
the quality loop (stage context, `checks:`, the in-session verify tools, review criteria,
`fix_medium` and extra reviewers, the no-progress stop, `by_complexity`): MVP.md §44, with its
design record in `specs/quality/`;
governance - path rules, the org config layer, the cost budget, threshold calibration and
exploration: MVP.md §45, with its design record in `specs/governance/`;
the decision shape - complexity cut from one size number, the asked stages and the level's
template, cut proposals in `decisions calibrate`, `actions.implement.escalate`: MVP.md §9,
§9.1, §31.1, §44.8, §45.4, with its design record in `specs/decision-shape/`;
clarify - the clarity question, the clarify step before the decision, `task.clarification`,
`task needs input`, and the `investigation` stage that took the place of analysis and planning:
MVP.md §49 (with §8, §9, §37, §40), with its design record in `specs/clarify/`;
the benchmark (`bench/`, the `orchbench` command, `runs report`): `bench/README.md`, with its
design record in `specs/benchmark/`;
the claude credential (API key or the CLI's login, `--claude-auth`, the `apiKeySource` check,
`FailureAuth`): MVP.md §46, with its design record in `specs/claude-auth/`;
the story (`story plan`, `story run`, `story status`, `run --story-task`, the child issues in
Jira, the scope question): MVP.md §48 and §39.11, with its design record in `specs/story/`;
delivery - the commit type and trailer, the branch name, `report.md`, the push that opens the
merge request, the Jira comment and transition, `--output json` and the exit codes, `orchestrator
review`: MVP.md §50 (with §4, §39.12, §42, §43, §47, §48), with its design record in
`specs/delivery/`; `delivery.push: false`, the run and the story that end at the commit: MVP.md
§50.5, §50.8, with its design record in `specs/delivery-push/`;
the Jira listener (`orchestrator jira listen`: tickets labelled `ai-run` become runs, one at a
time, the `listener` and `gitlab` sections, the git credential helper, `run --listener-task`,
`failure_kind`): MVP.md §51 (with §30, §39, §50.10, §50.13), with its design record in
`specs/jira-listener/`.
This file is a summary to keep future work aligned with them, not a replacement.

## Architecture (MVP.md §3)

Responsibilities are strictly separated:

- **LLM** — task reasoning: clarifying an unclear task (no tools), research, investigation,
  implementation, review. `orchestrator review --base <ref>` is a run of just `test` and
  `review` over `base...head` in a detached worktree (MVP.md §50.11).
- **Decision provider** — bounded probabilities only: does the task need project context,
  is it clear enough, its complexity distribution, each stage's probability. It never decides
  anything.
- **Orchestrator (this codebase)** — translation and decision-round invocation, project
  context collection, the policy (thresholds → decisions), workflow construction, provider
  routing, state transitions, limits, artifacts/context, test execution, persistence.
- **Git** — source of truth for code changes.
- **Test command** — source of truth for test pass/fail (exit code only, never LLM text).

Orchestrator/workflow code must never contain AI-provider-specific behavior. Per-provider CLI
argv quirks — including how an authority becomes permission flags — are isolated in each
provider's `buildArgs` function (`internal/provider/{claude,codex,gemini}/client.go`). Jev's
wire format is isolated in `internal/decision/jev`: its transport types and `Client` methods
are unexported. Jira's is isolated in `internal/jira` (unexported wire types; it imports only
`domain`, `apperr` and the standard library, never `config`).

`internal/workflow` and `internal/policy` must never import anything but `domain` (the
workflow package also defines its own `StepExecutor` interface). `internal/action` is the
composition point that implements `StepExecutor` using config/provider/repository/testexec;
`internal/app` is the composition root for everything. `internal/verify` is the in-session
verify server behind the hidden `orchestrator mcp-verify --dir <dir>` (specs/quality §6): it
speaks MCP with the standard library only, reads no input but `<dir>/verify.json` (replace
reads and writes the repository's files), and imports `guard`, `testexec`, `projectctx` (the
deny list replace honours) and `domain`, never `action`. `action` is its one client: for every
implement whose adapter speaks MCP, and with `actions.implement.verify`, it writes the `verify.json` of a private `orch-verify-*` directory,
merges the `orch` server into the step's `--mcp-config`, and reads `calls.jsonl` and `violation`
back as untrusted input (`action/verify.go`). `internal/testreport` is a leaf (standard library
only, enforced by a test): it reads failing test names out of a failed test step's output into
`TestResult.failed_tests`/`report_format` for the retry prompt, and what it finds never decides
pass/fail (specs/quality §8.2).

`bench/` is a black box to the core and the core is one to it. It is a separate Go module
with the path `orchbench`, so `orchestrator/internal/...` cannot be imported from it (a
compile error, backed by `bench/boundary_test.go`, which also catches a `replace` or a vendored
copy); it starts `orchestrator` and `claude` as programs and reads only what they write
(`run.json`, `decision.json`, stream-json). Nothing in the core imports it or exists only for
it (`internal/report` imports only `domain`), and it never selects or routes a model. Add no
shortcut that shares code between the two modules; if `bench/` needs a field, the orchestrator
must write it for an operator's sake too.

## Decision flow (MVP.md §8–§9)

```
[real Jev] → GET /v1/models: decision.model served? (jev.CheckModel; failure → exit, no run;
             on_failure: ask turns an unreachable Jev into a warning)
[--jira] → fetch the issue, render it as the task  (internal/jira; failure → exit, no run)
[--stage] → Decision{source: manual} from the flags; the rest of this flow is skipped
request → translate if ≥30% Cyrillic prose (internal/translate; failure → original + warning)
        → pre-check: P(none), P(project), P(clear)    (decision.Provider.ContextDepth)
        → P(clear) ≥ policy.clarify.threshold? no → the clarify step (MVP.md §49): one LLM call,
          authority none (no tools), given the request and the instructions Go selected
          (projectctx.Docs); then a re-check of request + clarification (decision.Provider.
          Clarity). Still unclear: interactive → the operator adds text, up to clarify.max_rounds;
          auto or rounds spent → the run stops `task needs input` (exit 4, nothing to resume).
          Not for --story-task; clarify.enabled: false asks and records the question only.
        → policy: project if P(project) ≥ project_threshold
        → Context Manager builds ProjectContext        (internal/projectctx)
          failure → continue with context_limited
        → classification: complexity + 3 stage probs   (decision.Provider.Classify)
          + the shadow scope question `scope_story`: recorded as `scope.story`, decides
            nothing, not asked for --stage or --story-task (MVP.md §48.9)
        → policy → complexity, enabled stages          (internal/policy)
        → workflow.Build                                (internal/workflow)
  provider failure → manual choice (on_failure: ask, interactive only) or error
```

`internal/classification.Service` runs this flow; `app.taskFromJira` runs the optional
fetch before it, and the rendered text is then the request like typed text. Providers:
`jev.Provider` (HTTP client or the offline `jev.NewMockClient()` with
`decision.jev.mock: true`) and `llm.Provider` (a CLI answering the same questions),
selected statically by `decision.provider`. Both use the
shared questions (`internal/decision/questions.go`) and state (`internal/decision/state.go`) —
edit them in one place only, and treat any wording change as a reason to recalibrate
thresholds. The decision provider sees text only: the request, later the request followed by
the rendered clarification — never an instruction file (`ClarityState{Request,
Clarification}`).

Fixed stage order and stage → action mapping (Go code, not LLM-invented):

```
research → research, investigation → investigate,
development → implement, testing → test, review → review
```

The clarify step is not a stage and is never in a workflow: it runs before the decision.
`analysis`/`analyze` and `planning`/`plan` are gone (a config or `--stage` naming them is
refused with a message naming `specs/clarify`).

Policy rules (`specs/decision-shape/`, `specs/clarify/`): size = P(medium) + 2·P(complex),
computed by Go from the probabilities for every provider; complexity is `complex` if size ≥
`policy.complexity.cuts.complex`, else `medium` if size ≥ `cuts.medium`, else `simple` —
applied to both the primary and the shadow question, the primary decides. Only research
and development are asked (`domain.AskedStages`); stages = `policy.stages.force
[complexity]` (the template) ∪ the asked stages with probability ≥ threshold (inclusive) ∪
testing whenever development is on; without development, testing and review are off; nothing
enabled → `investigation`. `policy.explore` never explores development. The same decision always
produces the same workflow. `policy.clarify.threshold` is inclusive too (`policy.ClearEnough`).

Engine transitions (`internal/workflow/transitions.go`) are a state machine, not a for-loop
over a fixed list: success → the next step of the workflow (done after the last); test
failure → implement if the workflow has it, else the run fails; review fail → implement if
present, else the run completes with the verdict; any other failure fails the run. Execution
history is append-only (`run.Executions`), never overwritten. Two independent limits guard
against infinite loops: `max_steps`, `max_fix_iterations`; `limits.stop_without_progress`
(`workflow.NoProgress`, over persisted executions only) ends a fix loop earlier when implement
changed nothing or the same failure repeats. Without `implement`, the last
read-only step (research/investigate) writes the final `answer`, printed at the end.
`Engine.Run` always enters through `workflow.Entry(run)`: the first step for an empty
history, otherwise the state the history and the transition table give (MVP.md §37.5).
`investigate` has authority `read`; with `implement` its artifact is the plan (read by implement
and review), without it, as the last read-only step, it writes the answer.

## Non-negotiable invariants

- **Fail fast, before any AI call or HTTP request** (MVP.md §7): `config.Load` →
  `config.Validate` → `config.ValidateDependencies` → `config.ValidateAuthorities` always
  run fully, in order, before any decision client or translator is constructed (the provider
  registry is built first; constructing it calls nothing). `Validate` covers the decision
  surface too (`clarify:` is required: a route or `clarify: {enabled: false}`; the old
  `analysis`/`planning` keys are refused naming their replacement and `specs/clarify`);
  `ValidateDependencies` checks every action's, the translator's, the clarify step's (when
  enabled) and an LLM decision provider's executable, and the Jev API key, and — only with
  `DependencyOptions{Jira: true}` (`run --jira`, `story plan --jira`, and `validate` when the
  section is present) — the Jira section and its variables; `ValidateAuthorities` checks that each
  route's provider can enforce its authority; right after it, every routed provider CLI's
  `--version` must be inside the range its adapter verified (`provider.VersionVerifier`;
  outside it only `security.allow_unverified_cli: true` lets a run go on, recorded and
  warned). A missing executable for an action used later
  (e.g. Codex for `review`) must fail `orchestrator run` before anything is called.
  `config.Load` parses a version-2 file strictly (unknown keys are an error); a key the
  validator rejects with its own message (a literal secret) must be declared in the schema.
- **Least authority, fixed in Go, fail closed** (MVP.md §38, `specs/permissions/`). Every
  provider call carries a `domain.Authority` (`none`, `web`, `read`, `edit`): workflow
  actions from `domain.AuthorityFor`, the translator, the clarify step and the LLM decision
  provider `AuthorityNone` explicitly — never chosen by a model, a provider default or config,
  never configurable per action, and independent of the run mode. The clarify call has no tools,
  so "do not read the code" is enforced by its authority, not by its prompt; the instructions it
  gets are chosen by Go (`projectctx.Docs`, deny list first), and the decision provider never
  sees them. A failed clarify call fails the run (`ProviderError`): an unclear task never goes
  on unclarified. No authority has a shell tool;
  Bash rules that constrain arguments are never a boundary. No call gets both repository
  access and a way out (network, shell). Authority → CLI flags exists only in
  `internal/provider/<name>/buildArgs`; an adapter declares `Supports` only for authorities a
  live check (`go test -tags security ./test/security/...`) has verified, and refuses
  anything else — a silent downgrade is the one unacceptable failure. Never pass
  `bypassPermissions` or any equivalent.
- **The operator's tree is never touched in worktree mode** (MVP.md §47). A workflow with
  `implement` runs in a worktree by default (`execution.isolation: worktree`; `run --inplace` or
  `isolation: inplace` opts out, `run --worktree` overrides an `inplace` config) and runs every step, guard,
  scan, review diff and commit in `<state>/orchestrator/worktrees/<repo-id>/<run-id>`; the
  orchestrator writes git objects, the run's branch, the worktree and
  `refs/orchestrator/` there and nothing in the operator's working tree, index or branch. The
  worktree's path is derived from the run ID, never read from `run.json`; the branch is not (it
  is named by the repository's instructions, a template or `orchestrator/<run-id>`, MVP.md
  §50.3), so a resume checks the recorded name's format and `git worktree list`; the mode comes from
  `options.isolation`, not the current config. The `.git` pointer is checked in Go before
  every fingerprint, every `git worktree add/list/remove/prune` holds the flock
  `<state>/orchestrator/worktrees/<repo-id>.lock` (git races itself, MVP.md §47.2), `worktree.setup` runs only before any step, and a new test or scenario
  that reads the result in the working tree pins `isolation: inplace`. New run artifacts:
  `run.json: worktree` and `logs/setup.{stdout,stderr}.log`.
- **Guard implement's result before anything runs it** (MVP.md §38.3): a workflow with
  `implement` needs a clean worktree (unless `security.require_clean_worktree: false`); the
  run records `pre_run_ref`; `guard.Guard` (`internal/guard`, aliased as `action.Guard`)
  compares protected paths (`security.protected_paths`, required, no built-in list, a leading
  `/` anchors at the root; plus the config file, always; git-ignored ones
  included) and the git config/hooks fingerprint around every `implement` call and every
  test command (both run code a model wrote) and fails the step. The fingerprint is compared
  before any `git status` — git status runs a planted `core.fsmonitor`. In a worktree run
  (`specs/worktree/`) the worktree's `.git` file is read in Go and compared with the recorded
  `admin_dir` before the fingerprint (`RepoGuard.GitPointer`, violation
  `.git (worktree pointer)`), and the sandbox binds it read-only. `worktree.setup` runs once
  (`runSetup`, only for a run with no executions and `setup_done: false`) in the test sandbox
  with `Sandbox.Network` (the one place the network is allowed; a copy, never `s.runner`'s;
  `json:"-"`, so verify.json cannot carry it) between two guard snapshots, inside the engine's
  share of `execution.timeout` (the second snapshot is taken even when setup was killed).
  `Dispatcher.Guard` is required — `implement` and `test` refuse without one. Provider CLIs run with
  `execution.ScrubbedEnv()`, which also drops the credential variables the config names
  (`Config.CredentialEnvNames`, registered right after `Validate`); every process that runs
  `test.command` gets the narrower `execution.TestEnv()` allowlist (plus `test.env_pass`,
  which refuses credential-like names); git keeps the full environment. The test command
  runs under bwrap unless `test.sandbox: none` is written out (`testexec.Sandbox`, probed in
  the fail-fast chain; its mount order is load-bearing). A workflow with `implement` and
  `test` runs the tests once before the first step and refuses a red suite unless
  `run --allow-red-tests` (MVP.md §41.5). The `test` step runs the `checks:` in order before
  `test.command`, each under the same sandbox, environment and guard, with a guard comparison
  after **each** command, so the next one never runs on a planted hook (MVP.md §44.2). After
  the guard check of every `implement` and `test`, what the tree adds is scanned (`internal/secretscan`, stdlib only): a finding
  outside the run-start baseline stops the step (`security.secret_scan.mode: stop`) or is
  recorded (`warn`); the review diff is redacted; a secret's value is never printed, logged,
  persisted or put in a prompt.
- **The claude credential is set by the adapter for the resolved mode and checked against
  `apiKeySource`** (MVP.md §46, `specs/claude-auth/`). One mode per process (`api` or `login`),
  from `--claude-auth`, then `providers.claude.auth`, then `auto`, resolved by
  `config.ResolveClaudeAuth` in the fail-fast chain. Only `internal/provider/claude` builds the
  child's environment for the mode (the key from `providers.claude.api_key_env` goes back in
  under `ANTHROPIC_API_KEY`; `workflow`, `policy`, `action` and `buildArgs` learn nothing about
  it), and the `apiKeySource` the CLI reports after every call must match the mode: a mismatch
  or an authentication/billing error is `FailureAuth`, fatal in the translator, the LLM
  decision provider and every step. The key is env-only, redacted like the Jev key, and never
  in argv, config, `run.json` or logs; a failure never switches the mode, and `resume`
  re-resolves it instead of reading it back. `providers.claude.api_env` is claude's alone: set
  in `api`, its names removed in `login`. No other process gets it, and it is never a secret:
  `Validate` refuses credential names and secret-looking values, naming only the variable.
- **Only exit codes and git diff are trusted, never LLM prose** (MVP.md §18/§19/§31.4): test
  pass/fail comes only from the test command's real exit code; review inspects the actual
  `git diff`, not just an implementation summary. Never parse phrases like "all tests should
  pass" out of model output as a substitute for running the tests. Checks are exit codes too.
  Failing test names parsed from the output (`internal/testreport`), and the split into old
  and new failures, only appear in a prompt: they never decide pass/fail, a transition or a
  stop (MVP.md §31.4, §44.7). The in-session `run_tests` is a hint, and the `test` step stays
  the truth.
- **Verify is not a shell** (MVP.md §38.1, §44.3, `specs/permissions/DECISIONS.md` D16). With
  `actions.implement.verify: true` the session gets `mcp__orch__run_tests` and
  `mcp__orch__run_check` from the orchestrator's own MCP server; the adapter's `--tools` does
  not change. The command comes from config (`test.verify_command` else `test.command`, a
  configured check), never from the model; the model picks only whether to run, a check name from a closed list, and a
  filter matching `^[A-Za-z0-9_./:-]{1,200}$` that reaches the process only as
  `ORCH_TEST_FILTER` (the `test` step never sets it). The sandbox is mandatory (`verify` with
  `test.sandbox: none` is a config error), the guard compares the repository with the step's
  before-state before **every** call, and the `violation` marker the server writes fails the
  step even if the tree looks clean afterwards. The server reads only `verify.json`; `action`
  reads `calls.jsonl` and `violation` back as untrusted input. The server name `orch` and any
  name with `__` are reserved. `mcp__orch__replace` is always on, with no config key, for an
  implement whose adapter is a `provider.MCPSupporter` (MVP.md §44.3, D18): two literal
  strings and a file filter, rewritten in Go over the files git lists, never a deny-listed,
  protected, ignored, binary or large file, a symlink or a path outside the repository; the
  same guard before every call, and neither string is recorded.
- **Go decides the quality loop, from config and the persisted run** (MVP.md §44). The
  no-progress stop, `review.fix_medium` (read from `options.review_fix_medium` on the run, never
  from the current config), the extra reviewers' merge and the `by_complexity` route are Go code
  over config and `run.json`; every transition they add is derivable on resume.
- **Governance is Go over config and the persisted run** (MVP.md §45, `specs/governance/`).
  `policy.paths` rules match the files git says the run's net diff changed, never a model's
  list, and a rule that needs a stage the workflow lacks fails the step: the workflow is never
  rebuilt. The org layer (`config.LoadLayered`, `/etc/orchestrator/org.yaml`, the environment
  variable only when that file is absent) only tightens: floors, ceilings and additions, in
  its own strict schema; a weaker repository setting, an unreadable org file and a route
  outside `allowed_models` are config errors before any call. `limits.max_cost_usd` fails
  closed: a routed provider that cannot report cost (`provider.CostReporter`) is a config
  error, and the engine stops before a step once the persisted executions have cost the
  budget. `decisions calibrate` only proposes (read-only, writes no config, never part of
  `run`); `policy.explore` only turns stages on. None of this binds the machine's owner.
- **Decision providers return probabilities; Go decides** (MVP.md §31.2). Never let a
  provider's own pick (e.g. Jev's `choice` field), an LLM, or a caller choose stages or their
  order. Every provider answer is strictly validated; anything unexpected is an
  `InvalidResponse`. The one case where the stage set does not come from a provider is
  `run --stage` (MVP.md §40): the operator names it, `domain.ManualDecision` records it as
  `source: manual`, and it is still never an LLM and never the order.
- **Static provider routing** (MVP.md §31.1): provider/model per action — and the decision
  provider — come from config. No automatic "best model" selection or fallback; a failing
  decision provider leads to the user's manual choice, never to another provider.
  `actions.<name>.by_complexity` is static routing too: a config table keyed by the decided
  complexity, model and effort only (never the provider), so the same decision always gives the
  same models; `run --stage` has no complexity and uses the base. `actions.implement.escalate`
  is the one model a failure changes: an implement that fixes a failed test or review (read
  from `run.Executions`) runs on the `by_complexity` entry one level up, or on the decided
  level's own `by_complexity.<level>.escalate` when it has one, derivable on resume.
- **Secrets never leave the machine**: the Context Manager applies its deny list (`.env*`,
  `*.pem`, `*.key`, `credentials*`, `context.extra_deny`, to every path segment)
  before reading anything, lists files only via git (honouring `.gitignore`), and never reads
  binaries, large files or symlinks. The Jev API key comes only from the environment
  (`decision.jev.api_key_env`), is read in `app`, passed only to the client's constructor,
  redacted from every error and scrubbed from every child process; a literal `api_key` in
  the config is rejected.
- **Jira: read-only, one GET, before the run exists, never on resume** (MVP.md §39).
  `run --jira` makes one `GET /rest/api/2/issue/{key}` to `jira.base_url` after the whole
  fail-fast chain and `jira.ParseRef`, and before `store.NewRun` — a failure creates no
  run. No redirect is followed; the answer is strictly validated. The request goes only to
  `jira.base_url`, never to a URL the operator pasted. Credentials are env-only (a literal
  secret key is rejected), read only in `app.taskFromJira`, redacted from every error and
  scrubbed from child processes. The rendered ticket becomes `Task.Description`; no stage
  learns where the task came from, and `resume` builds no Jira client.
- Provider CLI invocation is argv-only (`exec.CommandContext`), never `sh -c`, for the user's
  task/prompt — the one documented exception is `test.command`, which is trusted operator
  config and does go through `sh -c` to support shell syntax. The prompt itself goes on
  stdin, never in argv (MVP.md §41.1). Repository data in a prompt is bounded
  (`limits.prompt`): test output by `testexec.BoundTestOutput` (aliased in `action`), the
  review diff by
  `Repository.ReviewDiff` (new files included, deny-listed files by name only); artifacts
  and logs keep everything. User text inside prompts
  (request, project context) is embedded as delimited data that cannot close its block;
  the task in every stage prompt goes through `action.taskBlock` and repository-derived
  text (git diff, test output) through `action.fence` (both random-nonce delimiters) —
  defense in depth, never a boundary.
- **Resume continues, never re-decides** (MVP.md §37). `orchestrator resume` reuses the
  persisted decision and workflow. It constructs no translator, decision provider or
  Context Manager, and it validates everything it reads back from the run directory
  strictly (paths derived, never read; `workflow.Verify`), refusing rather than
  repairing. On a run that has executed a step, the git fingerprint is compared with
  `resume_baseline` before any `git status`, then the protected paths, then HEAD against
  `head_ref`. A worktree run (`specs/worktree/` §11) resumes in its worktree only after
  `verifyRunWorktree` accepted it, with nothing but git in the operator's repository run
  before: the derived path equals the record, the directory exists, its `.git` file (read
  in Go) names the recorded `admin_dir` under `<common>/worktrees/`, and `git worktree list`
  shows it on `orchestrator/<id>`; the operator's tree plays no part. The run directory is
  `flock`ed by the one `run`/`resume` that owns it.
  Until the operator approves, a resume changes nothing on disk. `run` and `resume` share
  one tail after the decision (`executeWorkflow` in `internal/app/app.go`); keep it that
  way. A resume refuses a run started before `specs/clarify` (a workflow with `analyze` or
  `plan`, a decision with `analysis` or `planning`) and a run stopped `task needs input`
  (nothing to resume); it reads `task.clarification` back for the stages and asks nothing again.
- **Commit only on an explicit yes** (MVP.md §42). At the end of a completed run with
  `implement`, the shared tail offers a commit on the current branch (`app.offerCommit`),
  never under `--mode auto` unless `auto_commit: true` gave the yes in advance (§42.6:
  then no question, and no commit on a warning, without `summary_short`, or when the last
  implement matched a `policy.paths` rule with `approval: manual`), never for a
  run started on uncommitted changes, and only after
  `action.CheckBaseline` (fingerprint first) passes — `git commit` runs the repository's
  hooks. The message is the implementation's `summary_short` (without one, the operator
  types it) plus the `Orchestrator-Run` trailer, assembled by Go with no model call.
  `Repository.CommitAll` is the one call that writes the user's index and a branch. Nothing in the offer changes the run's status or exit code.
  A worktree run commits in its worktree on its own branch as the first step of the delivery
  chain below; `auto_commit` has no effect there. `finishWorktree` then runs
  `merge-tree` against `base_branch` in the operator's repository (`session.operator`, after
  its fingerprint matched the one taken at creation), the push and `git worktree remove` without
  `--force`; without a commit the worktree stays.
- **Delivery: ask unless `--mode auto`, the push opens the merge request, no GitLab credentials**
  (MVP.md §50, `specs/delivery/`). A worktree run that completes with `implement` ends with commit
  → push → merge request → worktree removal, with no flag; `--inplace` leaves it out, and
  `delivery.push: false` (copied into `options.push`, `specs/delivery-push`) ends it at the commit
  with exit 0, needs no remote, and in a story stacks the tasks on local branches. Only
  `--mode auto` takes the three yeses in advance (and then the §42.6 refusals apply); every other
  mode asks before each, an empty answer is no and a no stops the chain. No commit, no push. The
  merge request is opened by the one `git push -u … -o merge_request.create -o
  merge_request.target=… -o merge_request.title=… -o merge_request.description=…` of the run's own
  branch (explicit refspec, never `--force`, never to a branch that exists on the remote, an intent
  in `run.json: mr` before it, never repeated): the orchestrator holds no GitLab token and has no
  GitLab client, the operator's git credentials do the push, and the MR URL is only read from the
  push's answer. Go writes everything a person reads (commit message, `report.md`, title,
  description, Jira comment) and escapes model- and ticket-written text; `commit_type` is the one
  model word, taken from a closed list. `--output json` prints one line on stdout and exit code 6
  is "completed, needs a person".
- **A model proposes, Go orders, the operator approves** (MVP.md §48, `specs/story/`). `story
  plan` writes a spec of tasks as files and runs nothing; `story run` is the approval. Go finds
  the services the task names under `story.roots` (no model call), validates the model's
  answer strictly, orders the tasks from `depends_on` (Kahn, smallest ID first) and computes
  each task's base and fallback branch (named by its repository's rules, `story/<slug>/<ID>` only the
fallback: stacked on the same-repository task it
  needs, or, with several such parents, on the tip of the story's spec branch
  `story/<slug>/spec` where Go merged them with `merge-tree` and `commit-tree`, never in a tree;
  a merge that conflicts makes the task `conflict` and stops the story for a manual merge); no
  model output picks what runs next, where, or with which stages. A story task is an
  ordinary worktree run started as a child process (`run --story-task`, hidden), with
  development forced on and research and investigation off (the task file is the plan), no
  clarify step,
  the task's acceptance items as review criteria and the `Orchestrator-Story` commit trailer;
  stories do not nest. `story run` runs up to `story.max_parallel` (`--parallel N`, 1..8, default
  1) ready tasks of different services at once (tasks of one service one at a time), each its own child run, with one goroutine owning `story.json`, the
  README, the task files and the console (the children's goroutines only run a process); while
  children run no git call writes `.git/config` or the hooks (every child's guard fingerprints
  them), and nothing new starts after a task that did not end done. A
  dispatched task's file is frozen up to `## As built`, which only Go appends. The scope
  question is shadow data: nothing reads `scope.story` to decide.
- **Jira writes only after approval, never twice** (MVP.md §48.7, §39.11, §39.12). The writes are
  `story run` creating the child issues (`jira.create`, a story planned from a Jira issue): after
  the confirmation, before the first dispatch, an intent in `story.json` before each POST, the
  key recorded after the 201, no retry once a request may have reached Jira (only 429/503 with
  `Retry-After`), and an intent without a key stops the next start; and, with `jira.write`, a
  comment and a transition on the task's own issue after a push opened its merge request
  (`mr.state: created`; the same intent-first, never-repeated rules; a failure is a warning;
  `options.jira_write` is the copy a resume uses, and a resume still reads nothing from Jira).
  `story plan` writes nothing to Jira. `run --jira` refuses an issue type in `story.jira_issue_types`.
- **The listener owns Jira; its child never calls it** (MVP.md §51, `specs/jira-listener/`).
  `orchestrator jira listen` is a supervisor like `story run`: it polls Jira (JQL built by Go,
  re-checked in Go), claims a ticket by a transition, finds the service and project from the
  labels and components (never a model), clones or fast-forwards the checkout, and runs the ticket
  as a child `run --listener-task <file> --mode auto --output json`, which builds no Jira client
  and has `jira.write` off; the listener reads the child's JSON line and `run.json` and only then
  writes a comment and a transition (intent first in `.orchestrator/listener/state.json`, never
  repeated, a failure is a log line). The GitLab token reaches only git, through the credential
  helper (the hidden `git-credential`), never a URL, argv, `.git/config`, log, state or comment, and
  never a provider CLI or the test command; the child gets no Jira variables. Only allowlisted
  groups (`gitlab.allowed_groups`) are cloned, and only reporters in `listener.query.reporters`
  start a run (the label alone is not enough). A cloned repository's `orchestrator.yaml` is
  trusted because its group is. `internal/listener` is pure (domain and the standard library);
  HTTP stays in `internal/jira`, git in `internal/repository`, processes in `app`. One listener
  per working directory (flock), one ticket at a time.
- Stages always get the user's original request (through `action.taskBlock`); the translation
  is only for the decision provider and relevant-file search. After a clarify step the stages
  that take the task (research, investigate, implement, review) get, after it, the rendered
  clarification (`domain.Clarification.Render`) through `action.fence`; an empty one adds nothing.

## Out of scope (MVP.md §30)

No HTTP provider APIs/OpenRouter/LiteLLM for the AI steps (the HTTP integrations are the Jev
decision API, reading one Jira issue for `run --jira` and the listener's Jira calls), no model benchmarking in the core
(it lives in `bench/`, outside it, and never selects models) and no automatic
model selection/routing/fallback, no parallel or multi-agent workflows (the one exception: a
story's ready tasks run at once, each its own run with its own sequential workflow, MVP.md §48.6), no Web UI/REST
API/IDE plugin, no GitHub integration and for GitLab only git's clone, fetch and the push that opens
a merge request (no API client; a token only for the listener's git credential helper, MVP.md §50.13),
no Jira beyond reading one issue, creating the child issues of an approved story, the comment and
transition after a merge request and, for `jira listen` only, a JQL search, comments, `/myself` and
comments and transitions without a merge request (no links or worklogs), no remote repository execution, no RAG or persistent
conversational memory, no database, no custom dynamically-defined action types or
user-defined workflow language. Don't add abstractions only useful for these.

## Tasks from `specs/*/tasks`

When you cut a specification into tasks, each task updates the documentation its own change makes
out of date - its sections of MVP.md, CLAUDE.md, README.md, `orchestrator.example.yaml` - in the
same task. Do not leave the documentation to a closing task: one task editing MVP.md in a dozen
places reads it piece by piece with a context that grows every turn (specs/clarify CL08: 20
minutes, 94 edits of MVP.md). A closing task may still add what only exists once every task is
done, such as a section that summarises the whole feature.

A task is finished by its specification's README ("Ground rules"), not only by green tests:
tick it in that README's table and in `TODO.md` where `TODO.md` tracks it, bring the
README's `Status:` line up to date, and add an `## As built` section to the task file with
the date and what was built differently from the plan, and why. A task under `bench/` also
runs the four gates in `bench/`: it is a separate module, and the root `go test ./...` does
not test it.

Inside an orchestrator run, do all of this in the same change as the code; do not leave it to the
operator. The run's `test` step (`scripts/check.sh`) is the task's gate, and the run only
commits after that step passes, so a task that fails its gate never reaches the branch. The one
exception is a gate the test step cannot run, such as `go test -tags security
./test/security/...`, which makes live calls: leave a task with such a gate unticked, and say
in its As built which gate is left to the operator. The four `bench/` tests that skip under
bwrap are run outside the sandbox once per specification, before its branch merges, and
recorded in the README's `Status:` line; they are not a gate for each task.

## Build & test

```
go build ./...
go vet ./...
go test ./...
gofmt -l .          # must be empty
cd bench && go test ./...   # bench/ is a separate module; the root go test does not run it
```

`scripts/check.sh` runs these four gates in the root module only (not `bench/`, test that by hand
with the command above) and stops at the
first failure; it is also the orchestrator's `test.command` for this repository, so a run's
test step does not cover `bench/`. `ORCH_TEST_FILTER` (set only by the implement session's
`run_tests`) narrows its `go test` to `-run <filter>`; build, vet and gofmt still run in full. Under the bwrap sandbox four `bench/` tests skip (nested
bwrap, `orchestrator validate`, the end-to-end `TestPipeline` of `bench/test` among them); run
the script outside the sandbox once per specification, before its branch merges (rebuild
`bin/orchestrator` first, since the `orchestrator validate` test uses it).

Tests need no network, no Jev key and no real provider CLIs: `test/integration` uses fake
executables (`internal/testutil/fakeexec`, routed by the `ACTION: <name>` first line of every
prompt; `FAKE_ARGS_DIR` records each call's argv, `FAKE_ENV_DIR` its environment), a fake
Jev (`test/integration/jev_test.go`) and a fake Jira (`test/integration/jira_test.go`). The
live permission suite is separate and spends real calls:
`go test -tags security ./test/security/...`. Golden files:
`go test ./internal/decision/jev -update`, `go test ./internal/logging -update`,
`go test ./internal/jira -update`, and the stage prompts with a story's survey and specify prompts
`go test ./internal/action -update` (`internal/action/testdata/prompts/`; the test fixes
`action.nonce`, so read the diff of a rewritten file before committing it). Never commit a token-shaped literal (an `AKIA…` key, a
`ghp_…` token): secret scanners and push protection would flag the repository itself;
tests assemble such values at run time (`internal/secretscan`, scenario 27).

## Run artifacts

Persisted under `.orchestrator/runs/<run-id>/`: `run.json` (with `task.source` for a
`run --jira` task, `task.clarification` (`{threshold, checks, status, rounds}`: written once
before the decision, absent when the first check was clear, with `clarify.enabled: false`, for
a story task and after a failed pre-check; a run stopped `task needs input` has it and no
decision or workflow), the `decision`,
`pre_run_ref`, `head_ref`, `resume_baseline` (the guard's run-start state), `provider_cli`,
`branch_naming` (`{source, proposed?, rejected?, usage?, call?}`), `mr` (`{state: intent|pushed|created,
remote, source, target, iid?, url?, skipped?, error?}`: the intent is saved before the push),
`jira_write` (`{comment, transition}`), `review_target` (`{base, head, merge_base, …}`, an
`orchestrator review` run, which has no `worktree.branch`),
`org_config` (`{path, sha256}` of the org file the run started under; each `resumes[]` entry
has its own), `claude_auth` (`{requested, mode, decided_by, key_env, api_env}`, never the key; `api_env`
holds variable names only; each
`resumes[]` entry has its own; a record, never read back to decide), `allow_red_tests`, `pre_run_test` (with `checks`, `failed_check`, `failed_tests`),
`options` (`review_fix_medium` and `path_rules`, a copy of `policy.paths`, `jira_write`, a copy of
`jira.write`, and `push: false`, a copy of `delivery.push: false`, all recorded at run start and read by a resume instead of the config), `secret_scan_baseline`, `secret_findings` (never a
value), `commit` (the SHA the operator approved at the end), the append-only `resumes` (each with its `provider_cli`), and each execution's
`provider.authority`, `provider.model_source` (`base`, `by_complexity.<level>` or `escalated.<level>`), `attempt`,
`usage` (with `partial` for a call cut off before its final report), `call` (`api_key_source`
as the CLI reported it, prompt size and cuts, models, API time, tool calls, an extra reviewer's `extra_calls`) and, for an implement the
guard passed, `diff` from git (with `run_files`, up to 200 `{path, insertions, deletions, binary}`
for `report.md`) and `path_rules` (the `policy.paths` rules the run's net diff matched,
with the stages the workflow lacks in `missing`; recorded also when they failed the step), and for any
implement `tree_after` and `verify_calls`; a test
result's `checks`, `failed_check`, `failed_tests` and `report_format`; a review result's `diff`
summary, `criteria` and `reviewers`) and `outcome`
(written when the run reaches a terminal status, recomputed over the whole history by a
resume, derived and never read back for a decision: `status`, `failure_reason`,
`fix_iterations`, `tests_passed_final`, `review_verdict`, `verify_calls`, `approval_required`, `duration_ms`, `active_ms`,
`usage` summed over the steps and the decision calls with `calls` and `partial_calls`,
`decision_overhead` {`cost_usd`, `ms`, `calls`}, the net `diff`), `summary.md` (rendered
from run.json and decision.json when
the run ends, never read back; field reference MVP.md §43), `report.md` (the reviewer's report and
the merge request description, written next to it by Go, never read back; MVP.md §50.4), `decision.json` (`source: manual` for `run --stage`, with no provider, complexity or latency; `models`: the
provider, model, effort and source each action gets; `policy_hash`: the sha256 of the
`DecisionPolicy` the decision used, absent for `manual`; `questions_hash`: the sha256 of the
shared questions, which `decisions calibrate` groups sizes by; `complexity.sources.<src>.size`
and `complexity.cuts`; `stages[].explored`: a stage
`policy.explore` turned on; grouped on by `decisions report` and
`decisions calibrate` (read-only, `internal/report/calibrate.go`); full trace:
translation, source, model, depth, complexity with choice/score agreement, every stage's
probability/threshold (absent for a stage not asked)/forced/reason, `clarity` {threshold,
checks, status, reason} (`status` `disabled` under `clarify.enabled: false`), latencies, and `usage` of each decision call that
reported one: `translation`, `context_depth`, `classification`, `clarify`, `clarity_recheck`; the
last two count in `outcome.decision_overhead` and `limits.max_cost_usd`; `policy_hash` covers
`policy.clarify.threshold`),
`project_context.json` (exactly what was sent, only if something was), `workflow.json`,
`ticket.json` (the fetched issue, `run --jira` only; an audit record never read back),
`artifacts/<NNN>-<action>.json` (a model step's with the `prompt` exactly as sent - a record,
removed when a resume reads the artifact back), `artifacts/clarify-<N>.json` (the clarify call's
prompt as sent, the excerpts' paths and headings, the parsed points), `logs/<NNN>-<action>.{stdout,stderr}.log` (a test step with
`checks:` adds `logs/<NNN>-test.<check>.*`, an implement with verify calls adds
`logs/<NNN>-implement.verify-<K>.*`), and for calls outside steps `logs/translate.*`,
`logs/decide-{context,classify}.*`, `logs/clarify-<N>.*`, `logs/decide-clarity-<N>.*` (one per
re-check), `logs/pre-run-test.*`, `logs/pre-run-test.<check>.*` and `logs/branch-name.*` (the branch-naming call).

`jira listen` keeps its own home in the working directory (MVP.md §51.9): `.orchestrator/listener/`
holds `state.json` (per ticket: attempts, service, phase, run id, outcome, the writes done, `next_try_at`),
`lock` (flocked, one listener per directory) and `tasks/<KEY>.<attempt>.json` (a child's task input,
whose `run.json` has `task.source.listener: true`); the clones live in `listener.services_dir`
(`.clone-*` is a clone in progress).

A story run adds `task.source` (`kind: story`: story, task, attempt, jira), `task.acceptance` and
the branch, base and base branch it was asked for to a child's `run.json`, and the `story task`
reason on development and `scope.story` to `decision.json`. The story itself lives in the story
home under `.orchestrator/stories/<slug>/` (MVP.md §48.8): `story.json` (the services, the bases
with `bases.<repo>.spec_branch`, the status and, per task, the run, branch, commit, Jira key and
intent, cost; a task whose parents do not merge cleanly is `conflict` with `conflict {parents,
files}`), `ticket.json` for
`story plan --jira`, `artifacts/survey-<repo>.json` and `artifacts/specify.json` (the prompts as
sent and the answers), `logs/survey-<repo>.*` and `logs/specify.*`, `logs/task-<ID>.<attempt>.log`
(a child's output when more than one task runs at once), and `tasks/<ID>.json` (a
child's task input); a plan that reaches no spec leaves `.plan-<id>`. The spec itself is in
`<spec_dir>/<slug>/` (committed by the operator), with `## As built` appended to each task file by Go.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

When the user types `/graphify`, use the installed graphify skill or instructions before doing anything else.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- Dirty graphify-out/ files are expected after hooks or incremental updates; dirty graph files are not a reason to skip graphify. Only skip graphify if the task is about stale or incorrect graph output, or the user explicitly says not to use it.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).