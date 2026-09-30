# Spec: Resume a run by ID

Status: **accepted**. The open questions are resolved (§16); implementation is planned in `tasks/` (see `README.md`).
Scope: a new `orchestrator resume <run-id>` command that continues a persisted run from where
it stopped, without deciding again or rebuilding the workflow. The decision flow, workflow
construction, the transition table, static routing, authorities and what is trusted (exit
codes + git diff) stay the same.
Supersedes the sketch in MVP.md §37, which predates the permission model (MVP.md §38) and
misses the gaps listed in §3. Once this is implemented, MVP.md §37 is rewritten from this
document and becomes the normative home, as with §38.

---

## 1. Problem

Every `orchestrator run "<task>"` starts from scratch: `store.NewRun` creates a fresh
`run.json` (`internal/app/app.go:212`), the decision runs again, and the engine always
starts at `Workflow.Steps[0]` (`internal/workflow/engine.go:76`). A run that failed (a
provider error, a timeout, a malformed review), was stopped (Ctrl-C, a manual-gate abort, a
limit) or crashed cannot continue. The operator has to start over, which:

- re-runs and pays for steps that already succeeded (analyze, plan, a long implement);
- can produce a **different workflow**, because the decision provider may answer
  differently the second time;
- loses the history: the retry context (test failure output, review issues) that the next
  `implement` needs.

Everything a resume needs is already on disk under `.orchestrator/runs/<id>/`. Only the
code to read it back is missing.

## 2. What exists

| piece | state |
|---|---|
| `run.json`: task, decision, workflow, mode, `pre_run_ref`, append-only `executions`, counters | written after every step (`engine.persist`) |
| `artifacts/NNN-<action>.json`: each step's structured output | written best-effort (`Dispatcher.saveArtifact`) |
| `logs/NNN-<action>.*.log` | indexed by execution index, so new steps never collide |
| `persistence.Store.LoadRun` | **missing** |
| engine entry at any state other than `Steps[0]` | **missing** |
| Dispatcher's in-memory context (`lastResearch` … `pendingReviewIssues`, `internal/action/context.go:77-87`) | **lost** when the process exits |

## 3. Gaps and findings

Items marked † are not in MVP.md §37 and come from the permissions work or from reading the
code.

| # | Finding | Where |
|---|---|---|
| G1 | No way to load a run: no `LoadRun`, no run-ID lookup, no validation of what is read back. | `internal/persistence/filesystem.go` |
| G2 | The engine always enters at `Steps[0]`. The resume state has to be derived from `Executions` plus the transition table. | `internal/workflow/engine.go:76` |
| G3 | The Dispatcher's prompt context (`lastAnalysis`, `lastPlan`, `lastImpl`, `lastTest`, `pendingTestFailure`, `pendingReviewIssues`) lives only in memory. Without it, a resumed `implement` would retry without the test failure or review issues it is meant to fix. | `internal/action/context.go:77-87` |
| G4 † | **The clean-worktree rule blocks every useful resume.** After `implement`, the tree is dirty by design. Applying the fresh-run rule (`app.go:262`) would refuse the run. | `internal/app/app.go:262` |
| G5 † | **A crash during `implement` or `test` skips the guard.** The guard compares protected paths after the call. If the process dies mid-call, the comparison never happens, and a resumed `test` would run whatever was written. The same applies to a run that the guard failed: the violating files stay on disk, and a new `Snapshot` taken on resume would treat them as the baseline. | `internal/action/implement.go:59`, `testaction.go` |
| G6 † | **Resume inputs are writable by the model.** `.orchestrator/runs/` sits inside the worktree, and `implement` (`edit`) and the test command (writable repo, even under bwrap) can write there. Within one process this does not matter, because state is in memory. On resume, a tampered `run.json` (task, workflow, `pre_run_ref`) or artifact (the prompt context) would be trusted. `ArtifactPath` is a string read from `run.json`, so trusting it also allows path traversal. | `persistence`, `action/*` |
| G7 † | **`git status` must not run first on resume.** A fresh run's first `git status` (`IsDirty`) runs on the operator's own repository. A resumed run's first `git status` runs on a tree a model last touched, and a planted `core.fsmonitor` would execute. The git fingerprint must be compared before any `git status`, as the guard already does (`guard.go` `Changed`). | `internal/app/app.go` (`IsDirty`) |
| G8 † | **No protection against concurrent use.** `status: running` can mean the process crashed or that it is still running in another terminal. MVP.md §37 treats `running` as crashed, which is wrong when two processes append to the same `Executions`. | — |
| G9 † | **`review` diffs against the current `HEAD`** (`git diff HEAD`, `repository/git.go:58`). If the operator commits the run's changes between the stop and the resume, a resumed review sees an empty or partial diff and passes vacuously. | `internal/repository/git.go:58` |
| G10 † | **Runs refused for a dirty tree are not resumable.** The dirty-tree refusal happens before `run.Workflow` is set (`app.go:262` vs `:271`), so the decided workflow is lost although decision.json has it. | `internal/app/app.go:262-271` |
| G11 † | **Plan-mode runs end in `pending`.** `--mode plan` returns without a status, so "preview now, execute later" is not covered by §37's `failed`/`stopped` list. | `internal/app/app.go:304` |
| G12 † | `FixIterations` is incremented after the step is persisted and before the next one runs (`engine.go:175`). A crash in between loses the increment. The counter has to be derived from the history, not trusted. | `internal/workflow/engine.go:175` |
| G13 † | The implement artifact is saved **before** the guard check (`implement.go:49` vs `:59`). This has to be reordered so the orchestrator writes nothing to the run directory during a guarded call (see §9.3). | `internal/action/implement.go:49` |

