# QL08 — Stop without progress

**Size** M (2–3 d) · **TODO** Q7.1, Q6.4 · **Depends on** QL07 · **Blocks** QL13

## Goal

The run stops with a clear reason, instead of spending its remaining fix iterations, when
`implement` changed nothing, when the same tests fail the same way, or when review repeats
the same issues (`SPEC.md` §8.1).

## Files

- `internal/domain/run.go`: `StepExecution.TreeAfter`; `internal/workflow/engine.go`:
  `StepOutcome.TreeAfter` (persist what the action already computes)
- `internal/action/implement.go`, `diffstat.go`: set `TreeAfter`
- `internal/workflow/progress.go` (new): `NoProgress(run *domain.Run) (reason string)` over
  persisted executions only
- `internal/workflow/engine.go`: call it after a step whose next state is a backward entry to
  implement
- `internal/workflow/resume.go`: `Entry` for a run stopped for no progress
- `internal/config/config.go`, `validator.go`: `limits.stop_without_progress` (default true);
  `internal/domain/workflow.go`: `LimitsView.StopWithoutProgress`
- `internal/persistence/validate.go`: `tree_after`

## Steps

1. Persist `tree_after` for implement steps (40 or 64 hex chars).
2. `progress.go`, all in terms of `domain` types:
   - **tree:** the last two implement executions have equal `TreeAfter`, with no test or
     review between them that passed → "no progress: implement changed nothing". Only
     compare after the second implement of the run.
   - **tests:** the last two failed test executions have the same failure key: `FailedCheck`
     + `ExitCode` + `FailedTests` if both have them, else a SHA-256 of the bounded output with
     digit runs replaced by `#` → "no progress: the same tests fail".
   - **review:** every blocking issue (critical/high, and every unmet criterion) of the last
     failed review matches one of the previous failed review's (same file, Jaccard ≥ 0.6 over
     normalised words, `SPEC.md` §8.1) → "no progress: review repeats the same issues".
3. Engine: after a test or review step that would send the run back to implement, if
   `StopWithoutProgress` and `NoProgress(run) != ""`, stop: status `stopped`, that reason,
   and a `LimitExceededError` with the reason and how to turn the check off. The tree check
   runs after the implement step itself, so a test step is not spent on an unchanged tree.
4. Resume: a run stopped for no progress continues into implement (`SPEC.md` §15 D4), and the
   check is not applied to the entry. `max_fix_iterations` still applies.
5. The similarity function and the normalisation get a table test, with the 0.6 threshold as a
   named constant.

## Acceptance

- Table tests of `NoProgress` for each signal, including the near misses: same tests but a
  different check; similar text but a different file; one new issue among repeated ones means
  progress.
- Engine test with a fake executor: equal trees stop after the second implement, and the
  failure reason and status are persisted.
- `limits.stop_without_progress: false` restores today's behavior (the same fake run uses all
  iterations).
- Resume of a stopped run enters implement.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built (2026-10-10)

Done as planned, except:

- **Gates:** the change was written without being able to run anything, so the four gates
  (`scripts/check.sh`) are the operator's to run; this task stays open until they pass.
- **Which signal is read:** `NoProgress` looks at the last completed execution only. After an
  implement it reads the tree; after a test, the tests; after a review, the review. A hard-failed
  last step is not judged. The engine calls it right after a recorded implement (tree), and after
  a failed test or review that goes back to implement (tests, review).
- **Order against `max_fix_iterations`:** the fix iteration is counted first and the hard limit
  is read first, so a run that exhausted `max_fix_iterations` keeps the reason it always had
  and the progress check only ends a loop earlier. The counters are those of the history and a
  no-progress stop does not change them.
- **Stricter than the table:** a test or review that passed between the two failures being
  compared means the run moved, so the tests and review signals do not fire either (the tree
  signal already had that rule). A failure with no output and no names has no key and is never
  "the same". Two descriptions with no words are never "similar".
- **Matching:** blocking issues are matched against the previous failed review's blocking
  issues only (medium and low are ignored on both sides); an unmet criterion matches on its
  `criterion` text against the previous review's unmet criteria. Words are lower-cased runs of
  letters and digits of 3 or more characters; `normalizeWords` and `jaccard` are separate
  functions and `similarityThreshold = 0.6` is inclusive.
- **Resume:** `Entry` returns implement for a run whose status is `stopped` with a reason that
  starts with `no progress:`, and also after the resume was approved - the app has cleared the
  status and reason by then, so `Entry` reads `Resume.FromReason` of the resume records that no
  step has run after (the n-th resume is `Resumes[n-1]`, its steps carry `Attempt` n). `Entry`
  does not call `NoProgress`. Executions are compared across resumes: the implement a resume
  enters is always run, and its own result is judged like any other, so an implement that again
  changes nothing stops the run again. `app/resume.go` needed no change: it refuses only a
  completed run.
- **Integration tests:** the fake implement either fixes the fixture or never changes it, so
  every scenario that loops to `max_fix_iterations` would now stop earlier. The integration
  config writer therefore renders `limits.stop_without_progress: false` unless a scenario sets
  `stopWithoutProgress`. The scenarios that exercise the stop are QL13's.
- **Bench:** the bench configs and their golden files do not set the key, so benchmark runs
  use the default (on); QL15 decides whether a configuration should turn it off.
