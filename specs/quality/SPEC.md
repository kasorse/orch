# Spec: Quality — a better result from the same stages (phase 2)

Status: **specified** (2026-10-08). The design questions are answered (§14), the author's
defaults are in §15. MVP.md becomes the normative home when QL14 lands (a new §44, plus
§31.1 and §38); until then this file is the design source.
Scope: TODO.md phase 2, items Q1, Q2, Q3, Q5, Q6 and Q7. Q4 (worktree isolation) goes to its
own spec together with W1; Q8 (the cost of the decision rounds) waits for the benchmark
decision B4 (§16). Touches MVP.md §11–§12 (engine, limits), §18–§19 (test, review), §25
(limits), §31.1 (static routing), §37 (resume), §38 (permissions), §41 (pre-run test).

---

## 1. Problem

Each stage is a separate cold `claude --print` call. The orchestrator decides what the next
stage learns about the previous one, and today that is too little. The loop between
`implement` and `test` is also too coarse. Each problem was checked against master at
`14b6b6f`:

1. **Stages lose what the previous stage found.** `BuildPlanPrompt` passes the analysis
   summary, root cause and recommendation, but not `affected_files` or `risks`.
   `BuildImplementPrompt` adds the plan's summary and steps, but not its files or risks.
   `BuildReviewPrompt` gets only the analysis and plan summaries. A retried `implement` sees
   the previous attempt's one-line summary, not what it changed
   (`internal/action/prompts.go`). Every stage searches the repository again for files an
   earlier stage already named.
2. **`implement` works blind.** It has no shell (authority `edit`: Read, Grep, Glob, Edit,
   Write; `specs/permissions/DECISIONS.md` D6), so it cannot build or run a test. Feedback
   comes only from the orchestrator's `test` step after the session ends, and then a new cold
   session starts, at most `limits.max_fix_iterations` (2 by default) times. Bare Claude Code
   edits and tests dozens of times in one session. This is the largest quality loss
   (TODO.md §0.2).
3. **The test command is the only deterministic gate.** Code that passes the tests but fails
   `go vet`, a linter or a SAST scan passes the run. Whether review notices depends on the
   model.
4. **Review has nothing to check against.** The plan has no checkable criteria, so review
   judges the diff against the task text alone. `medium` issues are printed and dropped.
   Review is one pass by one model.
5. **The fix loop cannot tell it is stuck.** If `implement` changes nothing, or the same tests
   fail with the same output, or review repeats the same issue, the run spends its remaining
   iterations anyway (`internal/workflow/engine.go`, no progress check). The retry prompt
   carries the raw, bounded test output with no list of the failing tests, and nothing says
   which failures were already there before the run.
6. **Every step uses one model regardless of complexity.** The decision round computes
   `simple | medium | complex`, but it only feeds `policy.stages.force`. A one-line fix runs
   on the same model and effort as a redesign.

## 2. What already works

- Analysis and plan artifacts already carry `affected_files` and `risks`
  (`domain.AnalysisArtifact`, `domain.PlanArtifact`). The plan parser is lenient and falls
  back to `{summary: output}` (`internal/action/plan.go`).
- Retry state lives in one place: `Dispatcher` fields set only by the `apply*` methods
  (`internal/action/restore.go`), and a resume replays the same methods. A new piece of retry
  context therefore needs no separate resume path.
- `Repository.WorktreeTree` and `Repository.DiffNumstat` (`internal/repository/git.go`)
  compute a stat between any two trees, and `action.stepDiff` already uses them against
  `pre_run_ref`.
- `action.fence` and `action.BoundTestOutput` embed repository data as delimited, bounded
  blocks (MVP.md §41.1).
- The test step runs `test.command` through `testexec.Runner` under bwrap, with the
  `execution.TestEnv()` allowlist, inside `guardedTest` (guard snapshot → run → compare). A
  secret scan follows (`internal/action/testaction.go`). The pre-run test reuses
  `guardedTest` (MVP.md §41.5).
- The guard compares the git fingerprint first, over `--git-common-dir` config and the real
  hooks path, then the protected paths (`action.RepoGuard`, `repository.GitFingerprint`).
  `GuardState` marshals to JSON.
- The claude adapter takes opt-in MCP servers: `--strict-mcp-config --mcp-config <json>`, with
  each MCP tool named in `--allowed-tools` (`provider.Extensions`). The live suite already has
  a stdio MCP server written in shell and an allowlist check
  (`test/security/extensions_test.go`, E1/E2).
- The review verdict is Go's: `ReviewResult.Decide` (any critical or high issue → fail). The
  review answer is parsed strictly (`parseReviewStrict`).
- Resume derives where to continue from the history and the transition table
  (`workflow.Entry`, `outcomeOf`, `Counters`), never from a stored "next state".

## 3. Principles

1. **Only exit codes decide pass/fail** (MVP.md §31.4). Checks are exit codes. Test-output
   parsing (§8.2) only names failing tests in a prompt. The in-session `run_tests` (§6) is a
   hint to the model, and the `test` step stays the source of truth.
2. **No shell for `implement`, still** (D6). The model gets fixed tools whose command comes
   from trusted operator config, never from the model. The one model-supplied value, a test
   filter, is a regex-checked string passed in an environment variable and never put into a
   command line.
