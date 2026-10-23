---
id: PS04
repo: orche
depends_on: [PS03]
jira: none
---
# PS04 — End-to-end scenarios and the documentation

**Size** M

## Goal

The parallel story is covered end to end in `test/integration`, and the normative documents
describe it: MVP.md is the source of truth, and CLAUDE.md, TODO.md, README.md and the example
config agree with it (SPEC.md, all sections).

## Files

- `test/integration/story_parallel_test.go`, with fake executables and no network, like
  `story_run_test.go`:
  1. two slots, four tasks: A and B independent in one repository, C depending on A and B, D in a
     second repository depending on nothing. A, B and D run at once. C starts after the merge
     on `story/<slug>/spec`, from the merge commit. The story completes, and the end table shows
     every task's target (A, B, D: the spec branch; C: the spec branch).
  2. the same with A and B in conflict: C is `conflict`, the story is `blocked`, and the hint
     names the files. After a scripted manual merge on the spec branch, `story run` completes the
     story.
  3. two slots, B fails while A runs: A is settled, C is never started, and the story is `failed`.
     The next `story run` resumes B, and runs C once B is done.
  4. one slot: the scenario of `TestStoryRun_OrderStackingAsBuiltAndTheEnd` gives the same output
     as before, apart from the spec branch lines.
- MVP.md:
  - §48: the spec branch, several parents and their merge, the `conflict` state, the slots and
    the parallel loop, the logs, the budget with runs in flight, the start check. Write it as a
    new subsection, or fold it into §48.4 and §48.6; the "Tasks run one at a time" sentences
    change.
  - §30: the parallel line says that a story runs up to `story.max_parallel` tasks at once, each
    its own run, and that one run's workflow stays sequential.
  - §4: `story run --parallel N`.
  - the config block of §48.1: `max_parallel`.
- CLAUDE.md: the story invariant ("tasks run one at a time"), the out-of-scope list ("no parallel
  or multi-agent workflows": the exception for story tasks), and the run artifacts
  (`logs/task-<ID>.<attempt>.log`, `bases.<repo>.spec_branch`, `conflict`).
- TODO.md: the parallel story as done, with a pointer to `specs/parallel-story/`.
- README.md (root): the story section mentions `--parallel` and the spec branch.
- orchestrator.example.yaml: `max_parallel: 1` commented out under `story:`, with one line on
  what it does and the test-resources caveat (SPEC.md Risks).

## Acceptance

- The four scenarios pass under `go test ./test/integration/...`.
- MVP.md, CLAUDE.md and README.md say nowhere that story tasks always run one at a time.
- `orchestrator.example.yaml` still loads in the config tests that read it.
- `scripts/check.sh` passes.

## Notes

Do not edit anything under `specs/parallel-story/` (story run writes its README and As built).
This is the only task of the story that edits the shared documents.
