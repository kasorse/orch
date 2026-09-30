# R08 — Integration scenarios

**Size** M (1–1.5 d) · **Depends on** R06, R07 · **Blocks** R09

## Goal

End-to-end evidence, with fake executables and no network, that resume continues correctly
and refuses what it must (`SPEC.md` §14).

## Files

- `test/integration/scenario18_resume_*_test.go` … one file per scenario group
  (scenario 17 is R04's `scenario17_resume_inputs_test.go`, which already covers item 9)
- `test/integration/helpers_test.go` — `resume(t, dir, id, args...)`, run-JSON load/edit
  helpers

Fakes are routed by the `ACTION: <name>` prompt line. Call order and prompts come from
`FAKE_ARGS_DIR`.

## Scenarios

1. **Retry a failed step.** implement fails (the fake exits 1) → `resume --mode auto` →
   implement runs again, then test and review, and the run completes. `executions` has 3 +
   3 entries; the new ones have `attempt: 1`. The second implement prompt contains the plan
   text. There is one `resumes` record with `from_status: failed`.
2. **Limits.** test always fails, `max_fix_iterations: 1` → stopped. Resume with the same
   config → exit 4 before any fake call, and the message names the key. Raise to 2 and
   resume → implement runs with the test failure output fenced in its prompt.
3. **Plan then execute.** `run --mode plan` → `pending`, no restore point. `resume --mode
   auto` → `pre_run_ref`, `head_ref` and `resume_baseline` are recorded, and the run
   executes from the first step.
4. **Interrupted.** Edit `run.json` to `status: running` with no lock held → resume
   reports "interrupted" and continues.
5. **Guard violation.** A fake implement writes `.claude/settings.json` → failed. Resume →
   exit 4, protected path named, no fake call. Delete the file and resume → implement is
   retried.
6. **Planted fsmonitor.** Between the runs, append `[core] fsmonitor = …` to
   `.git/config` → refused. The recorded git argv contains no `status` after the refusal
   check began, and the fsmonitor marker file does not exist.
7. **HEAD moved.** Commit something between the runs → refused, both SHAs shown.
8. **Tampering.** (a) a workflow in `run.json` that does not match the decision;
   (b) a deleted artifact of a completed step; (c) `artifact_path: "../../../etc/passwd"`
   → each exits 1 with `corrupt`.
9. **Resume inputs protected.** A fake implement edits its own `run.json` → the step fails
   at the guard.
10. **Completed.** Resume a read-only run that completed → exit 0, no fake call, and the
    answer is printed.
11. **IDs.** A unique 8-character prefix works. An ambiguous prefix lists both IDs. A
    7-character prefix and an unknown ID → exit 1.
12. **In use.** Hold `Store.Lock` from the test → resume exits 1 with `in_use`.

## Acceptance

All scenarios pass under `go test ./test/integration/...`, as do scenarios 1–17, with no
network and no real provider CLI.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