3. **Guard before running model-written code.** Everything that executes code the model
   wrote (a check, `run_tests` mid-session) runs only after the guard confirms that hooks,
   git config and protected paths are untouched.
4. **Go decides, from config.** Which model runs a step under `by_complexity`, when a run
   stops for lack of progress, and whether a `medium` issue goes back to `implement` are
   decided by Go code from the config and the persisted history. An LLM decides none of
   them.
5. **Resume continues, never re-decides** (MVP.md §37). Every new transition is derived from
   persisted results and options recorded at run start, so a resumed run goes where the
   original would have.
6. **Opt-in for anything that costs money or changes behavior.** `by_complexity`,
   `implement.verify`, `review.fix_medium` and extra reviewers are off unless configured.
   Prompt context (§4), checks (§5) and the no-progress stop (§8.1) are on, because they only
   make a run cheaper or more informed.

---

## 4. Stage context (QL01) — TODO Q1

### 4.1 What each stage receives

| stage | today | after |
|---|---|---|
| plan | analysis summary, root cause, recommendation | + analysis `affected_files`, `risks` |
| implement | analysis summary, root cause, recommendation; plan summary, steps | + analysis and plan `affected_files`, `risks`; plan `acceptance_criteria` |
| implement (retry) | previous attempt's summary | + the run's diff stat so far (§4.3) |
| review | analysis summary, plan summary | + plan `steps`, `risks`, `acceptance_criteria` (§7.1) |

Lists are rendered as `- item` lines under a heading. An empty list is left out. The list
items come from a model's artifact, so they are data. They go into the prompt the same way
the analysis summary does today, as plain text after the task block. A model cannot use
them to close the task block, because `taskBlock` uses a nonce.

Example of the implement prompt's middle part after the change:

```
Analysis:
The panic comes from a nil map in Store.Put.
Root cause: Store is constructed without make().
Recommendation: initialise the map in NewStore.
Affected files (from analysis):
- internal/store/store.go
Risks (from analysis):
- callers that build Store{} directly

Plan:
Initialise the map in NewStore and guard Put.
Steps:
- add make() in NewStore
- add a test for Put on a fresh store
Affected files (from plan):
- internal/store/store.go
- internal/store/store_test.go
Risks (from plan):
- Store{} literals in tests
Acceptance criteria:
- Put on a store from NewStore does not panic
- a test covers Put on a fresh store
```

### 4.2 `acceptance_criteria` in the plan

The plan's JSON shape gains `"acceptance_criteria": ["..."]`. The prompt says what a
criterion is: one observable, checkable statement about the result, such as "function X
returns ErrEmpty for an empty input" or "`go test ./internal/store` passes". It also says to
give at most 8 criteria and to write none rather than vague ones. `domain.PlanArtifact` gains
`AcceptanceCriteria []string` (`json:"acceptance_criteria,omitempty"`). The parser stays
lenient: a missing field is an empty list. Go keeps the first 8 entries, trims each to 300
bytes on a UTF-8 boundary and drops empty strings. This is deterministic, so a resume reads
the same list back from the artifact. The artifact is decoded strictly on resume
(`decodeStrict`), so the new field must exist in the struct before any run writes it.

### 4.3 Diff stat on a retry

When `implement` runs again (after a failed test or review), its prompt gains:

```
What the run has changed so far (git diff --stat against the run's start):
<<<diffstat-3f9a…
 internal/store/store.go      | 4 +++-
 internal/store/store_test.go | 12 ++++++++++++
 2 files changed, 15 insertions(+), 1 deletion(-)
diffstat-3f9a…>>>
```

- It is computed from `Repository.DiffNumstat(pre_run_ref's tree, WorktreeTree())` and
  rendered in `git diff --stat` style by Go. No new git command runs.
- It goes through `fence` (repository-derived data) and is bounded by
  `limits.prompt.max_diff_bytes`. If the bound cuts it, the prompt keeps the first files and
  adds a `… N more files` line.
- Deny-listed files appear by name only, the same as in the review diff. A stat line carries
  no content anyway.
- It is computed when the prompt is built, not stored. On resume, the same tree gives the
  same stat.

### 4.4 Prompt golden files

Prompt tests today are assertions (`fence_test.go`, `bound_test.go`). QL01 adds golden
files for the six stage prompts in their main variants (first attempt, retry after test,
retry after review, final answer) under `internal/action/testdata/prompts/*.golden`. They are
updated with `go test ./internal/action -update`, the same pattern as `internal/jira`. Nonces
are fixed in the test through the existing seam (`var nonce` in `internal/action/fence.go`).

---

## 5. Deterministic checks (QL03) — TODO Q5

### 5.1 Configuration

```yaml
checks:
  - {name: build, command: "go build ./..."}
  - {name: vet,   command: "go vet ./..."}
  - {name: lint,  command: "golangci-lint run --new-from-rev=$ORCH_BASE_REF"}
  - {name: sast,  command: "semgrep --error --config p/golang"}
test:
  command: "go test ./..."
```

- `checks` is an optional top-level list. `name` matches `^[a-z0-9][a-z0-9_-]{0,31}$` and is
  unique. `command` is non-empty. There are at most 16 checks.
- A check command is trusted operator config like `test.command`, and it runs through
  `sh -c` (the documented exception, CLAUDE.md).
- A config with `checks` but no `test.command` is an error. Checks run inside the test
  step, so the step needs its command.

### 5.2 Where checks run

