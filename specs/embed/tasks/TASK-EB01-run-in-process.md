---
id: EB01
depends_on: []
---
# EB01 — the run in process: `runTask`/`resumeRun` with a directory, the result as a value, the child mode deleted

**Size** L

## Goal

SPEC.md §2: the listener runs a ticket by calling the run, not by starting a child. The child
mode and `run --listener-task` are deleted (O2). `story run` keeps its children.

## Files

- `internal/app/app.go`: `runTask` takes `dir string` and `task *listenerTask` in place of
  `listenerTaskFile`; `run` passes `os.Getwd()` and nil; `reporter.Cwd` and `repository.Open`
  use `dir`. `parseArgs` refuses `--listener-task` with a message naming this spec. The result
  value (`buildResult`) is returned to the caller through the recorder.
- `internal/app/resume.go`: `resumeRun` likewise takes `dir`.
- `internal/app/listener_task.go`: keep the type, `parseListenerTask`, `readListenerTask`,
  `listenerTaskSource`; delete `checkListenerTaskFlags`.
- `internal/app/listen.go`: `execute` calls `runTask`/`resumeRun` with the service directory,
  `recover` (§2.2), the output to `r.stderr` (§2.3); delete `childLine`, `parseChildLine`,
  `r.exe` for the run (it stays for the helper's entries), the `code < 0`
  branch of `afterChild`. `environment()` becomes the one-time shaping of the process
  environment (§2.4): `os.Setenv` of the helper's entries, `os.Unsetenv` of the Jira and
  parent-session variables after the Jira client is built, `execution.ScrubCredentials` of the
  Jira and GitLab names. `listen` takes `dir` (`jira listen` passes `os.Getwd()`), for EB03.
- `internal/app/story_run.go`: `runChildEnv` stays (story run).
- The §2.6 review of package-level state in `internal/app`, `internal/action`,
  `internal/execution`, `internal/provider`; what it finds, per run or justified in a comment.
- Docs in the same task: MVP.md §4 (`--listener-task` gone), §51 summary, §51.1, §51.7, §51.10,
  §51.11; CLAUDE.md invariant "The listener owns Jira; its child never calls it" (→ the run never
  calls it), "Run artifacts" (the task file as the run's input); README.md "Running the Jira
  listener".

## Acceptance

- `test/integration/listen_test.go`, `listen_fakes_test.go`, `listener_task_test.go`: every
  scenario passes with the tickets run in the test's process; `TestMain` still stands in for
  `mcp-verify` and `git-credential` only.
- New tests of SPEC §7: two tickets in one process with no carry-over and no leak (file
  descriptors, goroutines, within a tolerance); the Jira variables absent from a git and a test
  process during the run (`FAKE_ENV_DIR`); an injected panic → `failed`, the listener polls
  again; `run --listener-task` refused.
- A test asserts no `os.Getwd()`/`os.Chdir` on the run and resume paths of `internal/app`
  (the files the paths consist of, by name).
- `story run`'s tests unchanged. `scripts/check.sh` passes.