Related, out of scope: a `pre_run_ref` snapshot commit is not referenced by any ref, so
`git gc` can prune it after `gc.pruneExpire`. Resume does not depend on it, but the
"Restore point" message would name a missing commit. Pinning it
(`refs/orchestrator/runs/<id>`) is a separate change.

## 4. Principles

- **R1: Resume continues, never re-decides.** The persisted decision and workflow are reused
  as they are. Changing the task, decision or workflow means a new `run`.
- **R2: Same engine, different entry.** Resume is `Engine.Run` on a `Run` whose `Executions`
  is not empty. The entry state is a pure function of the history and the transition table
  (§7). A fresh run is the empty-history case of the same function.
- **R3: History is the truth, counters are derived.** `TotalSteps` and `FixIterations` are
  recomputed from `Executions` on load. `Executions` stays append-only: resume never edits
  or removes an entry.
- **R4: Every safety check a fresh run gets, plus the ones a stopped run needs.** The full
  fail-fast chain runs again with the current config. Authorities come from
  `domain.AuthorityFor` as always. The protected state is compared with the state at run
  start before anything else runs (§9).
- **R5: Persisted state is input, not trust.** `run.json` and artifacts are strictly
  validated. Paths are derived, never read from the file. The workflow must equal
  `workflow.Build(decision.Stages)`. Anything inconsistent refuses the resume. Nothing is
  repaired.
- **R6: One process per run.** A run directory is locked for the lifetime of the `run` or
  `resume` that owns it.

## 5. Non-goals

- Resuming a run with no workflow (the decision failed, or the run was cancelled before one
  was built). That is a new `run`.
- Editing the task, decision, workflow or mode history on resume.
- Migrating `run.json` from a binary without resume support. Such runs are refused (§6.3).
- A run browser (`orchestrator runs`, `show`). This could be added later, but it is not
  needed for resume.
- Resuming *inside* a step. A step interrupted mid-call starts again from the beginning;
  provider CLI sessions are not resumed.
- Parallel or multi-run execution (MVP.md §30).

## 6. CLI

```bash
orchestrator resume <run-id>            # full UUID or a unique prefix of ≥ 8 characters
orchestrator [--config p] [--mode m] [--verbose] resume <run-id>
```

