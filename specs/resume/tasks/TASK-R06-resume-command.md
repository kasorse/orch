# R06 — `resume` command; `run` and `resume` share one tail

**Size** L (1.5 d) · **Depends on** R01–R04 · **Blocks** R07, R08

## Goal

`orchestrator resume <run-id>` runs the order of operations in `SPEC.md` §6.1, with the
resumability table in §6.2 and the config/mode rules in §10. The part after the decision is
one code path for both commands.

## Files

- `internal/app/app.go` — dispatch, `resumeTask`, the shared tail, lock in `runTask`,
  workflow persisted before the dirty refusal
- `internal/app/resume.go` (new) — resume-specific checks, if `app.go` gets too long
- `internal/interaction/review.go` — a default mode in the mode prompt

## Steps

1. **Shared tail.** Extract from `runTask`, starting where `run.Decision`/`run.Workflow`
   are set, into `executeWorkflow(ctx, deps, run, opts)`: authority line, approval and
   mode, restore point + `head_ref` + baseline (only when the run has none), sandbox,
   Dispatcher + both guards, engine, final answer. `runTask` calls it, and its behaviour is
   unchanged; scenarios 1–17 are the check.
2. **`IsDirty` ignores the runs directory.** `repo.IsDirty` is a plain
   `git status --porcelain`, so in a repository that does not ignore `.orchestrator/` a
   resume of a `pending` run would be refused by its own run directory (found in R04).
   Exclude `:(exclude,top).orchestrator` as `StatusSummary` and `SnapshotCommit` already
   do. For a second `run` in such a repository, this fixes the same false positive.
3. **`run` changes.** Take `store.Lock` right after `NewRun`, held until return. Set and save
   `run.Decision`/`run.Workflow` before the dirty-tree refusal (G10). The refusal still
   happens before any prompt.
4. **`resume` dispatch.** `case "resume"` with exactly one argument. The usage string gets
   `resume <run-id>`.
5. **`resumeTask`**, in this order:
   1. Fail-fast chain (`loadAndValidateConfig`).
   2. `repository.Open` (rev-parse only).
   3. `ResolveRunID` → `Lock` → `LoadRun` → `workflow.Verify` (→ `not_resumable`).
   4. `completed`: build a Dispatcher, `Restore`, print "already completed" and the final
      answer, and return nil.
   5. No executions (§6.2 `pending` rows): the dirty-tree rule as in `run`, then the shared
      tail from approval onward.
   6. Otherwise: `CheckBaseline` (fingerprint first) → HEAD equals `head_ref` (refusal is a
      `WorkflowError`, exit 4, showing both SHAs) → `Entry` + `CheckLimits` → Dispatcher +
      `Restore` → output (R07) → the shared tail with `resumed: true`.
   7. Before the engine starts: append a `Resume` record, set `Attempt` for new executions
      (the engine stamps `len(run.Resumes)`), clear `FailureReason`, set status `running`,
      and save.
   No translator, decision provider or project-context manager is constructed.
6. **Mode (§10).** `--mode` wins. Otherwise the mode prompt shows the persisted mode
   (unless it is `plan` or empty) as the default on an empty answer. A persisted `auto`
   still asks, and `--mode auto` is required for an unattended resume. The approval prompt
   is shown for every non-auto mode. In `plan` mode, resume prints the remaining workflow
   and returns without executing or changing the status.
7. **Timeout.** `execution.timeout` starts at the beginning of `resumeTask`'s engine
   phase. There is no decision phase to discount.

## As built

- `executeWorkflow(ctx, *session, tailOptions)` is the shared tail. `session` holds the
  Dispatcher, runner and guarded patterns (`newSession`, no provider call and no
  `git status`). `tailOptions` carries what differs between the two commands: the
  `--mode`, the default mode, `dirty`, the `Resume` record (nil for `run`) and when
  `execution.timeout` starts. The restore point and baseline are recorded when the run
  has **no executions**, not "when it has no baseline", as SPEC §9.1 says. A run that
  crashed after its baseline but before its first step gets a fresh one.
- Until the operator approves, a resume changes nothing on disk. A cancel, a prompt
  error, a plan-mode answer or any refusal leaves `run.json` byte-identical. The
  `Resume` record, `status: running` and the cleared `failure_reason` are written right
  after approval, before the restore point and baseline of a `pending` run, so every
  later failure is attributed to that resume.
- `workflow.Entry` (pure) runs before the baseline check, so a run whose history already
  finished (`StateDone`) is marked `completed` and reported like a completed run.
- `pre_run_ref` is checked (`Repository.CommitExists`: a full lowercase SHA, then
  `git cat-file -e <sha>^{commit}`) only after the fingerprint, protected paths and HEAD
  checks pass. Before that, only `rev-parse` runs in a resumed repository. A missing
  commit is `corrupt`, as §6.3 says. A snapshot commit pruned by `git gc` therefore
  makes the run unresumable until pinning (deferred) exists.
- A run with executions and no `resume_baseline` → `not_resumable`. A malformed
  baseline → `corrupt`. Verify's "no workflow" → `not_resumable`, and a mismatched
  workflow → `corrupt`.
- HEAD refusal: `WorkflowError` naming both SHAs, with `git reset --soft <head_ref>` as the
  way back. `BaselineViolation` now adds that a moved or renamed repository is also
  reported as a `.git` change (R04 note).
- The mode prompt takes a default (`interaction.ReviewAndSelectMode(p, out, pinned,
  default)`). An empty answer without a default is still "unrecognized option".
- `run` saves `decision.json` and `project_context.json` before the dirty refusal too,
  not only `run.json`. `Decided` output stays after the refusal, so a refused run's
  console output is unchanged.
- Minimal `ConsoleReporter.ResumeHeader`, `NextStep` and `AlreadyCompleted`
  (`internal/logging/resume.go`). R07 adds `History`, `WorkingTree` and the golden files.
- Tests: `IsDirty` ignores the run directory; `CommitExists`; mode default;
  `scenario18_resume_command_test.go` (usage errors; dirty refusal keeps its workflow,
  resume refuses while still dirty, resumes by 8-char prefix after a commit, with no
  decision call; plan run → a plan-mode resume and a cancelled resume leave `run.json`
  unchanged → `resume --mode auto` executes → a second resume reports "already
  completed" and calls nothing). The with-history path, the fsmonitor refusal and the HEAD
  refusal were checked by hand with a throwaway scenario. R08 adds the permanent ones.

## Acceptance

- Scenarios 1–17 pass unchanged.
- `orchestrator resume` with no argument or two arguments → usage error, exit 2.
- The order in 5.6 is covered by R08 scenarios 5–7, and no provider call is made on any
  refusal path.
- A dirty-tree refusal now leaves a `run.json` with `workflow`, and it is resumable after
  a commit.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
