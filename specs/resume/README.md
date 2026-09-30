# Resume a run by ID

Status: **in progress**. The spec is accepted and the decisions are taken (`SPEC.md` §16).
R01–R06 are done; R07–R09 remain.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification: gaps, principles, entry state, safety on resume, persistence, tests, estimate, decisions |
| [`tasks/`](tasks/) | 9 implementation tasks |

MVP.md §37 is the normative home once R09 rewrites it from `SPEC.md`. Until then this folder
supersedes the sketch in §37.

## The problem in one paragraph

A run that failed, was stopped or crashed cannot continue. The only option is a new `run`,
which pays again for finished steps, may decide a different workflow, and loses the retry
context. The fix is `orchestrator resume <run-id>`. It loads the persisted run, derives the
entry state from the history and the existing transition table, rebuilds the prompt context
from the artifacts, and runs the same engine from there. It never decides again. Before
anything runs on the tree a model last touched, the git fingerprint and the protected paths
are compared with their state at run start. The run directory is locked, and everything read
back is strictly validated, because the run directory is writable by the model.

## Tasks

| task | title | size | depends on |
|---|---|---|---|
| [R01](tasks/TASK-R01-load-and-lock.md) | run fields, `LoadRun`, run-ID resolution, lock, `RunStateError` ✓ | M | — |
| [R02](tasks/TASK-R02-entry-state.md) | `workflow.Entry`, derived counters, `Verify`, limit pre-check ✓ | S–M | R01 |
| [R03](tasks/TASK-R03-restore-context.md) | `Dispatcher.Restore` from artifacts; implement's artifact after the guard ✓ | M | R01 |
| [R04](tasks/TASK-R04-baseline-and-guard.md) | run-start baseline, `head_ref`, resume inputs under the guard ✓ | M–L | R01 |
| [R05](tasks/TASK-R05-deny-runs-dir.md) | runs directory in every workflow call's deny list + live check ✓ | S | R01 |
| [R06](tasks/TASK-R06-resume-command.md) | `resume` command; `run` and `resume` share one tail ✓ | L | R01–R04 |
| [R07](tasks/TASK-R07-console-output.md) | resume header, history, next step; golden files | S | R06 |
| [R08](tasks/TASK-R08-integration.md) | integration scenarios | M | R06, R07 |
| [R09](tasks/TASK-R09-documentation.md) | MVP.md §37/§38.5, CLAUDE.md, README | S | all |

```
R01 ──┬── R02 ──┐
      ├── R03 ──┤
      ├── R04 ──┴── R06 ── R07 ── R08 ── R09
      └── R05 ─────────────────────────┘
```

Estimate: 7.5–9 engineering days (`SPEC.md` §15). R02, R03, R04 and R05 are independent
after R01. `run` behaves exactly as before until R06, except that it records `head_ref`
and `resume_baseline` (R04) and denies its runs directory to the file tools (R05).

## Ground rules for every task

- `internal/workflow` and `internal/policy` import only `domain`. `persistence` does not
  import `workflow` or `action`. Anything that needs both is wired in `app`.
- Nothing is re-decided on resume. No decision provider or translator is constructed for
  `resume`.
- Persisted state is input: validate strictly, derive paths, refuse on inconsistency, never
  repair.
- No `git status` (or any other command that can run `core.fsmonitor`) on a resumed tree
  before the git fingerprint matches the baseline.
- `Executions` stays append-only.
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .`
  empty. Existing integration scenarios 1–17 keep passing unchanged.

## Deferred

- **A checkpoint per step** (a snapshot commit after each successful step), so the resume
  header can show "changed since the stop" rather than the whole working tree.
- **Review against `head_ref`** instead of `HEAD`, which would make a moved HEAD resumable
  (Q5).
- **Pinning `pre_run_ref`** under `refs/orchestrator/runs/<id>` so `git gc` cannot prune the
  restore point.
- **`orchestrator runs` / `show`**: listing and inspecting runs.