`--config`, `--mode` and `--verbose` behave as they do for `run`. The current config is
used, not the one the run started with. See §10.

### 6.1 Order of operations

```
parse args
→ fail-fast chain (Load → Validate → ValidateDependencies → sandbox probe → ValidateAuthorities)
→ resolve run ID (exact, else unique prefix) under .orchestrator/runs/
→ lock the run directory (§11)
→ LoadRun + validate (§6.3)
→ resumability (§6.2)                            — completed: report, exit 0
→ protected-state check vs run-start baseline (§9.1)  — fingerprint first, then git status
→ HEAD check (§9.2)
→ derive entry state (§7) + limit pre-check (§8)
→ rehydrate Dispatcher context from artifacts (§7.3)
→ print: resume header, history, next step, remaining plan, authority, restore point
→ approval + mode (non-auto), as for run
→ append a Resume record, status running, persist
→ Engine.Run
```

Nothing on this path calls an AI provider or makes an HTTP request before the approval,
except what `run` also does. Resume makes no decision calls at all.

### 6.2 Resumability by persisted state

| status | workflow | executions | resume does |
|---|---|---|---|
| `completed` | — | — | prints "already completed", re-prints the final answer if there is one, exit 0, no provider call |
| any | none | — | refuses: "no workflow was decided; start a new run" (exit 1) |
| `pending` (plan mode, or crashed before approval) | yes | none | runs the tail of a fresh run: dirty-tree rule, restore point, baseline, approval, engine from `Steps[0]` (G11) |
| `stopped` "cancelled by user" at approval | yes | none | same as `pending` |
| `failed` dirty-tree refusal (after G10) | yes | none | same as `pending`, and the dirty-tree rule applies again |
| `failed` / `stopped` | yes | ≥ 1 | derive the entry state (§7) |
| `running`, lock free | yes | any | the previous process died. Handled like `failed` and reported as "interrupted" |
| `running`, lock held | — | — | refuses: "run is in use by another process" (exit 1) |

### 6.3 Loading and validation

`persistence.Store.LoadRun(id)` reads `run.json` and applies strict checks (R5). Any failure
is a `RunStateError{Kind: corrupt}`:

- unknown JSON fields are rejected; status, mode, actions and authorities must be known
  enum values;
- `run.ID` equals the directory name;
- `Executions[i].Index == i`, and statuses are `completed`/`failed` (a `running` entry is
  never persisted);
- `Workflow` equals `workflow.Build(Decision.Stages)`, which is deterministic, and every
  execution's action is a step of the workflow. This check lives in `workflow.Verify`,
  because `persistence` does not import `workflow`;
- `resume_baseline` (§9.1) is present. If it is missing, the run predates resume support
  and is refused with its own message;
- `pre_run_ref`, when set, resolves to a commit (`git cat-file -e <ref>^{commit}`).

## 7. Entry state

### 7.1 Derivation

New pure function in `internal/workflow` (imports only `domain`):

```go
// Entry returns the state the engine continues from, given the run's history.
// An empty history returns the workflow's first step, which is the fresh-run case.
func Entry(run *domain.Run) (state State, why string, err error)
```

Let `last` be the last entry of `Executions`:

| last execution | entry | why |
|---|---|---|
| none | `Steps[0]` | "start" |
| `failed` (hard error: provider, timeout, malformed review, guard) | `last.Action` | "retry <action> (failed: <error>)" |
| `completed`, outcome derived as below → `Next(...)` is a step | that step | "continue after <action>" / "retry implement after test failure" |
| `completed` → `Next` is `StateDone` | — | run is complete (a crash between persisting the step and persisting `completed`); marked completed, exit 0 |
| `completed` → `Next` is `StateFailed` ("tests failed" without implement) | `last.Action` | "re-run <action>" (see Q3) |

The outcome of a completed step is derived only from trusted, persisted results:
`test` → `TestResult.Success`; `review` → `ReviewResult.Decide()` (never the model's
`status`); every other action → success. `Next` itself does not change.

