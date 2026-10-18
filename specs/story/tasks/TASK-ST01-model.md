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
  prefix, repositories among the given service names, missing dependencies, cycles naming the
  tasks);
  `Order(tasks) []string` (Kahn, smallest ID first); `Base(tasks, id) (parent string, err)` —
  §9.2: the same-repository ancestors no other such ancestor depends on; none → `""`, one → it,
  more → the error text of §9.2
- `internal/story/readme.go`: `RenderREADME(title, status, tasks, states)` (golden)
- `internal/story/asbuilt.go`: `AppendAsBuilt(data []byte, AsBuilt) []byte` — §10.3's section
- `internal/story/services.go`: `Named(text string, components, candidates []string) []string` —
  §7.1 point 2: whole-word, case-insensitive, Unicode-aware boundaries; components by equal name

## Acceptance

- Unit: round trip of a task file; each front matter error names the key and the file; the
  order on ties; cycle and missing dependency messages; the base rule — none, stacked, an
  ancestor in the same repository reached through another repository, and the diamond refused;
  the frozen hash ignores everything from `## As built` on and changes with any edit above it.
- `Named`: `billing` in `… в сервисах billing и gateway.` and `Billing,`; not in `billing-v2`,
  `prebilling` or `billing_old`; a component `Gateway` names `gateway`; a candidate named `c++`
  is impossible (the name pattern), so no regex is built from a name.
- Golden: README for a planned, a running and a completed story.
- An import test: `internal/story` imports only `domain` and the standard library.

## Gates

`scripts/check.sh`

## As built

2026-10-18. `internal/story` is in, with unit, golden and import tests. `scripts/check.sh` is the
run's test step; it was not run by hand here.

- **Sizes.** `RenderTaskFile(t Task, size string)` takes one size (`""` leaves the `**Size**` part
  out), since a file is rendered per task. The generated line `**Size** M · **Repository** r ·
  **Depends on** …` is redundant with the front matter, so `ParseTaskFile` drops it from
  `Task.Body` (a line that starts with `**Size** ` or `**Repository** ` right after the heading).
  `Body` is the text after the heading and that line, verbatim, so `Parse(Render(t))` gives `t` and
  `Render` is byte-stable. ST04 builds `Body` as `"\n## Goal\n…"`.
- **Front matter.** `id` and `repo` are required; `depends_on` (`[A, B]`, `[]`, or empty) and `jira`
  are optional. `jira` is `none` or `^[A-Z][A-Z0-9_]+-[1-9][0-9]{0,8}$`; an empty `jira:` is an
  error. Blank lines are skipped; a repeated key, an indented line, a line without a colon, a
  quoted value and a comment are errors. The heading must be the first line after the front matter
  and its ID must equal `id`. Errors read `<file>: front matter key "<key>": …`.
- **`FrozenHash`** is the sha256 (hex) of the file up to the first line that is `## As built` or
  starts with `## As built `, **with the trailing line breaks of that part removed**. The
  difference from "everything before" is deliberate: `AppendAsBuilt` must put a blank line before
  its section, and without it the hash recorded at dispatch would no longer match after the append.
  The heading counts also inside a code fence: the rule must be the one Go itself applies.
- **`SetJira`** replaces the value of a `jira` line or adds the line last in the front matter, in
  the file's line ending; every other byte is kept.
- **Graph.** `Validate` returns the first problem, checked in the order count, ID shape and
  uniqueness, prefix, repository, dependencies, cycle; a zero or exceeded `maxTasks` is an error
  (fail closed). A cycle names its loop (`RF02 -> RF03 -> RF02`), not the tasks waiting on it.
  `Base` drops an ancestor that another same-repository ancestor depends on directly **or through
  other tasks**, since that task's own branch already contains it.
- **README.** `TaskStatus` (`pending`, `running`, `done`, `failed`, `blocked`, `stopped`) and
  `Status` (`planned`, `running`, `completed`, `blocked`, `failed`, `stopped`) are fixed here, with
  `TaskState{Status, Branch}`. `RenderREADME` returns a string and links no task files (it does not
  know their names).
- **`AsBuilt`** carries the run's data; `Summary` is put on one line after a fixed label, and every
  other text field is collapsed to one line too, so nothing a model wrote can start a line.
  `AppendAsBuilt` takes the date from the caller (no clock).
- **`Named`** folds case rune by rune, treats a letter, a digit (Unicode), `_` and `-` as word
  characters, and returns the matches sorted. Combining marks are not word characters.
- **Import test** forbids, outside the tests, `os`, `os/exec`, `net`, `net/http`, `syscall` and
  `io/ioutil` as well; `domain` is allowed but nothing imports it.