Checks are **not a new action type** (§30 forbids user-defined actions). The `test` step
runs every check in order, then `test.command`, and stops at the first non-zero exit code:

```
test step:  build → vet → lint → sast → test.command
                    ✗ vet exits 1 → step fails, failed_check: "vet", later commands not run
```

Each command:
- runs through the same `testexec.Runner`, under the same sandbox (bwrap unless
  `test.sandbox: none`), with the same `execution.TestEnv()` allowlist plus `test.env_pass`;
- gets `ORCH_BASE_REF=<pre_run_ref>` in its environment. A linter can use it to report only
  what the run introduced. The variable is set for `test.command` too, which costs nothing
  and keeps the environments the same;
- is guarded: the guard takes one snapshot before the first command and compares after
  **each** command, fingerprint first. A violation stops the step with the same error as
  today. The next command never runs on a tree with a planted hook.

The secret scan runs once after the step, as today. If a workflow has no `testing` stage,
the checks do not run. An operator who wants them always forces `testing`
(`policy.stages.force`).

### 5.3 Result

`domain.TestResult` gains:

```json
"checks": [
  {"name": "build", "exit_code": 0, "success": true,  "duration": 2100000000},
  {"name": "vet",   "exit_code": 1, "success": false, "duration": 900000000}
],
"failed_check": "vet"
```

- `checks` lists every command that ran, in order: name, exit code, success, duration. A
  check's stdout and stderr are not kept here. They go to
  `logs/<NNN>-test.<check>.{stdout,stderr}.log`.
- When a check fails, the top-level `command`, `exit_code`, `stdout` and `stderr` are the
  failing check's. `failed_check` names it. When every check passes, they are
  `test.command`'s, as today, and `failed_check` is absent.
- `success` is true only if every check and the test command exited 0.

### 5.4 What `implement` sees

On a retry after a failed check:

```
The previous implementation FAILED the check "vet" (one of the operator's checks, run before
the tests). Fix the implementation so the check passes.

Failing command: go vet ./...
Exit code: 1
<fenced, bounded stdout/stderr>
```

The tests did not run, and the prompt says so. It does not claim they pass.

### 5.5 Pre-run test

The pre-run test (MVP.md §41.5) runs the same sequence: checks, then `test.command`. A red
check before any change refuses the run like a red test, unless `run --allow-red-tests`. In
that case the first `implement` sees the failing check's output (`PreRunFailure`), as it
sees a failing test today. `pre_run_test` in `run.json` gains `failed_check`. `test.baseline:
off` skips the whole sequence.

---

## 6. Verify inside `implement` (QL04–QL06) — TODO Q2

### 6.1 What the model gets

With `implement.verify: true`, the implement session gets two extra tools from an MCP server
named `orch`, which the orchestrator itself provides:

| tool | arguments | what it does |
|---|---|---|
| `mcp__orch__run_tests` | `filter` (optional string) | runs `test.command` and returns its exit code and bounded output |
| `mcp__orch__run_check` | `name` (required, one of the configured check names) | runs that one check and returns its exit code and bounded output |

`run_check` is only offered when `checks` is non-empty. Its input schema lists the names as
an `enum`.

A call returns MCP `content` with one text block:

```
exit code: 1
command: go test ./...
filter: TestStore
duration: 3.2s
calls left in this step: 5
--- output (bounded) ---
<stdout/stderr, bounded by limits.prompt.max_test_output_bytes, same cut as BoundTestOutput>
```

A refused call (limit reached, guard violation, invalid filter) returns `isError: true` with
a one-line reason and runs nothing.

The prompt of `implement` gains one paragraph when verify is on: "You can run the project's
tests with the tool `mcp__orch__run_tests` (and its checks with `mcp__orch__run_check`).
Use them to check your change before you answer. The orchestrator runs the tests again
after you finish, and only that run counts."

### 6.2 The filter

`filter` must match `^[A-Za-z0-9_./:-]{1,200}$`. Anything else is refused: a `;`, a space,
`$(…)`, a quote, an empty string or 201 bytes. A valid filter is passed **only** as the
environment variable `ORCH_TEST_FILTER` of that one run. It is never substituted into the
command. Without a filter, the variable is unset. Using it is the operator's choice:

```yaml
test:
  command: 'go test ./... ${ORCH_TEST_FILTER:+-run "$ORCH_TEST_FILTER"}'
```

If `test.command` does not refer to `ORCH_TEST_FILTER`, a filter is harmless and the whole
suite runs. The `test` step itself never sets the variable.

### 6.3 Configuration

```yaml
actions:
  implement:
    provider: claude
    model: opus
    verify: true            # default false
limits:
  verify:
    max_calls_per_step: 8   # default 8, range 1..50
    call_timeout: 10m       # default 10m; also bounded by the implement step's timeout
```

Fail-fast rules (`config.Validate` and `ValidateDependencies`):
- `verify: true` requires `test.command`, and `test.sandbox` must not be `none`. The model
  triggers these runs, so the sandbox is mandatory. `verify: true` with `sandbox: none` is a
  config error.
- `verify` is accepted only on `actions.implement`. On any other action it is an unknown key.
- the server name `orch` is reserved: `extensions.mcp.orch` is a config error. Server names
  containing `__` are rejected too (`mcp__a__b__c` would be ambiguous), which tightens an
  existing check.