`Engine.Run` replaces `current := State(run.Workflow.Steps[0].Action)` with `Entry(run)`.
For a fresh run the result is identical.

### 7.2 Why a failed step is retried as is

A hard failure recorded no outcome, so the transition table has nothing to route. Retrying
the same action is what the operator would have done by starting over, and it is still
subject to the limits (§8). A step that failed through the guard is only retried after
§9.1 has shown that the protected state is back to its run-start value.

### 7.3 Rehydrating the prompt context

New `(*action.Dispatcher).Restore(run *domain.Run, r ArtifactReader) error`. It replays the
**state changes** of each *completed* execution in order, without calling anything:

| action | effect (same as the live code) |
|---|---|
| research / analyze / plan | set `lastResearch` / `lastAnalysis` / `lastPlan` |
| implement | set `lastImpl`, clear `pendingTestFailure` and `pendingReviewIssues` |
| test | set `lastTest`; `pendingTestFailure` = result if it failed, else nil |
| review | `pendingReviewIssues` = issues if `Decide()` fails, else nil |

Failed executions are skipped, which matches the live behaviour: a failed `implement` did not
consume the pending retry context, and its retry gets the same context.

- The artifact path is **derived** (`artifacts/%03d-%s.json` from index and action). The
  stored `ArtifactPath` is only compared with it (G6).
- The artifact is decoded strictly into its domain type. Review goes through
  `parseReviewStrict`'s `Validate`.
- A missing or undecodable artifact for a completed step refuses the resume
  (`RunStateError{corrupt}`), naming the file. Resuming with a silently thinner prompt is not
  an option. The test step has no artifact on disk when its guard failed, but that step is
  `failed` and so skipped.

This lives in `internal/action`, next to the code whose state it rebuilds, and a test keeps
the two in step (§14).

## 8. Limits and timeouts

- `max_steps` and `max_fix_iterations` are evaluated against the whole history, as in
  MVP.md §37 (see Q1). `TotalSteps = len(Executions)`. `FixIterations` is the number of
  completed `test`/`review` executions whose derived outcome is a failure, when the
  workflow contains `implement` (G12).
- **Pre-check before approval.** If the entry would immediately exceed a limit
  (`TotalSteps ≥ max_steps`, or entry is `implement` via a backward transition with
  `FixIterations > max_fix_iterations`), resume refuses before asking anything, with a
  `LimitExceededError` (exit 4). The message states the used and configured values and the
  config key to raise.
- `execution.timeout` is a fresh budget per invocation. It covers this process's steps, as
  it covers a fresh run's. `actions.<name>.timeout` applies per step as always.

## 9. Safety on resume

### 9.1 Run-start baseline (G5, G7)

At the point where a fresh run records `pre_run_ref` (after approval, before the first
step), it also records `resume_baseline` in `run.json`: the guard's state then (git
fingerprint, plus a content hash per protected path that differs from HEAD or is ignored).
If that snapshot fails, the run is refused before its first step: a run without a baseline could never be resumed safely. The restore point is different; failing to record it is only reported. `action.GuardState` gets JSON (un)marshalling. Its fields stay unexported, and only the guard
compares them.

On resume, before anything runs `git status`:

1. `GitFingerprint` (rev-parse and file reads only) is compared with the baseline's. A
   mismatch refuses the resume with the guard's existing `.git (config or hooks)` message,
   which already warns not to run git until checked by hand.
2. Only then is `RepoGuard.Changed(baseline)` compared. Any protected path that differs
   refuses the resume, naming the paths and the restore point (the `protectedViolation`
   message, headed "since the run started").

This covers a crash mid-`implement`/`test`, a run the guard failed whose files are still
there, and anything that changed protected paths between the two processes. Within a run,
no change to protected state is ever legitimate, because every such change stops the run.
So "equal to the run-start baseline" is the exact invariant. To resume after a guard
violation, the operator reverts the protected files. To keep the change, they start a new
run.

