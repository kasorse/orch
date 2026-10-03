# AI Model Orchestrator

A local Go CLI that takes one engineering task, decides which workflow stages it needs, and
executes that workflow by shelling out to locally-installed AI CLIs (`claude`, `codex`,
`gemini`) against your current git repository — running real tests and persisting run state
to disk. The full spec is `MVP.md` (the decision layer in detail: `specs/JEV integration/`);
`CLAUDE.md` summarizes the invariants that guide the implementation.

A **decision provider** — the [Jev](https://docs.typesafe.ai) decision API, or one of the
CLIs — only estimates probabilities: does the task need repository context, how complex is
it, how likely is each stage (research, analysis, planning, development, testing, review)
needed. Everything chosen from those numbers is Go code: configurable thresholds pick the
complexity and the stages, the stages always run in the same order, and a deterministic state
machine drives them. The AI models only do reasoning (research, analysis, planning,
implementation, review) — they never decide what happens next. Test pass/fail is decided only
by the real exit code of your test command, never by anything a model says.

## Prerequisites

- Go 1.26.1 or later.
- `git`, on `PATH`, and the target repository must already be a git repo.
- For each provider referenced in your config (`claude`, `codex`, and/or `gemini`) — by an
  action, the translator, or an LLM decision provider — that provider's CLI must be installed
  and resolvable via `PATH` (`exec.LookPath`). You only need the providers your config
  actually uses.
- With `decision.provider: jev`: a Jev API key in the environment variable named by
  `decision.jev.api_key_env` (default `JEV_API_KEY`) — or `decision.jev.mock: true` to run
  without one (see below).
- Only for `run --jira`: a `jira` section in the config and the Jira credentials in the
  environment variables it names (see [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket)).

## Building

```
go build -o orchestrator ./cmd/orchestrator
```

## Configuration

Copy `orchestrator.example.yaml` to `orchestrator.yaml` (the default config path; override with
`--config <path>`) and adjust it. Version 2 is required; a version 1 config (with
`classifier` and `actions.answer`) is rejected with a list of what changed.

```yaml
version: 2

decision:
  provider: jev              # jev | claude | codex | gemini
  model: jev-1.13.0          # jev: a version, not an alias; CLI provider: its model
  timeout: 5s                # jev: one HTTP request; CLI provider: one call
  complexity:
    primary: choice          # choice | score - the question the policy decides on
    shadow: true             # also ask the other one, for comparison in the trace
  on_failure: ask            # ask: choose complexity and stages by hand; fail: stop
  jev:                       # read only when provider is jev
    base_url: https://api.typesafe.ai
    api_key_env: JEV_API_KEY
    max_retries: 1
    mock: false

translation:
  provider: claude
  model: haiku
  timeout: 60s

policy:
  context_depth:
    project_threshold: 0.5
  complexity:
    complex: 0.40
    medium: 0.50
  stages:
    thresholds: {research: 0.70, analysis: 0.60, planning: 0.65, development: 0.60, testing: 0.60, review: 0.70}
    force:
      complex: [analysis, planning, review]

context:
  max_files: 20
  max_bytes: 24576
  extra_deny: []

actions:
  research:  { provider: claude, model: sonnet }
  analyze:   { provider: claude, model: sonnet }
  plan:      { provider: claude, model: sonnet }
  implement: { provider: claude, model: opus }
  review:    { provider: claude, model: sonnet, timeout: 20m }

test:
  command: "go test ./..."

limits:
  max_steps: 12
  max_fix_iterations: 2

execution:
  timeout: 15m

security:
  require_clean_worktree: true
  protected_paths: []
```

- **`decision`** chooses who estimates the probabilities, like an action chooses its
  provider:
  - `jev` — the Jev HTTP API. The API key is read from the environment variable
    `api_key_env` names; a literal `api_key` in the file is a validation error.
    `max_retries` retries only 408/429/5xx/529, honouring `Retry-After`.
  - `jev` with `jev.mock: true` — an offline stand-in for Jev: no network, no key,
    deterministic answers from keywords in the request. Useful while you have no API
    access; a warning is printed and the trace marks the model as `mock:<model>`. The
    numbers are plausible, not calibrated.
  - `claude` / `codex` / `gemini` — the same questions answered by that CLI (today only
    `claude` passes the authority check, see [Security model](#security-model)).
  - The flow is the same for all of them. A failing provider never falls back to another:
    with `on_failure: ask` you choose the complexity and stages yourself (interactive runs
    only); with `fail` the run stops.
  - `complexity.primary` picks the question the policy decides on; with `shadow` on, the
    other one is asked too and only recorded, so the two can be compared.
- **`translation`** — a request that is mostly Russian is translated to English for the
  decision provider (the stages still get your original text). If translation fails, the
  original is sent and a warning is printed.
- **`policy`** turns probabilities into decisions: `project_threshold` for collecting
  project context; the cumulative complexity thresholds (complex is checked first); a
  per-stage threshold (inclusive); and stages forced on for a complexity. If no stage
  qualifies, the workflow is `analysis`. The same thresholds apply to every decision
  provider.
- **`context`** bounds the project context sent to the decision provider; `extra_deny`
  adds glob patterns that are never read.
- Each entry under **`actions`** independently chooses a `provider` (`claude` | `codex` |
  `gemini`) and a `model` string passed straight through to that provider's CLI — there's no
  automatic "best model" selection, fallback, or benchmarking. `timeout` optionally tightens
  that action's share of `execution.timeout`. A provider must be able to enforce the
  action's authority: `codex` and `gemini` currently cannot, so routing an action (or the
  translator, or the decision provider) to them fails validation with exit 2.
- `test.command` runs via `sh -c` in the target repository (the one place a shell is used —
  it's trusted operator configuration, not user input). `test.sandbox: bwrap` runs it under
  bubblewrap — see [Security model](#security-model); `test.ro_binds` / `test.rw_binds`
  re-expose paths such as a module cache (read-only) and a build cache (writable).
- `limits.max_steps` bounds total workflow steps; `limits.max_fix_iterations` bounds how many
  times a failing test or review can send the workflow back to `implement` — both exist purely
  to guarantee termination.
- `execution.timeout` (Go duration syntax, e.g. `15m`) bounds the whole run, including the
  decision — except the time you spend on a manual choice.
- **`security`** (optional) never widens what a step may do. `require_clean_worktree`
  (default `true`) makes a workflow with `implement` refuse to start on a dirty worktree;
  `protected_paths` adds patterns `implement` must not change. See
  [Security model](#security-model).
- **`jira`** (optional) is where `run --jira` reads a ticket from. It is validated whenever
  present, but its credential variables are needed only by `run --jira` (and checked by
  `validate`). See [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket).

## Security model

Every provider call gets the least tools its job needs, chosen by a fixed table in Go code —
never by the model, a provider default or config. **No step has a shell.** A provider that
cannot enforce an authority is refused before any AI call, and nothing ever runs a CLI with
its permission checks turned off.

| call | authority | may | may not |
|---|---|---|---|
| translation, decision | `none` | answer from the prompt | use any tool |
| `research` | `web` | search and fetch web pages | read any file |
| `analyze`, `plan`, `review` | `read` | read and search the repository | write, run commands, use the network |
| `implement` | `edit` | read, create and modify files **inside the repository** | run commands (not even `go build`), use the network, write outside the repository, touch `.git/` or `.claude/` |
| `test` | — | the orchestrator runs `test.command` itself | — |

No call has both repository access and a way out (network or shell). Anything a call was not
granted is denied on the spot, never prompted, so no mode can hang on a permission question.
**Modes do not change authority**: `manual`, `accept-edits` and `auto` all run each step with
the table above; they differ only in how much you see and confirm between steps.

For `claude` that means `--restricted --permission-mode dontAsk --permission-prompts none
--strict-mcp-config --setting-sources ""` plus the authority's `--tools`. Consequences:

- The target repository's `.claude/settings.json` hooks, `.mcp.json` servers and settings are
  never loaded.
- Your own `~/.claude/settings.json` is not loaded either. Authentication that lives in the
  environment or in Claude Code's credentials keeps working; if yours depends on `env` or
  `apiKeyHelper` in user settings, export the variable instead.
- The file tools cannot read the secret deny list — `.env`, `.env.*`, `*.pem`, `*.key`,
  `credentials*`, `secrets*` and `context.extra_deny` — the same list the Context Manager
  applies. The match is case-sensitive (`.ENV` is not covered).

Around `implement` the orchestrator itself guards the result, for any provider:

- A workflow with `implement` refuses to start on a dirty worktree (commit or stash first, or
  set `security.require_clean_worktree: false`).
- Before the first step it records a restore point, printed as `Restore point: <sha>` and
  saved as `pre_run_ref` in `run.json`: HEAD, or on a dirty tree a snapshot commit of
  everything (untracked files included) that leaves your tree, index and stash alone.
  `git checkout <sha> -- .` brings the files back. The commit is pinned under
  `refs/orchestrator/runs/<run-id>` so `git gc` never prunes it; those refs are not
  branches or tags and are never pushed by default. They are kept until you drop them:
  `git for-each-ref refs/orchestrator` lists them,
  `git update-ref -d refs/orchestrator/runs/<run-id>` removes one.
- After every `implement` call, before anything runs its output, it checks that no protected
  file changed — `.claude/**`, `.mcp.json`, `.github/workflows/**`, `.gitlab-ci.yml`,
  `.envrc`, `.vscode/**`, the config file when it lives in the repository (even if
  git-ignored), `security.protected_paths` — and that `.git/config` and the git hooks are
  untouched. Otherwise the run stops (exit 4) naming the files; the changes stay for you to
  inspect.
- In `manual` mode the prompt before the step after `implement` lists what it changed.
- What a resume reads back — every run's `run.json`, `workflow.json`, `decision.json` and
  `artifacts/` — is protected the same way. The file tools are denied the whole
  `.orchestrator/runs/` directory as well.
- The same check runs around the test command, which executes code the model wrote: a test
  that plants a hook, CI file or agent setting stops the run too. `.git` is compared before
  the orchestrator runs `git status`, which would otherwise execute a planted
  `core.fsmonitor`.

### Sandboxing the test step

With `test.sandbox: bwrap` the test command runs under
[bubblewrap](https://github.com/containers/bubblewrap):

| | access |
|---|---|
| the filesystem (`/usr`, `/etc`, your toolchain) | read-only |
| the repository | read-write; its `.git` read-only |
| `/tmp` | a private, empty, writable tmpfs (`TMPDIR=/tmp`) |
| `$HOME` (`~/.ssh`, `~/.aws`, `~/.config/gh`, …) | hidden — an empty tmpfs |
| `test.ro_binds` / `test.rw_binds` | re-exposed read-only / read-write |
| network | none |

So a test can build and write inside the repository, but cannot read your credentials, send
anything out, or write outside the repository. For Go, re-expose the module cache and the
build cache, or every run starts cold and cannot download modules:

```yaml
test:
  command: "go test ./..."
  sandbox: bwrap
  ro_binds: [~/go/pkg/mod]
  rw_binds: [~/.cache/go-build]
```

Tests that need the network or outside services (a database, docker) fail in the sandbox;
keep `sandbox: none` for them. Linux only. `run` and `validate` probe bwrap before any AI
call; on Ubuntu 24.04 and later AppArmor blocks bwrap's user namespaces until you install a
profile for it:

```bash
sudo tee /etc/apparmor.d/bwrap > /dev/null <<'PROFILE'
abi <abi/4.0>,
include <tunables/global>
profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE
sudo systemctl reload apparmor
```

Provider CLIs and the test command also do not inherit the variables through which a Claude
Code session you launched the orchestrator from reaches its children (`CLAUDECODE`,
`CLAUDE_CODE_MESSAGING_*`, …), nor the credential variables your config names — the one in
`decision.jev.api_key_env` and, with a `jira` section, `jira.token_env` and
`jira.email_env`. The orchestrator reads those itself; a test the model wrote cannot.

### What is not protected

- **Without `test.sandbox: bwrap`, code the model writes runs in `test`** with your
  permissions — a test file can do anything your user can; reviewing the changes in `manual`
  mode before `test` is then the only mitigation. With the sandbox, a test can still read
  what your user can read outside `$HOME`, and change anything inside the repository.
- **Your repository goes to the model provider** — that is the job. Only the deny list above
  is withheld, and only from the file tools.
- **Prompt injection is reduced, not eliminated.** Text in the repository or the diff can
  still mislead a step; what it can make that step do is bounded by the table above, and the
  guard, `review` and the diff expose the result.
- **`test.command` is trusted configuration** run through `sh -c`. For a repository you do not
  control, pass `--config` from outside it, use `test.sandbox: bwrap`, keep `manual` mode,
  and run the whole thing in a container.
- **A Jira ticket is task text written by whoever can edit it.** With `run --jira` it is
  treated exactly like text you typed: printed in full before anything is decided, kept
  verbatim in `run.json` and `ticket.json`, and given no extra authority — but also no
  extra gate, even in `--mode auto`. Read the printed task when the ticket's author is not
  you.
- **A hostile provider CLI binary** is out of scope.
- **A provider CLI orphaned by `kill -9`** of the orchestrator can keep writing after the
  last check. A resume catches protected files and a malformed `run.json`, but a subtly
  edited artifact could reach the next prompt. Check the history a resume prints before
  approving it.

Details, the threat model and the rejected alternatives: `specs/permissions/`; normative
summary: `MVP.md` §38.

## How a workflow is decided

1. **Language.** A request whose prose is at least 30% Cyrillic is translated.
2. **Pre-check.** The decision provider estimates whether classifying the request needs the
   repository; above `project_threshold`, project context is collected.
3. **Project context.** A compact summary — never whole files: language and dependencies
   from the manifest, test layout, bullet rules from `AGENTS.md`/`CLAUDE.md`/
   `CONTRIBUTING.md`, matching README/docs sections, the files relevant to the request with
   one-line summaries, git branch and changed paths.
4. **Classification.** Complexity distribution(s) and each stage's probability.
5. **Policy.** Thresholds pick the complexity and the stages; forced stages are added.
6. **Workflow.** `research → analyze → plan → implement → test → review`, filtered to the
   enabled stages. A workflow without `implement` ends by printing the answer written by its
   last read-only step.

## What is sent to Jev

With `decision.provider: jev`, two requests per run go to `decision.jev.base_url`
(`https://api.typesafe.ai` by default):

- the request text — the English translation when one was made;
- when project context is needed, the context described above: paths, one-line summaries,
  rules, doc excerpts, dependency names, test layout, git branch and changed paths.

Never sent: file contents beyond those summaries, anything matching the deny list
(`.env`, `.env.*`, `*.pem`, `*.key`, `credentials*`, `secrets*`, plus `context.extra_deny` —
matched against every path segment), files ignored by `.gitignore`, binaries, files over
256 KB, symlinks, `.git/`, `.orchestrator/`. `project_context.json` in the run directory is
exactly what was sent. Per Jev's documentation, customer data is not used for training, and
zero data retention is offered only on the enterprise plan — check their current terms before
sending proprietary context. With `jev.mock: true` nothing leaves the machine; with an LLM
decision provider the same data goes to that CLI. With `run --jira`, the request text is the
rendered ticket (and your notes), so the ticket goes to the decision provider and, when it
is mostly Russian, to the translator, exactly as typed text would.

## What is sent to Jira

Only with `run --jira`, and only one read: `GET <jira.base_url>/rest/api/2/issue/<KEY>`
asking for the summary, description, issue type, parent and your configured `fields`, with
your credentials in the `Authorization` header. Nothing is ever written to Jira. The
request goes to `jira.base_url` only — never to a URL you pasted, and never through a
redirect — over `https` (plain `http` is accepted only for `localhost`/`127.0.0.1`/`::1`).

## Usage

```
orchestrator [--config path] version
orchestrator [--config path] validate
orchestrator [--config path] run [--verbose|-v] "<task description>"
orchestrator [--config path] run --jira <KEY|URL> ["notes"]
orchestrator [--config path] run --stage <name> [--stage <name> ...] "<task>"
orchestrator [--config path] [--mode m] resume <run-id>
orchestrator decisions report
```

- `version` — prints the build version.
- `validate` — loads and validates the config, including checking that every referenced
  provider CLI is actually resolvable on `PATH`, that the Jev API key is set and, when the
  config has a `jira` section, that its credential variables are set, and reports the
  result without running anything. With a real (not mock) Jev it also asks Jev whether it
  serves `decision.model` (`✓ jev serves jev-1.13.0`). Run this first when setting up a new
  config. Unknown keys in a version-2 config (a typo such as `timout`) are an error, listed
  with their line numbers.
- `run "<task>"` — decides the workflow (printing the decision: context depth, complexity,
  who decided, the workflow) and executes it step by step against the repository in the
  current working directory, printing progress as it goes.
- `run --jira <KEY|URL> ["notes"]` — takes the task from a Jira ticket. See
  [Taking the task from a Jira ticket](#taking-the-task-from-a-jira-ticket).
- `run --stage <name> [--stage <name> ...] "<task>"` — runs exactly the stages you name and
  skips the decision layer (no translation, Jev or LLM call, project context). See
  [Choosing the stages yourself](#choosing-the-stages-yourself).
- `resume <run-id>` — continues a run that failed, was stopped, was only planned
  (`--mode plan`) or whose process died, from where it stopped. See
  [Resuming a run](#resuming-a-run).
- `decisions report` — compares the two complexity questions (`choice` and `score`) over
  the `decision.json` of every run in `.orchestrator/runs/`: agreement rate, a 3×3 matrix,
  mean confidence, and the runs where they disagree. Manual (`--stage`) runs, runs chosen by
  hand, mock answers and runs without the shadow question are skipped. It needs no config and
  calls nothing. Until there is Jev access, `decision.provider: claude` collects real data.
- `--mode plan|manual|accept-edits|auto` — without it, `run` asks you to approve or cancel the
  decided workflow and then to pick a mode; `manual` confirms every step. `auto` asks nothing
  (and, if the decision provider fails, stops instead of asking you to choose).
- `--verbose` / `-v` — the sole verbosity switch (no config key or environment variable
  equivalent). Adds the per-stage decision table (probability, threshold, forced, reason),
  each AI step's `Authority:` line and, for every provider call including translation and
  LLM decisions: the child process's live output prefixed with `  | `, a heartbeat line
  every 30s while the call is in flight, and a `✓ completed (Ns)` line. Without `--verbose`
  you only see a `→ running <provider>...` line before each call. The redacted command line
  (the prompt argument is always replaced with `<prompt: N chars>` — the prompt body itself
  is never printed or logged, anywhere) and the working directory appear in a failure's
  message, with the path of the full log.
- **Terminal UI.** When stdout is an interactive terminal, the same information is rendered
  the way agentic CLIs like Claude Code do: a boxed run header, `⏺` blocks with `⎿` details,
  probability bars for the stages, an animated spinner with elapsed time while a model or
  the test command works, coloured review severities, styled prompts and a boxed summary
  with the chain of steps that ran. Piped or redirected output (CI logs, `| tee`) stays the
  plain text described above, byte for byte. `NO_COLOR=1` or `ORCHESTRATOR_PLAIN=1` forces
  plain text on a terminal too. Preview it without running a model:
  `ORCHESTRATOR_DEMO=1 go test ./internal/logging -run TestPretty_FullRun -v`.

Config loading, structural validation, dependency validation (executables on `PATH`, the
Jev API key) and the authority check (every route's provider can enforce its authority) always
run to completion, in that order, *before* any AI provider is invoked or any request is sent
to Jev — a missing executable for an action used later in the workflow (e.g. `codex` for
`review`) fails `run` immediately. `orchestrator validate` runs the same chain. With a real
Jev, `run` then asks Jev whether it serves `decision.model` before anything else; with
`on_failure: ask`, an unreachable Jev is only a warning there.

`execution.timeout` bounds the entire run, including the decision — not just workflow step
execution. Each provider/test child process runs in its own process group, so a timeout or
Ctrl-C kills the whole process tree it spawned, not just the direct child.

If the target repository has uncommitted changes when `run` starts, a workflow with
`implement` refuses to start (see [Security model](#security-model)); any other workflow
prints a warning and continues. The orchestrator never stashes, resets, or otherwise touches
your working tree on your behalf.

### Resuming a run

```
orchestrator resume 3f2a9c1e             # the full run ID, or a unique prefix of ≥ 8 characters
orchestrator --mode auto resume 3f2a9c1e # unattended
```

The run ID is printed as `Run: <id>` when the run starts. Resume **never decides again**. It
reuses the run's decision and workflow and makes no Jev or decision-provider call. From the
run's history it works out where to continue: a failed step is retried, and after a failed
test it goes back to `implement` with the test output, as if the run had never stopped. Steps
that already succeeded are not paid for again. The current config applies from here on:
providers, models, timeouts, limits, test command.

Before asking anything it shows the previous attempt, every step so far, the working tree,
the next step and the remaining workflow. Without `--mode` you approve and pick a mode as for
`run`, and pressing Enter keeps the run's earlier mode. A run that was `auto` still asks, so an
unattended resume needs `--mode auto`.

A resume refuses, before anything runs:

- when a protected file, `.git/config` or a git hook differs from when the run started — for
  example after a step the guard stopped, or a crash mid-step. `.git` is compared before any
  `git status`. Revert those files to resume, or start a new run to keep them;
- when HEAD has moved since the run started, because review diffs against HEAD
  (`git reset --soft <head_ref>` brings it back with your changes kept);
- when a limit is already used up. Limits count the whole history, so raise the named key
  (`limits.max_fix_iterations`, `limits.max_steps`) first;
- when `run.json` or an artifact was edited, is missing or does not match (`corrupt`,
  exit 1), or when another orchestrator process is working on the run (`in_use`, exit 1).

Your own edits to other files between the stop and the resume are fine. They show up in the
working tree listing, and `review` sees them in the diff. A completed run is just reported
(with its answer, for a read-only workflow). Details: `MVP.md` §37 and `specs/resume/`.

### Choosing the stages yourself

```
orchestrator run --stage review "review my change"
orchestrator run --stage testing --stage review "check the hand edit"
orchestrator run --stage plan "plan the cache"
orchestrator run --stage review --jira PMC-2789
```

Use it when you already know what you need: review code you changed by hand, just run the
tests, plan without implementing. Names are `research`, `analysis`, `planning`,
`development`, `testing`, `review`, plus the aliases `analyze`, `plan`, `implement`, `test`.
Repeat `--stage` for several; one stage per flag. The order is always the fixed one, whatever
order you give them in, and the retry rules, permissions, test command and resume are the same
as for a decided run. `decision.json` records `source: manual`.

* A stage whose usual input you skipped (planning without analysis, development without a
  plan) prints a warning and still runs. Testing and review never warn.
* `review` looks at your uncommitted changes against `HEAD`. With no changes it fails with
  `nothing to review` before calling the model.
* `development` still needs a clean worktree (or `security.require_clean_worktree: false`);
  without it a dirty tree only prints the usual warning.
* The config must still be valid and every executable it names present, including the
  decision provider's and the Jev key, even though they are not called.

### Taking the task from a Jira ticket

```
orchestrator run --jira PMC-2789
orchestrator run --jira pmc-2789 "only the FE part; leave pim-qa alone"
orchestrator run --jira https://yourcompany.atlassian.net/browse/PMC-2789 --mode plan
```

The orchestrator reads the issue once, renders its key and summary, type, parent, a link,
the description and any configured `fields` into plain task text, appends your notes last
(`Operator notes:`), and prints the whole text before deciding anything. From there the run
is exactly a typed `run`. Wiki markup (`{{code}}`, `*bold*`, `#` lists) is passed through
verbatim — models read it fine. A ticket that renders to more than `jira.max_bytes` is
refused, never cut. A moved issue is followed to its new key, with a warning. A URL must be
the issue's `/browse/<KEY>` link on your configured instance; board and search links are
refused.

Add a `jira` section to the config (it is commented out in `orchestrator.example.yaml`):

```yaml
jira:
  base_url: https://yourcompany.atlassian.net   # Data Center: including any context path
  auth: basic                  # basic: Jira Cloud | bearer: Data Center
  email_env: JIRA_EMAIL        # basic only
  token_env: JIRA_API_TOKEN
  fields:                      # optional extra text fields, rendered in this order
    - id: customfield_10035
      label: Acceptance criteria
```

- **Jira Cloud:** create an API token at <https://id.atlassian.com/manage-profile/security/api-tokens>,
  use `auth: basic`, and export your account email and the token:
  `export JIRA_EMAIL=you@example.com JIRA_API_TOKEN=...`.
- **Jira Data Center / Server:** create a personal access token in your Jira profile, use
  `auth: bearer`, leave out `email_env`, and export the token in the variable `token_env`
  names.
- **Custom field ids:** `GET <base_url>/rest/api/2/field` (e.g. `curl -u "$JIRA_EMAIL:$JIRA_API_TOKEN"
  https://yourcompany.atlassian.net/rest/api/2/field`) lists every field with its `id` and
  `name`. Only text fields work; a select, user or number field fails the run with a message
  naming it.

Credentials only ever come from the environment — a `token`, `api_token`, `password`, `pat`
or `email` key in the file is rejected. They are needed only by `run --jira`; a plain `run` and a
`resume` work without them, even with the section configured. A resume never re-reads the
ticket: the task saved when the run started is the task, and the resume header says so
(`Task from PMC-2789, fetched … (not re-fetched)`).

When the ticket cannot be read, nothing is created and no AI is called: bad credentials,
a missing or invisible issue, or a redirect exit 2; Jira being down or answering
unexpectedly exits 3. Details: `MVP.md` §39 and `specs/jira/`.

## Run artifacts

Each `run` persists its state under `.orchestrator/runs/<run-id>/` in the target repository:

```
.orchestrator/runs/<run-id>/
  run.json              # the task (with task.source for run --jira), run status, the
                        # decision, step history (with each AI step's
                        # provider.authority and attempt), limits/iteration counters,
                        # pre_run_ref, head_ref, resume_baseline, resumes
  decision.json         # the full decision trace (see below)
  project_context.json  # exactly the context sent to the decision provider, if any
  workflow.json         # the workflow built from the decision
  ticket.json           # run --jira only: the issue as fetched (a record; never read back)
  artifacts/<NNN>-<action>.json          # structured output per step
  logs/<NNN>-<action>.{stdout,stderr}.log  # raw provider/test output per step
  logs/translate.{stdout,stderr}.log       # the translation call, if one was made
  logs/decide-{context,classify}.{stdout,stderr}.log  # LLM decision provider calls
```

`decision.json` answers "why did this run have these stages?": the request and its
translation, who decided (`jev`, `llm`, or `user` with the provider's error), the model
version that answered, the pre-check distribution and chosen depth, both complexity questions
with their picks and whether they agree, every stage's probability, threshold, forced flag and
reason, the workflow, and how long each decision step took.

Every provider call's raw stdout/stderr is persisted this way, whether it succeeds or fails —
not just failures.

Add `.orchestrator/` to the target repository's own `.gitignore` if you don't want this
bookkeeping tracked there.

## Reading a failure

When a provider call fails, `run` exits non-zero (see `MVP.md` §24 for the exact codes) and
prints a self-contained error, e.g.:

```
provider execution failed

provider: claude
action: implement
model: opus
failure: exit (the CLI ran and exited non-zero)
command: claude --print --restricted --permission-mode dontAsk --permission-prompts none --strict-mcp-config --setting-sources '' --settings '{"permissions":{"deny":["Read(**/.env)",...]}}' --tools Read,Grep,Glob,Edit,Write --allowed-tools Edit,Write --model opus <prompt: 1832 chars>
working dir: /home/user/repo
exit code: 1
duration: 2.4s

stdout:
API Error: Connection error.

stderr:
(empty)

full output: .orchestrator/runs/2f3c.../logs/002-implement.stdout.log
```

A decision provider failure reads `decision provider <name> failed: <kind>` — `timeout`,
`connection`, `rate limited`, `unavailable`, `auth`, `bad request` or `invalid response` —
with the HTTP status, the number of attempts and a bounded excerpt of the response (never the
API key). It exits 3, or 2 for `auth`.

A failed `run --jira` fetch names the ticket and what to fix, e.g. `PMC-2789 does not exist,
or the account behind JIRA_EMAIL cannot see it (HTTP 404)` or `Jira rejected the credentials
in JIRA_EMAIL/JIRA_API_TOKEN (HTTP 401)`, never a credential. It exits 2 for credentials, a
missing issue or a redirect, 3 when Jira is down or answers unexpectedly, and creates no run.

- `failure:` says *how* the call failed: `spawn` (couldn't start the executable at all),
  `exit` (ran and returned non-zero), `timeout` (exceeded `execution.timeout`), or `cancelled`
  (SIGINT/SIGTERM). Some provider CLIs — notably `claude --print` — write backend/API errors to
  stdout rather than stderr, so both streams are always shown; a blank stream renders as
  `(empty)` rather than nothing, so you can tell "no output" from "output not captured".
- `stdout:`/`stderr:` are each truncated to their last 4000 bytes in this message. The
  `full output:` line, when present, points at the untruncated file on disk.
- `command:` is the real argv that ran, except the prompt argument, which always renders as
  `<prompt: N chars>` — the prompt text itself is never printed or written to any log file.

## Testing

```
go build ./...
go vet ./...
go test ./...
gofmt -l .   # should print nothing
```

`go test ./...` requires no network access, no Jev key and no real `claude`/`codex`/`gemini`
installs: the `test/integration` suite uses fake executables installed on a scoped `PATH` and
a fake Jev served by `httptest`.

The live security suite checks that `claude` actually enforces each authority — every
refusal paired with a control showing the same harness sees the action when it is allowed.
It makes about a dozen small real calls (model `haiku`, override with
`ORCH_SECURITY_MODEL`) and needs network; its bwrap test skips where bwrap cannot sandbox:

```
go test -tags security ./test/security/...
```

## Limitations

This is an MVP, deliberately scoped down (see `MVP.md` for the full rationale). Notably absent:

- No HTTP-based provider APIs (OpenRouter, LiteLLM, etc.) — the AI steps run only as local
  CLI subprocesses. The HTTP integrations are the Jev decision API, which only returns
  probabilities, and reading one Jira issue for `run --jira`.
- No automatic model selection, routing, or fallback between providers — including between
  decision providers.
- Stages cannot yet be edited before a run (approve or cancel only); the decision thresholds
  are calibrated for Jev and shared with LLM decision providers.
- No parallel or multi-agent workflows — one task, one sequential workflow, one run at a time.
- No web UI, REST API, or IDE plugin — CLI only.
- No GitHub/GitLab integration or remote repository execution — operates on the local working
  directory only. Jira is read-only and limited to one issue: no comments, transitions,
  linked issues, attachments or JQL.
- No RAG, persistent conversational memory, or database — state is per-run JSON on disk.
- No custom/dynamically-defined action types or user-defined workflow language — the stage
  set, their order and the policy rules are fixed in Go code; only thresholds are configured.