- the implement provider must be `claude`, the only adapter with MCP support. Codex and
  gemini refuse every authority anyway (D13).

### 6.4 How it is wired

```
implement step (Dispatcher)
 ├─ guard.Snapshot()                         ← the before-state (today's)
 ├─ dir := os.MkdirTemp("", "orch-verify-")  ← outside the repository, 0700
 ├─ write dir/verify.json                    ← everything the server needs (below)
 ├─ provider call with Extensions{
 │     MCPConfig: {"mcpServers":{"orch":{"command":"<abs path of this orchestrator>",
 │                 "args":["mcp-verify","--dir","<dir>"]}}},
 │     MCPTools:  [mcp__orch__run_tests, mcp__orch__run_check]}
 │      └─ claude starts `orchestrator mcp-verify --dir <dir>` (stdio)
 │           each call: validate → guard.Changed(before) → Runner.Run in bwrap → append dir/calls.jsonl
 ├─ guard.Changed(before)                    ← today's check after the call
 ├─ read dir/calls.jsonl, dir/violation      → executions[].verify_calls; violation → step fails
 ├─ move call logs into logs/, remove dir
 └─ secret scan, artifact (today's)
```

`verify.json` holds, in a versioned struct (`"version": 1`):

- repository root;
- `test.command`, the checks (name and command), and `pre_run_ref`;
- the sandbox description (`testexec.Sandbox`: home, repo root, git dirs, ro/rw binds);
- the test environment as computed `KEY=value` pairs (the result of `execution.TestEnv()`).
  The server does not rebuild the allowlist from a config file;
- the guard's protected patterns and the before-state (`GuardState` JSON);
- the limits and the bound on output bytes.

The server does not read the orchestrator's config file, does not open the run directory and
makes no network call. Its only inputs are `verify.json` and its stdin. Its only outputs are
its stdout (MCP), `calls.jsonl`, `violation` and per-call log files, all inside `dir`.

`mcp-verify` is a hidden subcommand. It is not in the usage text and refuses to start without
`--dir`, or when `dir/verify.json` is missing, malformed or of an unknown version. It is
started only by claude, through the MCP config the orchestrator wrote.

The extension is built by the dispatcher, not by `extensionsFor`. It is added to the
operator's own `extensions` (if any are offered to `implement`), and both go into the one
`--mcp-config` document.

### 6.5 The MCP protocol subset

The server is new package `internal/verify`, standard library only (plus `domain`,
`testexec`, `execution`, `repository`, and `action`'s guard, or the guard moved into a small
shared package if the import would cycle). It speaks JSON-RPC 2.0 over stdio with
newline-delimited messages and implements:

- `initialize`, which answers with `protocolVersion` (the client's, if it is one the server
  lists, else the newest the server knows), `capabilities: {tools: {}}` and `serverInfo`;
- `notifications/initialized` (ignored);
- `tools/list`, which returns the tools in §6.1 with their JSON Schemas;
- `tools/call`, which runs the tool;
- `ping`.

Anything else gets JSON-RPC error `-32601`. A malformed message gets `-32700` or `-32600`.
Messages are read with a 1 MiB line limit. The server handles one call at a time, and calls
are serialised. The server exits on EOF.

### 6.6 Guard before each run

Before every `run_tests` and `run_check`, the server calls `Changed(before)` with the
implement step's before-state, fingerprint first. If anything changed (a hook, git config, a
protected path such as CI files or the runs directory), the server:
1. runs nothing;
2. writes `dir/violation` with the changed paths;
3. returns `isError: true` with "refused: protected files changed: <paths>";
4. refuses every later call in this step.

After the call, the dispatcher's own `Changed(before)` catches the same change and fails the
step, as today. `violation` is a second, independent signal: if it exists, the step fails
even if the tree was restored in the meantime. Without it, a model could plant a hook, have
it run under `run_tests`, then restore the file.

Model-written code runs only inside bwrap, with git dirs read-only, no network and a tmpfs
`$HOME`. A test cannot plant a hook or reach a credential, and the guard check after each run
confirms it.

### 6.7 Limits

- `limits.verify.max_calls_per_step` counts both tools. Once it is reached, calls are refused
  with "limit reached".
- `limits.verify.call_timeout` bounds one run. The server kills the process group when it is
  reached and returns exit code -1 with "timed out".
- All of this happens inside the implement call, so the implement step's own timeout
  (`execution.timeout`, `actions.implement.timeout`) bounds the total. That timeout should
  be raised when verify is on, and the example config says so.

### 6.8 Record

`executions[]` of an implement step gains:

```json
"verify_calls": [
  {"tool": "run_tests", "filter": "TestStore", "exit_code": 1, "success": false,
   "duration": 3200000000, "output_cut": true},
  {"tool": "run_check", "check": "vet", "exit_code": 0, "success": true, "duration": 800000000},
  {"tool": "run_tests", "refused": "limit reached"}
]
```

Each call's full stdout and stderr go to `logs/<NNN>-implement.verify-<K>.{stdout,stderr}.log`.
`summary.md` shows the count of calls and how many passed. `outcome` gains
`verify_calls` (the total over the run).

### 6.9 Why this is not a shell (D16)

D6 says `implement` has no shell and that no Bash allowlist is a boundary. This feature keeps
both:
- the command is `test.command` or a configured check: trusted operator config, chosen in Go;
- the model chooses only *whether* to run it, *which configured check* (from a closed enum),
  and a filter string that cannot contain shell syntax and never reaches a command line;
