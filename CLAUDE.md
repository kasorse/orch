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
the commit offered at the end of a completed run: MVP.md §42.
This file is a summary to keep future work aligned with them, not a replacement.

## Architecture (MVP.md §3)

Responsibilities are strictly separated:

- **LLM** — task reasoning: research, analysis, planning, implementation, review.
- **Decision provider** — bounded probabilities only: does the task need project context,
  its complexity distribution, each stage's probability. It never decides anything.
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
`internal/app` is the composition root for everything.

## Decision flow (MVP.md §8–§9)

```
[real Jev] → GET /v1/models: decision.model served? (jev.CheckModel; failure → exit, no run;
             on_failure: ask turns an unreachable Jev into a warning)
[--jira] → fetch the issue, render it as the task  (internal/jira; failure → exit, no run)
[--stage] → Decision{source: manual} from the flags; the rest of this flow is skipped
request → translate if ≥30% Cyrillic prose (internal/translate; failure → original + warning)
        → pre-check: P(none), P(project)              (decision.Provider.ContextDepth)
        → policy: project if P(project) ≥ project_threshold
        → Context Manager builds ProjectContext        (internal/projectctx)
          failure → continue with context_limited
        → classification: complexity + 6 stage probs   (decision.Provider.Classify)
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
thresholds.

Fixed stage order and stage → action mapping (Go code, not LLM-invented):

```
research → research, analysis → analyze, planning → plan,
development → implement, testing → test, review → review
```

Policy rules: complexity is `complex` if P(complex) ≥ `policy.complexity.complex`, else
`medium` if P(medium)+P(complex) ≥ `policy.complexity.medium`, else `simple` — applied to
both the primary and the shadow question, the primary decides. A stage is enabled if its
probability ≥ its threshold (inclusive), plus `policy.stages.force[complexity]`; nothing
enabled → `analysis`. The same decision always produces the same workflow.

Engine transitions (`internal/workflow/transitions.go`) are a state machine, not a for-loop
over a fixed list: success → the next step of the workflow (done after the last); test
failure → implement if the workflow has it, else the run fails; review fail → implement if
present, else the run completes with the verdict; any other failure fails the run. Execution
history is append-only (`run.Executions`), never overwritten. Two independent limits guard
against infinite loops: `max_steps`, `max_fix_iterations`. Without `implement`, the last
read-only step (research/analyze/plan) writes the final `answer`, printed at the end.
`Engine.Run` always enters through `workflow.Entry(run)`: the first step for an empty
history, otherwise the state the history and the transition table give (MVP.md §37.5).

## Non-negotiable invariants

- **Fail fast, before any AI call or HTTP request** (MVP.md §7): `config.Load` →
  `config.Validate` → `config.ValidateDependencies` → `config.ValidateAuthorities` always
  run fully, in order, before any decision client or translator is constructed (the provider
  registry is built first; constructing it calls nothing). `Validate` covers the decision
  surface too; `ValidateDependencies` checks every action's, the translator's and an LLM
  decision provider's executable, and the Jev API key, and — only with
  `DependencyOptions{Jira: true}` (`run --jira`, and `validate` when the section is
  present) — the Jira section and its variables; `ValidateAuthorities` checks that each
  route's provider can enforce its authority; right after it, every routed provider CLI's
  `--version` must be inside the range its adapter verified (`provider.VersionVerifier`;
  outside it only `security.allow_unverified_cli: true` lets a run go on, recorded and
  warned). A missing executable for an action used later
  (e.g. Codex for `review`) must fail `orchestrator run` before anything is called.
  `config.Load` parses a version-2 file strictly (unknown keys are an error); a key the
  validator rejects with its own message (a literal secret) must be declared in the schema.
- **Least authority, fixed in Go, fail closed** (MVP.md §38, `specs/permissions/`). Every
  provider call carries a `domain.Authority` (`none`, `web`, `read`, `edit`): workflow
  actions from `domain.AuthorityFor`, the translator and the LLM decision provider
  `AuthorityNone` explicitly — never chosen by a model, a provider default or config, never
  configurable per action, and independent of the run mode. No authority has a shell tool;
  Bash rules that constrain arguments are never a boundary. No call gets both repository
  access and a way out (network, shell). Authority → CLI flags exists only in
  `internal/provider/<name>/buildArgs`; an adapter declares `Supports` only for authorities a
  live check (`go test -tags security ./test/security/...`) has verified, and refuses
  anything else — a silent downgrade is the one unacceptable failure. Never pass
  `bypassPermissions` or any equivalent.
- **Guard implement's result before anything runs it** (MVP.md §38.3): a workflow with
  `implement` needs a clean worktree (unless `security.require_clean_worktree: false`); the
  run records `pre_run_ref`; `action.Guard` compares protected paths (git-ignored ones
  included) and the git config/hooks fingerprint around every `implement` call and every
  test command (both run code a model wrote) and fails the step. The fingerprint is compared
  before any `git status` — git status runs a planted `core.fsmonitor`. `Dispatcher.Guard` is
  required — `implement` and `test` refuse without one. Provider CLIs run with
  `execution.ScrubbedEnv()`, which also drops the credential variables the config names
  (`Config.CredentialEnvNames`, registered right after `Validate`); every process that runs
  `test.command` gets the narrower `execution.TestEnv()` allowlist (plus `test.env_pass`,
  which refuses credential-like names); git keeps the full environment. The test command
  runs under bwrap unless `test.sandbox: none` is written out (`testexec.Sandbox`, probed in
  the fail-fast chain; its mount order is load-bearing). A workflow with `implement` and
  `test` runs the tests once before the first step and refuses a red suite unless
  `run --allow-red-tests` (MVP.md §41.5). After the guard check of every `implement` and
  `test`, what the tree adds is scanned (`internal/secretscan`, stdlib only): a finding
  outside the run-start baseline stops the step (`security.secret_scan.mode: stop`) or is
  recorded (`warn`); the review diff is redacted; a secret's value is never printed, logged,
  persisted or put in a prompt.
- **Only exit codes and git diff are trusted, never LLM prose** (MVP.md §18/§19/§31.4): test
  pass/fail comes only from the test command's real exit code; review inspects the actual
  `git diff`, not just an implementation summary. Never parse phrases like "all tests should
  pass" out of model output as a substitute for running the tests.
- **Decision providers return probabilities; Go decides** (MVP.md §31.2). Never let a
  provider's own pick (e.g. Jev's `choice` field), an LLM, or a caller choose stages or their
  order. Every provider answer is strictly validated; anything unexpected is an
  `InvalidResponse`. The one case where the stage set does not come from a provider is
  `run --stage` (MVP.md §40): the operator names it, `domain.ManualDecision` records it as
  `source: manual`, and it is still never an LLM and never the order.
- **Static provider routing** (MVP.md §31.1): provider/model per action — and the decision
  provider — come from config. No automatic "best model" selection or fallback; a failing
  decision provider leads to the user's manual choice, never to another provider.
- **Secrets never leave the machine**: the Context Manager applies its deny list (`.env*`,
  `*.pem`, `*.key`, `credentials*`, `secrets*`, `context.extra_deny`, to every path segment)
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
  (`limits.prompt`): test output by `action.BoundTestOutput`, the review diff by
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
  `head_ref`. The run directory is `flock`ed by the one `run`/`resume` that owns it.
  Until the operator approves, a resume changes nothing on disk. `run` and `resume` share
  one tail after the decision (`executeWorkflow` in `internal/app/app.go`); keep it that
  way.
- **Commit only on an explicit yes** (MVP.md §42). At the end of a completed run with
  `implement`, the shared tail offers a commit on the current branch (`app.offerCommit`),
  never under `--mode auto`, never for a run started on uncommitted changes, and only after
  `action.CheckBaseline` (fingerprint first) passes — `git commit` runs the repository's
  hooks. The message is the implementation's `summary_short` (without one, the operator
  types it) plus the `Orchestrator-Run` trailer, assembled by Go with no model call.
  `Repository.CommitAll` is the one call that writes the user's index and a branch. Nothing in the offer changes the run's status or exit code.
- Stages always get the user's original request; the translation is only for the decision
  provider and relevant-file search.

## Out of scope (MVP.md §30)

No HTTP provider APIs/OpenRouter/LiteLLM for the AI steps (the HTTP integrations are the Jev
decision API and reading one Jira issue for `run --jira`), no model benchmarking or automatic
model selection/routing/fallback, no parallel or multi-agent workflows, no Web UI/REST
API/IDE plugin, no GitHub/GitLab integration, no Jira beyond reading one issue (no
comments, transitions or JQL), no remote repository execution, no RAG or persistent
conversational memory, no database, no custom dynamically-defined action types or
user-defined workflow language. Don't add abstractions only useful for these.

## Tasks from `specs/*/tasks`

A task is finished by its specification's README ("Ground rules"), not only by green tests:
tick it in that README's table and in `TODO.md` where `TODO.md` tracks it, bring the
README's `Status:` line up to date, and add an `## As built` section to the task file with
the date and what was built differently from the plan, and why. A task under `bench/` also
runs the four gates in `bench/`: it is a separate module, and the root `go test ./...` does
not test it.

## Build & test

```
go build ./...
go vet ./...
go test ./...
gofmt -l .          # must be empty
```

`scripts/check.sh` runs these four gates in the root module and in `bench/` and stops at the
first failure; it is also the orchestrator's `test.command` for this repository, so a run's
test step covers both modules. Under the bwrap sandbox three `bench/` tests skip (nested
bwrap, `orchestrator validate`); run the script outside the sandbox before closing a task.

Tests need no network, no Jev key and no real provider CLIs: `test/integration` uses fake
executables (`internal/testutil/fakeexec`, routed by the `ACTION: <name>` first line of every
prompt; `FAKE_ARGS_DIR` records each call's argv, `FAKE_ENV_DIR` its environment), a fake
Jev (`test/integration/jev_test.go`) and a fake Jira (`test/integration/jira_test.go`). The
live permission suite is separate and spends real calls:
`go test -tags security ./test/security/...`. Golden files:
`go test ./internal/decision/jev -update`, `go test ./internal/logging -update`,
`go test ./internal/jira -update`. Never commit a token-shaped literal (an `AKIA…` key, a
`ghp_…` token): secret scanners and push protection would flag the repository itself;
tests assemble such values at run time (`internal/secretscan`, scenario 27).

## Run artifacts

Persisted under `.orchestrator/runs/<run-id>/`: `run.json` (with `task.source` for a
`run --jira` task, the `decision`,
`pre_run_ref`, `head_ref`, `resume_baseline` (the guard's run-start state), `provider_cli`,
`allow_red_tests`, `pre_run_test`, `secret_scan_baseline`, `secret_findings` (never a
value), `commit` (the SHA the operator approved at the end), the append-only `resumes` (each with its `provider_cli`), and each execution's
`provider.authority` and `attempt`; a review result also records its `diff` summary), `decision.json` (`source: manual` for `run --stage`, with no provider, complexity or latency; full trace:
translation, source, model, depth, complexity with choice/score agreement, every stage's
probability/threshold/forced/reason, latencies),
`project_context.json` (exactly what was sent, only if something was), `workflow.json`,
`ticket.json` (the fetched issue, `run --jira` only; an audit record never read back),
`artifacts/<NNN>-<action>.json`, `logs/<NNN>-<action>.{stdout,stderr}.log`, and for calls
outside steps `logs/translate.*`, `logs/decide-{context,classify}.*` and
`logs/pre-run-test.*`.