For runs with no executions yet (§6.2 `pending` rows), there is no baseline yet. The
fresh-run tail applies instead: dirty-tree rule, restore point, baseline.

### 9.2 Worktree and HEAD (G4, G9)

- The clean-worktree rule (`security.require_clean_worktree`) applies only to runs that have
  not executed a step. Once a step has run, the tree is dirty by design. What protects the
  resume then is the baseline (§9.1), not cleanliness.
- `run.json` records `head_ref` (HEAD at run start; with a clean tree that is `pre_run_ref`
  itself). If HEAD differs on resume, the resume is refused (see Q5), because `review` would
  otherwise diff against the wrong base.
- Non-protected files the operator changed between the stop and the resume are allowed
  (Q2). After the §9.1 check passes, the resume header shows the working tree
  (`Repository.StatusSummary`, as the manual gate does). Without a checkpoint per step it
  cannot tell the run's changes from the operator's, so it lists both. `review` reads the
  real diff anyway, so manual fixes are reviewed like the model's. A checkpoint per step
  (a snapshot commit after each successful step) would allow "changed since the stop"; it is
  deferred.

### 9.3 Resume inputs are protected paths (G6, G13)

- The guard's patterns around `implement` and `test` also cover the run directory's resume
  inputs: `run.json`, `workflow.json`, `decision.json` and `artifacts/**` of **every** run
  under the runs directory, rendered relative to the repository top-level. Logs are not
  covered: they are output, never read back. These patterns are added only to the per-step
  guard and not to the run-start baseline, since `run.json` changes with every step.
- For this to hold, the orchestrator must write nothing to the run directory during a
  guarded call. Implement's `saveArtifact` moves after `Guard.Changed` (G13). The engine's
  persist and the log writes already happen after `Execute` returns.
- Every workflow call's deny list (`provider.Request.DenyRead`) also gets the runs
  directory, as defense in depth. In claude a `Read(...)` deny rule also blocks Edit and
  Write on the same path (`internal/provider/claude/client.go:85`), so no new rule kind is
  needed. It also keeps other runs' prompts, context and logs away from the file tools. As
  with every authority change, the live suite (`go test -tags security`) verifies this
  before it is relied on.
- Residual risk, documented in MVP.md §38.5: if the orchestrator is SIGKILLed while a
  provider CLI runs, the CLI (its own process group) can keep writing after the last guard
  check. §9.1 catches protected paths, and §6.3 catches structural tampering, but a subtly
  edited artifact in that window could reach the next prompt. It is bounded by the step's
  authority. The history and next step are printed before approval (§12), so the operator
  can inspect them.

## 10. Config and mode on resume

- The current config applies to everything that runs from now on: routing, models,
  timeouts, limits, test command, sandbox. Each step already records `provider`/`model`/
  `authority` in its `StepExecution`, so a routing change is visible in `run.json`. The
  decision thresholds are irrelevant, because nothing is re-decided.
- Mode: `--mode` wins. Without it, a non-auto resume asks as `run` does, and the persisted
  mode (if it is not `plan`) is the default shown in the prompt. A persisted `auto` is never
  applied silently: an unattended resume needs `--mode auto`, since the operator may be
  somewhere else now. The approval prompt is shown again for every non-auto mode. In
  `manual`, the gate applies to each remaining step. Authority does not depend on mode.

## 11. Locking (G8)

`persistence.Store.Lock(runID) (unlock func(), error)` takes `flock(LOCK_EX|LOCK_NB)` on the
**run directory's own file descriptor**. It is held until the process exits, released by the
kernel on crash, and cannot be bypassed by deleting a lock file. `run` takes it right after
`NewRun`, and `resume` takes it before `LoadRun`. `EWOULDBLOCK` →
`RunStateError{in_use}`. Linux and macOS both support `flock` on directory descriptors.

## 12. Persistence and output changes

`domain.Run` additions (all `omitempty`, so older readers are not broken):