- the run happens in bwrap with no network and no credentials, after a guard check;
- the tool set of authority `edit` does not change. The adapter's `--tools` stays
  `Read,Grep,Glob,Edit,Write` and only the two MCP tool names are added to
  `--allowed-tools`.

No call gets both repository access and a way out: the run has the repository but no network
and no credentials. Running model-written code is already what the `test` step does. Verify
moves the same execution, with the same isolation, into the session. `specs/permissions/` gets
decision D16 recording this, and MVP.md §38.1 lists the verify tools under `edit` as an
opt-in.

### 6.10 Live checks (QL06)

In `test/security`, with the real CLI:
- **V-verify-1:** implement with verify on, prompted to run `run_tests`, then to run
  `touch /tmp/<marker>` by any means, then to call `run_tests` with filter `x; touch
  /tmp/<marker2>`. Expected: `run_tests` ran (sentinel from the fake test command present),
  neither marker exists, and the second call is refused.
- **V-verify-2:** a model told to write `.git/hooks/pre-commit` and then call `run_tests`.
  Expected: the hook write fails (protected) or, if it lands, `run_tests` is refused and
  `violation` exists.
- **V-verify-3:** the model cannot write into the verify directory (`Write` to
  `<dir>/calls.jsonl` is refused), and `calls.jsonl` holds only the server's records.
- **V-verify-4:** with verify on, the argv still has `--tools Read,Grep,Glob,Edit,Write`, and
  `Bash` is not callable (positive control: `run_tests` works in the same session).

---

## 7. Review (QL02, QL11, QL12) — TODO Q6

### 7.1 A verdict per acceptance criterion (QL02)

When the plan has `acceptance_criteria`, the review prompt lists them numbered and asks for:

```json
"criteria": [
  {"index": 1, "met": true,  "evidence": "internal/store/store.go:24"},
  {"index": 2, "met": false, "evidence": "no test calls Put on a fresh store"}
]
```

- `domain.ReviewResult` gains `Criteria []CriterionVerdict{Index int, Criterion string, Met
  bool, Evidence string}`. Go fills `Criterion` from the plan after parsing. The model echoes
  only the index.
- Strict validation (`parseReviewStrict`): when the plan had N > 0 criteria, the answer has
  exactly N verdicts with indexes 1…N, each exactly once. `evidence` is non-empty and at
  most 500 bytes. Anything else is a malformed review, which is a hard step error (as today).
  When the plan had no criteria, or there was no plan step, `criteria` must be absent or
  empty.
- `Decide()`: an unmet criterion counts as a `high` issue. The review fails and goes back to
  `implement`, and the retry prompt lists the unmet criteria with their evidence next to the
  review issues.
- The persisted review result keeps `criteria`, so `outcomeOf` and resume see them.

### 7.2 Read the surroundings (QL02)

The review prompt says: "You have Read, Grep and Glob. Read the code around each changed
function (callers, the interface it implements, the tests that cover it), not only the diff.
An issue about code outside the diff is valid if the change causes it." This is a prompt
change only.

### 7.3 `review.fix_medium` (QL11)

```yaml
review:
  fix_medium: true     # default false
```

- A review that **passes** (no critical or high issue and every criterion met) but reports
  at least one `medium` issue sends the run back to `implement` once, with the medium issues
  in the retry prompt ("A code review passed but noted issues worth fixing").
- It counts as a fix iteration. It happens only if `fix_iterations < max_fix_iterations`;
  otherwise the run completes as today, with the issues as warnings. It happens at most once
  per run. Later reviews' medium issues are warnings.
- After that implement, the workflow continues as usual (test, then review). The second review
  decides as today.
- Workflow mechanics: `outcomeOf` gains a third outcome, `OutcomeFixable`, for a review that
  passes with medium issues while the option is on and not yet used. `Next(review,
  OutcomeFixable)` goes to implement if the workflow has it, otherwise done.
  `isBackwardEntryToImplement` and `Counters` count it as a fix iteration. All inputs come
  from `domain` and the persisted run, so `internal/workflow` imports nothing new.
- The option is recorded on the run at start (`run.json: options.review_fix_medium`) and read
  from there, never from the current config. A resume with a changed config follows the
  original run's rule.

### 7.4 Repeated issue → stop (part of QL08)

See §8.1. A review issue that the previous review in the same run already reported (same
`file`, similar `description`) and that the implement in between did not fix stops the run
as "no progress".

### 7.5 Extra reviewers (QL12)

```yaml
actions:
  review:
    provider: claude
    model: sonnet
    models: [opus]       # extra reviewers, in order; default []
```

- The review step calls the base model, then each model in `models`, **sequentially** and
  with the same prompt. They all use the same provider and the same authority (`read`).
  Different providers are not allowed: `models` is a list of model names, and the provider is
  the action's.
- Each answer is parsed and validated strictly on its own. If any one is malformed, the step
  fails.
- Go merges them. Issues are the union, de-duplicated by (severity, file, normalised
  description). A criterion is met only if every reviewer says met; the evidence comes from
  the first reviewer that says not met. `Decide()` runs on the merged result.
- The review artifact keeps every reviewer's answer
  (`reviewers: [{model, result}]`) and the merged result. `executions[].review_result` is the
  merged one.
