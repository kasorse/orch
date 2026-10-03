# Run chosen stages (`run --stage`)

Status: **implemented** (2026-10-03). All tasks S01–S07 are done. MVP.md §40 is the
normative home; this folder is the design record. The design questions are answered
(`SPEC.md` §13) and the defaults D1–D6 were kept. What was built differently, and why, is in
each task's "As built" section.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 7 implementation tasks |

## The problem in one paragraph

Every `run` asks a decision provider which stages to run. An operator who already knows
(review code changed by hand, just run the tests) cannot say so. `orchestrator run --stage
review --stage testing "<task>"` names the stages, skips the whole decision layer, and enters
the existing path at `workflow.Build`. Order, transitions, authorities, the guard and resume
are unchanged.

## Tasks

| task | title | size | depends on |
|---|---|---|---|
| [S01](tasks/TASK-S01-domain.md) | stage aliases, `manual` source, selection → decision ✓ | S | — |
| [S02](tasks/TASK-S02-gap-warnings.md) | gap warnings ✓ | S | S01 |
| [S03](tasks/TASK-S03-empty-review.md) | review fails on an empty diff ✓ | S | — |
| [S04](tasks/TASK-S04-cli-and-flow.md) | `run --stage`: CLI, flow, console ✓ | M | S01, S02 |
| [S05](tasks/TASK-S05-trace-and-persistence.md) | `decision.json` / `run.json` for a manual run ✓ | S | S01, S04 |
| [S06](tasks/TASK-S06-integration.md) | integration scenarios ✓ | M | S03–S05 |
| [S07](tasks/TASK-S07-documentation.md) | MVP.md, CLAUDE.md, README ✓ | S | all |

```
S01 ──┬── S02 ──┐
      ├── S04 ──┼── S05 ── S06 ── S07
S03 ──┴─────────┘
```

Estimate: about 4 engineering days. S01 and S03 can start in parallel.

## Ground rules for every task

- `internal/workflow`, `internal/policy` and `internal/domain` import nothing new
  (`workflow` only `domain`).
- Provider-specific code is untouched; authorities are untouched.
- A manual run constructs no translator, decision provider or Context Manager.
- Tests need no network. Runs without `--stage` stay byte-for-byte unchanged, and
  integration scenarios 1–19 keep passing.
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...`, and
  `gofmt -l .` empty.