```go
HeadRef        string          `json:"head_ref,omitempty"`        // HEAD at run start (§9.2)
ResumeBaseline json.RawMessage `json:"resume_baseline,omitempty"` // guard state at run start (§9.1)
Resumes        []Resume        `json:"resumes,omitempty"`         // append-only
```

```go
type Resume struct {
	At         time.Time  `json:"at"`
	FromStatus RunStatus  `json:"from_status"` // "running" recorded as-is (= interrupted)
	FromReason string     `json:"from_reason,omitempty"`
	Entry      ActionType `json:"entry"`
	Why        string     `json:"why"`
	Mode       RunMode    `json:"mode"`
}
```

`StepExecution` gains `Attempt int json:"attempt,omitempty"`: 0 for the original process,
*n* for the *n*-th resume. `FailureReason` is cleared when a resume starts, and the previous
reason is kept in `Resumes[n].FromReason`.

Also reorder `runTask` so that `run.Decision`/`run.Workflow` are set and saved before the
dirty-tree refusal (G10). The trace and `decision.json` are unaffected.

Console output (new `ConsoleReporter` methods, golden-tested):

```
Resuming run 3f2a9c1e-…  (.orchestrator/runs/3f2a9c1e-…)
Previous attempt: failed — provider execution failed (claude, implement, timeout)
History:
  ✓ 000 analyze    claude/sonnet  read
  ✓ 001 plan       claude/sonnet  read
  ✗ 002 implement  claude/sonnet  edit   timeout after 10m
Working tree:                                          (StatusSummary, only when not clean)
   M internal/retry/retry.go
  ?? internal/retry/retry_404_test.go
Next: implement — retry implement (failed: timeout)
Remaining workflow: implement → test → review
authority: …                                          (existing line)
Restore point: <pre_run_ref>                           (existing line)
```

## 13. Errors and exit codes

New `apperr.RunStateError{Kind, RunID, Msg}`, following the `DecisionError` pattern:

| kind | when | exit |
|---|---|---|
| `not_found` | no directory matches; or a prefix < 8 characters | 1 |
| `ambiguous` | the prefix matches several runs (they are listed) | 1 |
| `in_use` | lock held | 1 |
| `not_resumable` | no workflow; run predates resume support | 1 |
| `corrupt` | validation (§6.3) or a missing/invalid artifact (§7.3) | 1 |

Protected-state and HEAD refusals (§9) are `WorkflowError` (exit 4), the same category as the
guard at run time. Limit pre-check failures are `LimitExceededError` (exit 4). Config and
dependency failures are exit 2, as for `run`. `completed` → exit 0.

## 14. Tests

Unit:
- `workflow.Entry`: a table over every row of §7.1, including review fail with and without
  implement, and a test failure with no implement.
- Derived counters (§8) against engine-produced histories. Property: running the engine to a
  stop, then `Entry` + `Engine.Run`, gives the same transitions as an uninterrupted run with
  the same fake outcomes.
- `Dispatcher.Restore`: for each action, a replayed history gives the same fields as live
  execution. This is a table shared with the live-path tests, so a future change to an
  action's state handling breaks the test when `Restore` is not updated.
- `LoadRun` validation: every §6.3 rule, plus unknown fields, traversal in `artifact_path`,
  and a workflow that does not match its decision.
- `GuardState` JSON round-trip. `Lock` contention (two locks in one test via separate fds).

Integration (`test/integration`, fakeexec; each is a `scenarioNN_resume_*_test.go`):
1. Provider fails at implement → resume → implement retried, run completes. The history is
   append-only, and the second implement's prompt carries the plan (asserted through
   `FAKE_ARGS_DIR`).
2. Test fails, `max_fix_iterations` reached → resume with the same config refuses with
   exit 4 before any call. After raising the limit, it continues at implement with the test
   failure in the prompt.
3. Plan-mode run → `resume --mode auto` executes it from `Steps[0]`, and records the restore
   point and baseline at that time.
4. Simulated crash: `status: running`, no lock → resume reports "interrupted" and continues.
5. Guard violation at implement → resume refuses while the file is there, and proceeds
   after it is reverted.