- Usage is summed over the calls. `call` records the first call, and `call.extra_calls`
  holds one compact entry per extra reviewer (model, API time, tokens).
- This is not a parallel or multi-agent workflow (§30). It is one step, with calls in a fixed
  order and a merge that Go decides.

---

## 8. The fix loop (QL07, QL08, QL09) — TODO Q7, Q6.4

### 8.1 Stop when nothing changes (QL08)

After each step that can send the run back to implement, the engine checks for progress. It
compares the current failure with the previous failure of the same kind in this run:

| signal | compared with | means |
|---|---|---|
| implement's resulting tree (`tree_after`, a git tree id) | the tree after the previous implement | implement changed nothing |
| failing test: `failed_check` + exit code + failing test names (§8.2) if parsed, else a hash of the bounded output with digits replaced by `#` (masks durations) | the previous failed test step | same failure |
| review: every critical/high issue (and unmet criterion) matches one in the previous failed review | the previous failed review | review repeats itself |

"Matches" for review issues means: the same `file`, and descriptions whose normalised word
sets (lower case, letters and digits only, words of 3+ characters) have a Jaccard similarity
≥ 0.6. The comparison is deterministic and makes no LLM call.

When a signal fires, the run stops: status `stopped`, failure reason
`no progress: implement changed nothing` / `no progress: the same tests fail` /
`no progress: review repeats the same issues`, exit code 4. No more iterations are spent.

Mechanics: `StepExecution` gains `tree_after` for implement steps. The action package
computes it (it already computes `TreeAfter` for the diff). The comparison itself lives in
`internal/workflow` (`progress.go`) and works on persisted `StepExecution` data only, so the
same function serves a live run and `Entry`. On resume, a run that was stopped for no
progress resumes into implement, because the operator choosing to resume means "try again"
(§15 D4), and `max_fix_iterations` still applies.

`limits.stop_without_progress: true` (default) turns it on. `false` restores today's
behavior.

### 8.2 Failing test names (QL07)

New package `internal/testreport` (standard library + `domain`) extracts failing test names
from a test step's stdout and stderr for three common formats:

| format | recognised by | names from |
|---|---|---|
| `go test -json` | JSON lines with `"Action"` | `"Action":"fail"` events with a `"Test"` field, as `pkg.TestName` |
| `go test` (text) | lines `--- FAIL: TestX (…)` | those lines, with the package from the following `FAIL\t<pkg>` line |
| pytest | the `short test summary info` section, `FAILED path::name` | those lines |
| jest | `--json` result object, or `✕` / `● Suite › name` lines | `assertionResults[].status == "failed"` |

- The result is `{format, failed: []string}` with names de-duplicated and sorted, at most 200.
  An unrecognised format gives `format: ""` and no names, and nothing else changes.
- It never decides pass/fail. Success is still the exit code.
- `TestResult` gains `failed_tests` (when parsed) and `report_format`.
- The implement retry prompt adds `Failing tests: a, b, c` above the fenced output, so the
  names survive even when the output is cut.

### 8.3 Failures that were there before the run (QL09)

When the run started red (`run --allow-red-tests`) and both the pre-run test and the current
failed test have parsed names, the retry prompt splits them:

```
Failing before this run started (the task may be to fix them): TestA, TestB
New failures since the run started: TestC
```

This is a hint only. The step's success is the exit code, unchanged (§3.1, MVP.md §31.4), and
a red suite still fails the step even if every failure is "old". `run.json`'s
`pre_run_test` gains `failed_tests`, and each test result's `failed_tests` already carries
the rest, so `summary.md` can show the split. No new trust is put in parsed output.

---

## 9. Complexity chooses the model (QL10) — TODO Q3

### 9.1 Configuration

```yaml
actions:
  implement:
    provider: claude
    model: sonnet
    effort: medium
    by_complexity:
      simple:  {model: haiku, effort: low}
      complex: {model: opus,  effort: high}
```

- `by_complexity` is optional on every action that has a provider (research, analyze, plan,
  implement, review). Its keys are `simple`, `medium` and `complex`. Each value has `model`
  and/or `effort`; a missing field falls back to the action's base value. There is no
  `provider` key, and the strict parser rejects one. The provider is fixed per action, so the
  fail-fast dependency and authority checks still cover every call.
- `effort` follows the existing rules (`EffortLevels`, and only providers that accept it).

### 9.2 Selection

