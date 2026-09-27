# AI Model Orchestrator

A local Go CLI that takes one engineering task, decides which workflow stages it needs from
probabilities returned by a decision provider (the Jev decision API or an LLM CLI), builds the
workflow deterministically, and executes it by shelling out to locally-installed AI CLIs
(`claude`, `codex`, `gemini`) against the current git repository, running real tests and
persisting run state to disk. Full spec: `MVP.md` — treat it as the source of truth; the
decision layer in detail: `specs/JEV integration/SPEC.md` (normative) and
`specs/JEV integration/TASKS.md` (plan, decisions, and what was built differently and why);
the permission model: MVP.md §38, with its design record in `specs/permissions/`.
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
are unexported.

`internal/workflow` and `internal/policy` must never import anything but `domain` (the
workflow package also defines its own `StepExecutor` interface). `internal/action` is the
composition point that implements `StepExecutor` using config/provider/repository/testexec;
`internal/app` is the composition root for everything.

## Decision flow (MVP.md §8–§9)

```
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

`internal/classification.Service` runs this flow. Providers: `jev.Provider` (HTTP client or
the offline `jev.NewMockClient()` with `decision.jev.mock: true`) and `llm.Provider` (a CLI
answering the same questions), selected statically by `decision.provider`. Both use the
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

## Non-negotiable invariants

- **Fail fast, before any AI call or HTTP request** (MVP.md §7): `config.Load` →
  `config.Validate` → `config.ValidateDependencies` → `config.ValidateAuthorities` always
  run fully, in order, before any decision client or translator is constructed (the provider
  registry is built first; constructing it calls nothing). `Validate` covers the decision
  surface too; `ValidateDependencies` checks every action's, the translator's and an LLM
  decision provider's executable, and the Jev API key; `ValidateAuthorities` checks that each
  route's provider can enforce its authority. A missing executable for an action used later
  (e.g. Codex for `review`) must fail `orchestrator run` before anything is called.
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
  required — `implement` and `test` refuse without one. Provider CLIs and `test` run with
  `execution.ScrubbedEnv()`; git keeps the full environment. `test.sandbox: bwrap`
  (`testexec.Sandbox`) is probed in the fail-fast chain; its mount order is load-bearing.
- **Only exit codes and git diff are trusted, never LLM prose** (MVP.md §18/§19/§31.4): test
  pass/fail comes only from the test command's real exit code; review inspects the actual
  `git diff`, not just an implementation summary. Never parse phrases like "all tests should
  pass" out of model output as a substitute for running the tests.
- **Decision providers return probabilities; Go decides** (MVP.md §31.2). Never let a
  provider's own pick (e.g. Jev's `choice` field), an LLM, or a caller choose stages or their
  order. Every provider answer is strictly validated; anything unexpected is an
  `InvalidResponse`.
- **Static provider routing** (MVP.md §31.1): provider/model per action — and the decision
  provider — come from config. No automatic "best model" selection or fallback; a failing
  decision provider leads to the user's manual choice, never to another provider.
- **Secrets never leave the machine**: the Context Manager applies its deny list (`.env*`,
  `*.pem`, `*.key`, `credentials*`, `secrets*`, `context.extra_deny`, to every path segment)
  before reading anything, lists files only via git (honouring `.gitignore`), and never reads
  binaries, large files or symlinks. The Jev API key comes only from the environment
  (`decision.jev.api_key_env`), is read in `app`, passed only to the client's constructor,
  and redacted from every error; a literal `api_key` in the config is rejected.
- Provider CLI invocation is argv-only (`exec.CommandContext`), never `sh -c`, for the user's
  task/prompt — the one documented exception is `test.command`, which is trusted operator
  config and does go through `sh -c` to support shell syntax. User text inside prompts
  (request, project context) is embedded as delimited data that cannot close its block;
  repository-derived text in action prompts (git diff, test output) goes through
  `action.fence` (random-nonce delimiter) — defense in depth, never a boundary.
- Stages always get the user's original request; the translation is only for the decision
  provider and relevant-file search.

## Out of scope (MVP.md §30)

No HTTP provider APIs/OpenRouter/LiteLLM for the AI steps (the Jev decision API is the one
HTTP integration), no model benchmarking or automatic model selection/routing/fallback, no
parallel or multi-agent workflows, no Web UI/REST API/IDE plugin, no GitHub/GitLab
integration, no remote repository execution, no RAG or persistent conversational memory, no
database, no custom dynamically-defined action types or user-defined workflow language. Don't
add abstractions only useful for these.

## Build & test

```
go build ./...
go vet ./...
go test ./...
gofmt -l .          # must be empty
```

Tests need no network, no Jev key and no real provider CLIs: `test/integration` uses fake
executables (`internal/testutil/fakeexec`, routed by the `ACTION: <name>` first line of every
prompt; `FAKE_ARGS_DIR` records each call's argv) and a fake Jev
(`test/integration/jev_test.go`). The live permission suite is separate and spends real calls:
`go test -tags security ./test/security/...`. Golden files:
`go test ./internal/decision/jev -update`, `go test ./internal/logging -update`.

## Run artifacts

Persisted under `.orchestrator/runs/<run-id>/`: `run.json` (with the `decision`,
`pre_run_ref`, and each AI step's `provider.authority`), `decision.json` (full trace:
translation, source, model, depth, complexity with choice/score agreement, every stage's
probability/threshold/forced/reason, latencies),
`project_context.json` (exactly what was sent, only if something was), `workflow.json`,
`artifacts/<NNN>-<action>.json`, `logs/<NNN>-<action>.{stdout,stderr}.log`, and for calls
outside steps `logs/translate.*` and `logs/decide-{context,classify}.*`.
