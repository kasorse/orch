# The listener as a library: the run in process, a public package, a service on the platform

Status: **draft** (2026-11-04): the spec is written, no task started. The owner's answers are
`SPEC.md` §0 (O1–O7); the module path is `github.com/kasorse/orch` (O6), and making the
repository public is the owner's, outside the tasks.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 4 implementation tasks |

## The problem in one paragraph

A corporate service built on the platform library should be able to import the orchestrator and
run the Jira listener as one of its runners, deployed through the shared GitLab CI, with API keys
as the only provider access. Today the module is not importable (`module orchestrator`,
everything under `internal/`), the listener runs each ticket as a child of its own executable,
and the run assumes it is a process (`os.Getwd()`, `os.Environ()`, a JSON line on stdout). The
spec makes the run a Go call the listener makes in its own process (the child mode is deleted),
renames the module, and adds one public package with `Listen` and `Embedded`, the latter so that
a host binary still answers the two execs that remain, `git-credential` and `mcp-verify`.

## Ground rules

- A task is done when its acceptance holds, `scripts/check.sh` passes, its row below and
  `TODO.md` P14 are ticked, this `Status:` line is current, and the task file has an `## As built`.
- Each task updates the documentation its own change makes out of date (CLAUDE.md, "Tasks from
  specs/*/tasks"); EB04 adds only what needs every task done (MVP.md §53).
- Tests need no network, no key and no platform: the fakes of `test/integration` and the
  standard library. The live check on the platform (SPEC §7, last item) is the operator's gate.
- Nothing outside the listener changes behaviour: `run`, `resume`, `review`, `story run` and their
  tests stay as they are, except where SPEC §2.1 names the parameter they gain.

## Tasks

| done | task | title | size | depends on |
|---|---|---|---|---|
| [ ] | [EB01](tasks/TASK-EB01-run-in-process.md) | the run in process: `runTask`/`resumeRun` with a directory, the result as a value, the child mode deleted | L | — |
| [ ] | [EB02](tasks/TASK-EB02-module-path.md) | the module path (O6): `go.mod`, every import, `bench/boundary_test.go` | S | — |
| [ ] | [EB03](tasks/TASK-EB03-package.md) | the public package `orchestrator`: `Listen`, `Embedded` | M | EB01, EB02 |
| [ ] | [EB04](tasks/TASK-EB04-example-docs.md) | `examples/embed/`, MVP.md §53, the summaries | S | EB03 |