6. A planted change to `.git/config` between the runs → refused, and **no `git status`
   ran** (asserted from the recorded argv).
7. HEAD moved → refused.
8. Tampering: an edited workflow in `run.json`, a deleted artifact, and a traversal
   `artifact_path` → each `corrupt`, exit 1.
9. Implement (fake) writes `.orchestrator/runs/<id>/run.json` → the guard fails the step.
10. Completed run → exit 0, no provider call, answer re-printed.
11. Prefix resolution: unique prefix, ambiguous prefix, a prefix that is too short.

Live (`-tags security`): under `edit`, reading, editing and writing a file in the runs
directory are all refused.

## 15. Implementation plan and estimate

Sizes are focused engineering time for someone who knows the codebase, including tests. The
code change is about 700–900 lines of production code and 1,000–1,300 lines of tests.

| task | content | depends | size |
|---|---|---|---|
| R01 | Domain + persistence: `Run`/`Resume`/`Attempt` fields; `LoadRun`, run-ID/prefix resolution, strict validation (§6.3); `Store.Lock`; `RunStateError` + exit mapping | — | 1 d |
| R02 | `workflow.Entry`, derived counters, limit pre-check; `Engine.Run` enters via `Entry` | R01 | 0.5–1 d |
| R03 | `Dispatcher.Restore` + `ArtifactReader` (derived paths, strict decode); move implement's artifact write after the guard | R01 | 1 d |
| R04 | Safety: serializable `GuardState`, `resume_baseline` + `head_ref` recorded at run start; the resume-time check in fingerprint-first order; run-directory resume inputs added to the guard patterns | R01 | 1–1.5 d |
| R05 | runs directory added to every workflow call's deny list + live check | R01 | 0.5 d |
| R06 | `app`: `resume` command, `run` takes the lock, workflow persisted before the dirty refusal (G10), shared post-decision tail extracted from `runTask` so `run` and `resume` use one path; mode/approval rules (§10) | R01–R04 | 1.5 d |
| R07 | Console output (§12) + golden files | R06 | 0.5 d |
| R08 | Integration scenarios 1–11 | R06 | 1–1.5 d |
| R09 | Docs: rewrite MVP.md §37 from this spec, add §38.5 residual risk, update CLAUDE.md "Run artifacts" and the README usage | all | 0.5 d |

**Total: about 7.5–9 engineering days.** The critical path is R01 → R04 → R06 → R08. R02,
R03 and R05 can proceed in parallel after R01. The riskiest parts are R04 (ordering around
`git status` and the guard's pattern split) and R06 (extracting the shared tail of `runTask`
without changing `run`'s behaviour, which the existing scenarios 1–16 will check).

Without §9 and §11 (a "naive" resume: load, derive entry, rehydrate, go), the work would
take about 3–4 days. That version would let a resumed `test` run unguarded code after a
crash, and would trust model-writable state. That conflicts with MVP.md §38, so it is not
offered as an option.

## 16. Decisions

Resolved 2026-09-30. The recommended answer was taken in every case.

| # | question | decision | why |
|---|---|---|---|
| Q1 | Limits across resumes | **Cumulative** (MVP.md §37). A limit-stopped run resumes only after the config value is raised; the pre-check (§8) names the key and the values. | Limits guard against loops. A fresh budget per resume would reset the loop every time. |
| Q2 | Operator edits between the stop and the resume, outside protected paths | **Allowed**; the working tree is shown (§9.2). | "I fixed it by hand, now test and review" is a main use. Review sees the real diff either way. |
| Q3 | A run that failed through a transition (tests failed, no implement) | **Re-run that step** (§7.1). | The operator presumably fixed something. |
| Q4 | Plan-mode runs | **Resumable** from `Steps[0]` (§6.2), giving "decide now, execute later". | It costs one row. |
| Q5 | HEAD moved since the run started | **Refuse** (§9.2). | Review would diff against the wrong base. Diffing against `head_ref` changes review for fresh runs too, so it is a separate change. |
