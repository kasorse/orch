# ST01 — `internal/story`: task files, graph, order, bases, README rendering

**Size** M (2 d) · **Depends on** — · **Blocks** ST04, ST05, ST06

## Goal

The pure core of a story: read and write the spec's task files, validate the graph, compute the
order and each task's base, render README.md (`SPEC.md` §8, §9). No git, no process, no I/O
beyond the files it is handed.

## Files

- `internal/story/task.go`: `Task{ID, Repo, DependsOn, Jira, Title, Body, Acceptance}`;
  `ParseTaskFile(name string, data []byte) (Task, error)` — front matter between `---` lines,
  keys `id`, `repo`, `depends_on` (`[A, B]` or empty), `jira` (`<KEY>` or `none`), parsed by
  hand, anything else an error; the file name carries the ID; the title from the `# <ID> — ...`
  heading; `Acceptance` from the `## Acceptance` list items; the body bounded to 16 KiB;
  `FrozenHash(data)` — sha256 of everything before the first `## As built` line;
  `RenderTaskFile(Task, sizes...)` for `story plan` and `SetJira(data, key)` for ST07, both
  byte-stable
- `internal/story/graph.go`: `Validate(tasks, repos []string, maxTasks int) error` (IDs,
  prefix, repositories in the closed list, missing dependencies, cycles naming the tasks);
  `Order(tasks) []string` (Kahn, smallest ID first); `Base(tasks, id) (parent string, err)` —
  §9.2: the same-repository ancestors no other such ancestor depends on; none → `""`, one → it,
  more → the error text of §9.2
- `internal/story/readme.go`: `RenderREADME(title, status, tasks, states)` (golden)
- `internal/story/asbuilt.go`: `AppendAsBuilt(data []byte, AsBuilt) []byte` — §10.3's section

## Acceptance

- Unit: round trip of a task file; each front matter error names the key and the file; the
  order on ties; cycle and missing dependency messages; the base rule — none, stacked, an
  ancestor in the same repository reached through another repository, and the diamond refused;
  the frozen hash ignores everything from `## As built` on and changes with any edit above it.
- Golden: README for a planned, a running and a completed story.
- An import test: `internal/story` imports only `domain` and the standard library.

## Gates

`scripts/check.sh`
