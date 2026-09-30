# R01 — Run fields, `LoadRun`, run-ID resolution, lock, `RunStateError`

**Size** M (1 d) · **Depends on** — · **Blocks** R02–R06

## Goal

A persisted run can be found by ID or prefix, loaded under strict validation and locked
against a second process. The run carries the fields resume needs (`SPEC.md` §6.3, §11–§13).

## Files

- `internal/domain/run.go` — `HeadRef`, `ResumeBaseline json.RawMessage`, `Resumes []Resume`,
  `Resume`, `StepExecution.Attempt`
- `internal/persistence/filesystem.go` — `ResolveRunID`, `LoadRun`, `Lock`
- `internal/persistence/validate.go` (new) — structural checks
- `internal/apperr/errors.go` — `RunStateError` + kinds
- `internal/exitcode/exitcode.go` — mapping (exit 1)
- tests next to each

## Steps

1. Domain fields as in `SPEC.md` §12, all `omitempty`. `ResumeBaseline` is raw JSON, so
   `domain` does not know the guard's shape. Add a `RunStatus`/`RunMode`/`ActionType`/
   `Authority` validity helper where one is missing.
2. `RunStateError{Kind, RunID, Msg}` with kinds `not_found`, `ambiguous`, `in_use`,
   `not_resumable`, `corrupt`. Follow `DecisionError`: `Error()` renders the message, and
   `exitcode.FromError` maps every kind to `GenericError` (1). Put it before the generic
   fallthrough and test each kind.
3. `ResolveRunID(idOrPrefix)`: an exact directory name wins. Otherwise, among directory
   names that parse as UUIDv4, the unique one with that prefix. A prefix shorter than 8
   characters → `not_found` with a "use at least 8 characters" hint. Several matches →
   `ambiguous` listing them. The input must not contain a path separator or `..` (it is
   joined to a path).
4. `LoadRun(id)`: `json.Decoder` with `DisallowUnknownFields`. Missing directory or
   `run.json` → `not_found`. Otherwise, any failure → `corrupt` naming the rule:
   `run.ID == id`; valid enums; `Executions[i].Index == i`; no `running` entry;
   `ArtifactPath`, when set, equals the derived `artifacts/%03d-%s.json`. The workflow
   consistency check is R02's `workflow.Verify`, and the git checks are R04's.
5. `Lock(runID) (func(), error)`: `os.Open(runDir)` and `syscall.Flock(fd,
   LOCK_EX|LOCK_NB)`. `EWOULDBLOCK` → `in_use`. The returned func unlocks and closes. Keep
   the `*os.File` alive for the lifetime of the lock, because a finalizer closing the fd
   releases it.

## As built

- `persistence.ArtifactRelPath` is exported and now used by `SaveArtifact`, so the path
  `LoadRun` compares with (and R03 reads from) has exactly one source.
- `LoadRun` also checks workflow step actions, `resumes[]` enums and a non-negative
  `attempt`, and it rejects data after the run object. `total_steps` and `fix_iterations`
  are not checked: R02 derives them from the history (G12).
- `ResolveRunID` lowercases its input and accepts only hex digits and dashes, so it cannot
  form a path outside the runs directory. Only directories shaped like a run ID are
  prefix-matched.
- `RunStateError` is checked first in `exitcode.FromError`, so it stays exit 1 whatever
  error it wraps.
- Files: `internal/apperr/run.go`, `internal/persistence/{load,validate,lock}.go` and
  their tests; `Valid()` on `RunStatus`, `StepExecStatus`, `RunMode` and `ActionType`.

## Acceptance

- Round-trip: `NewRun` → `SaveRun` → `LoadRun` gives an equal run, new fields included.
- Unknown field, `ID` mismatch, a gap in indices, a `running` execution, and
  `artifact_path: "../../x"` → each is `corrupt`.
- Prefix resolution: exact, unique, ambiguous, too short, `../` input.
- Two `Lock` calls on the same run in one process (separate fds) → the second is `in_use`.
  After unlock, it succeeds.
- `exitcode.FromError` → 1 for every kind.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