Go picks once per step: if the run's decision has a complexity (`decision.complexity`, from
the policy) and the table has an entry for it, that entry; otherwise the base. A manual
decision (`run --stage`) has no complexity and gets the base. The same decision always gives
the same models, which keeps routing static (§31.1 is reworded: "a static table in config,
keyed by the decided complexity, is static routing").

Extra reviewers (§7.5) are not affected by `by_complexity`.

### 9.3 Record

- `executions[].provider` (`domain.ProviderRef`) gains `model_source`: `base` or
  `by_complexity.<level>`.
- `decision.json` gains `models`: for each action in the workflow, the model, effort and
  source the decision implies, written when the decision is made. This shows the route before
  any step runs.
- Resume reads the persisted decision and computes the same route. A config whose table
  changed since the run started gives a different route. That is the operator's config
  change. The resume compares each remaining action's route with `decision.json: models` and
  prints a warning for each difference ("implement model changed since the run started:
  opus → sonnet"). Nothing warns about model changes today; QL10 adds the warning for every
  model change, not only for `by_complexity`.

---

## 10. Resume

- **Q1:** retry context (diff stat) is computed from the tree when the prompt is built, and
  `acceptance_criteria` comes from the plan artifact. Nothing new is needed.
- **Q5:** the persisted `test_result.failed_check` and `checks` feed `applyTest` as today.
- **Q2:** a verify directory never survives a step. An implement interrupted mid-call is
  retried as a whole (today's rule), and the leftover temp directory is removed when the next
  run or resume starts (`orch-verify-*` older than the process, owned by the user).
- **Q6:** the `options` recorded at run start drive `fix_medium`. Review results carry
  `criteria`, and `outcomeOf` reads them.
- **Q7:** `tree_after`, `failed_tests` and the review issues are persisted, and progress is
  computed from them (§8.1).
- **Q3:** see §9.3.

## 11. Configuration summary

```yaml
checks:                          # QL03
  - {name: vet, command: "go vet ./..."}
actions:
  implement:
    verify: false                # QL05
    by_complexity: {}            # QL10, any action
  review:
    models: []                   # QL12
review:
  fix_medium: false              # QL11
limits:
  stop_without_progress: true    # QL08
  verify:
    max_calls_per_step: 8        # QL05
    call_timeout: 10m
```

Every key is optional and new to the version-2 schema (strict parsing). There is no config
version bump. For an existing config, the behavior changes only in the parts that are on by
default: the richer prompts (§4) and the no-progress stop (§8.1).

## 12. Persistence

`run.json` additions, each validated strictly on load (`persistence/validate.go`):

| field | where | validation |
|---|---|---|
| `options` `{review_fix_medium}` | run | booleans |
| `checks[]`, `failed_check` | test results, `pre_run_test` | names valid per §5.1; `failed_check` names a `checks[]` entry with `success: false` |
| `failed_tests`, `report_format` | test results, `pre_run_test` | ≤ 200 names, each ≤ 300 bytes; known format |
| `verify_calls[]` | implement executions | `tool` ∈ {run_tests, run_check}; `check` set iff run_check; filter matches §6.2; ≤ 50 entries |
| `tree_after` | implement executions | 40 or 64 hex chars |
| `provider.model_source` | executions | `base` or `by_complexity.{simple,medium,complex}` |
| `review_result.criteria[]` | review executions | indexes 1…N unique; evidence ≤ 500 bytes |
| `call.extra_calls[]` | review executions | ≤ 4 entries |
| `outcome.verify_calls` | outcome | ≥ 0 |

`decision.json` gains `models` (§9.3). Artifacts: the plan gains `acceptance_criteria`, and
the review artifact gains `reviewers` (§7.5). Logs gain per-check logs (§5.3) and per-verify-
call logs (§6.8).

## 13. Errors and exit codes

| situation | error | exit |
|---|---|---|
| invalid `checks`, `verify` without sandbox, reserved/ambiguous MCP server name, invalid `by_complexity`, `models` on a non-review action | config validation | 2 |
| a check fails in the pre-run test | `WorkflowError` (as a red test) | 4 |
| verify `violation` marker, or the guard after implement | `WorkflowError` | 4 |
| malformed review (criteria count, index, evidence), from any reviewer | `WorkflowError` | 4 |
| no progress | `LimitExceededError`-like stop, status `stopped` | 4 |
| `mcp-verify` started by hand without a valid `--dir` | usage error | 2 |

## 14. Answered questions (2026-10-08)

| # | question | answer |
|---|---|---|
| Q1 | stage context: which parts | **all**: files and risks to plan/implement, `acceptance_criteria` in the plan, criteria and risks to review, retry diff |
| Q2 | what a retry learns about the previous attempt | **diff stat** against `pre_run_ref` (the model can Read the files) |
| Q3 | verify inside implement | **yes**, built-in MCP server, sandbox mandatory |
| Q4 | test filter | **yes**, regex-checked, only as `ORCH_TEST_FILTER` |
| Q5 | MCP implementation | **own, standard library** (initialize, tools/list, tools/call) |
| Q6 | when Q2 is done | **integration + live security tests green**; the benchmark comparison is its own task (QL15) |
| Q7 | complexity → model | **yes, opt-in** (`by_complexity`), compared in the benchmark (QL15) |
| Q8 | worktree isolation (TODO Q4) | **own spec later**, with W1 |
| Q9 | checks | **yes**, inside the test step |
| Q10 | checks in the pre-run test | **yes**, like the tests; `--allow-red-tests` allows a red start |
| Q11 | `run_check` in verify | **yes**, closed list of check names |
| Q12 | review | **all of TODO Q6**: criteria, read the surroundings, `fix_medium`, repeated-issue stop, extra reviewers |
| Q13 | fix loop | **all of TODO Q7**: no-progress stop, test-name parsers, baseline names |
| Q14 | baseline names: gate or hint | **hint only**; pass/fail stays the exit code |
| Q15 | the cost of the decision rounds (TODO Q8) | **not now**, after B4 and G5 |

## 15. Defaults chosen by the author

| # | default | why |
|---|---|---|
| D1 | The verify server takes everything from `verify.json` in a temp dir outside the repository, not from the config file | One source the orchestrator computed after the fail-fast chain; the model cannot write outside the repository (checked live, V-verify-3); no second config load that could differ |
| D2 | A `violation` marker fails the step even if the tree looks clean afterwards | A hook could have run under `run_tests` and been removed; the marker is the evidence |
| D3 | Guard after **each** check, not once around the test step | With `test.sandbox: none` a check could plant a hook that the next check's git call would run |
| D4 | A run stopped for no progress can be resumed, into implement | Resuming is the operator's explicit "try again"; `max_fix_iterations` still bounds it |
| D5 | Review issue similarity: same file + Jaccard ≥ 0.6 over normalised words | Deterministic, cheap, no model; the threshold is in code with a table test, not config |
| D6 | `fix_medium` happens at most once per run and spends a fix iteration | Bounded cost; it is still a retry |
| D7 | Extra reviewers use the action's provider only | Keeps the fail-fast dependency/authority checks per action unchanged |
| D8 | `acceptance_criteria` capped at 8 × 300 bytes, cut in Go | Bounded prompt and a bounded number of review verdicts |
| D9 | Checks need `test.command` | Checks run inside the test step |
| D10 | `ORCH_BASE_REF` is set for every check and for `test.command` | Same environment for every command of the step |
| D11 | The no-progress stop is on by default | It only saves money; `limits.stop_without_progress: false` turns it off |
| D12 | The verify paragraph is added to the implement prompt only when verify is on | No tool is mentioned that the session cannot call |

## 16. Not covered

- **Worktree isolation** (TODO Q4): its own spec (`specs/worktree/`) together with W1. Notes
  for it: a run starts from HEAD and does not see uncommitted work. A deterministic
  `git merge-tree` check at the end can warn that the run's branch conflicts with the
  operator's current branch. No LLM ever resolves a conflict.
- **The cost of the decision rounds** (TODO Q8): after the benchmark decision (B4) and
  calibration (G5). Any change to `questions.go` means recalibrating.
- **Baseline names as a gate** (letting a run pass while "only old" tests fail): rejected; it
  would make success depend on parsed text (§31.4).
- **Parallel verify calls, a shell, or any tool beyond `run_tests` and `run_check`.**
- **Verify for codex/gemini**: no adapter is verified (D13).
- **Model choice by anything other than the decided complexity** (cost so far, failures).

## 17. Tests

- **Unit:**
  - prompt golden files (§4.4);
  - `acceptance_criteria` capping;
  - diff-stat rendering and bounding;
  - checks validation and sequencing, with a fake runner: order, stop at first failure,
    guard after each;
  - `verify` JSON-RPC: a table of requests → responses, malformed input, unknown method,
    1 MiB line, EOF; tool calls with fake runner and guard: limit, timeout, filter regex
    (fuzz: only `^[A-Za-z0-9_./:-]{1,200}$` is accepted), violation marker, refusal after a
    violation;
  - `verify.json` round trip and version check;
  - review criteria validation (count, indexes, duplicates, evidence size) and `Decide` with
    unmet criteria;
  - merge of reviewers (dedupe, criteria AND);
  - `outcomeOf` with `OutcomeFixable`, `Next`, `Counters`, and `Entry` for fix_medium;
  - the progress table (`progress.go`) for each signal;
  - `testreport` with golden inputs per format, including truncated and interleaved output
    and an unknown format;
  - `by_complexity` validation and selection (`--stage` → base; missing level → base).
- **Integration** (fake CLI, scenarios 33+; QL13):
  - plan's files, risks and criteria visible in implement's and review's prompts;
  - retry prompt has the diff stat;
  - a check fails (vet), implement fixes it, and everything is green;
  - a red check before the run is refused, and `--allow-red-tests` lets it start;
  - verify: the fake claude calls the MCP server over stdio (the fake speaks the three
    methods), `verify_calls` is recorded, and a hook planted by the fake before a call makes
    the step fail;
  - an unmet criterion → implement → met;
  - `fix_medium` once;
  - two reviewers merged;
  - no-progress stops (same tree; same failing tests; repeated review issue);
  - baseline-name hint in the prompt;
  - `by_complexity` picks haiku for a simple decision and the base for `--stage`.

  The fake CLI gains per-call implement responses (`implement_<N>.json`, like review), and
  an MCP client mode.
- **Live** (`-tags security`): V-verify-1…4 (§6.10). They run on the installed CLI, and the
  versions they passed on are recorded in `specs/permissions/DECISIONS.md` D16.

## 18. Documentation changes (QL14)

- **MVP.md:**
  - a new §44 "Quality loop" (stage context, checks, verify, review, fix loop, complexity
    routing; the field reference for the new run.json fields);
  - §31.1 (a `by_complexity` table is static routing);
  - §38.1 (verify tools under `edit`, opt-in, sandbox mandatory);
  - §18–§19 (checks in the test step; review criteria and extra reviewers);
  - §12 (no-progress stop);
  - §37 (what resume reads: options, tree_after);
  - §41.5 (checks in the pre-run test);
  - §43 (`summary.md` fields).
- **CLAUDE.md:** the invariants (verify is not a shell, guard before every verify call,
  `violation` marker; checks are exit codes; parsed test names never decide); the run
  artifacts list; a pointer to this spec.
- **Other files:** `README.md`, `orchestrator.example.yaml` (every new key, with the implement
  timeout note), `specs/permissions/DECISIONS.md` D16, and the TODO.md statuses.
