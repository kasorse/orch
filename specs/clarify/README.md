# Clarify — a clear task before the decision; one stage that reads the code

Status: **draft** (2026-10-25): the owner decided D1–D14 (`SPEC.md` §2); the author's defaults
A1, A4–A6 (`SPEC.md` §13) are open. No task started. TODO.md Q9.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 7 implementation tasks |

## The problem in one paragraph

`analyze` and `plan` both read the repository and return the places to change, so a complex task
pays for two read-only passes and the plan restates the analysis. Nothing checks whether the task
says what it wants: an unclear task is classified, implemented, tested and reviewed on a guess.
This spec:
- asks the decision provider `P(clear)` in the pre-check, on the request text alone;
- below `policy.clarify.threshold`, runs a **clarify** step before the decision: an LLM call with
  no tools and Go-selected instruction excerpts; the operator answers its questions at a
  terminal, the model answers from the instructions under `--mode auto`;
- asks again; still unclear: the operator adds to the task, or the run stops `needs input` with
  what is unclear; a failed clarify call fails the run; `clarify.enabled: false` (the benchmark)
  turns the step off and keeps recording `P(clear)`;
- drops analysis from the stages (two are asked: research, development) and renames planning to
  **investigation** (`investigate`): it reads the code and plans, or, without development,
  answers.

## Tasks

| done | task | title | size | depends on |
|---|---|---|---|---|
| [ ] | [CL01](tasks/TASK-CL01-analysis-leaves-stages.md) | Analysis leaves the stages: two asked stages, fallback, config errors | M | — |
| [ ] | [CL02](tasks/TASK-CL02-investigation.md) | `planning`/`plan` → `investigation`/`investigate`, with `root_cause` | M | CL01 |
| [ ] | [CL03](tasks/TASK-CL03-clarity-question.md) | The clarity question: pre-check, re-check, threshold, trace | M | — |
| [ ] | [CL04](tasks/TASK-CL04-docs.md) | `projectctx.Docs`: instruction excerpts for clarify | S | — |
| [ ] | [CL05](tasks/TASK-CL05-clarify-step.md) | The clarify step: call, operator, auto, needs input, `task.clarification` | L | CL03, CL04 |
| [ ] | [CL06](tasks/TASK-CL06-stages-get-clarification.md) | Stages get the clarification; summary.md | S | CL02, CL05 |
| [ ] | [CL07](tasks/TASK-CL07-integration-docs.md) | Scenarios, MVP.md §49, CLAUDE.md, README, example, bench, TODO.md | M | CL01–CL06 |

```
CL01 → CL02 ───────────┐
CL03 ─┬→ CL05 → CL06 ──┼→ CL07
CL04 ─┘        ↑ CL02  │
```

Estimate: about 12–15 engineering days. CL01, CL03 and CL04 can start together. Recommended
order: CL01 → CL02 (the rename settles every later diff) → CL03 → CL04 → CL05 → CL06 → CL07.

## Ground rules for every task

- **Go decides.** Clarity is a probability against a threshold; the clarify model's points never
  decide whether the run goes on.
- **The decision provider sees text only**, never an instruction file (D4).
- **The clarify call has `AuthorityNone`**, passed explicitly; it is never a workflow action and
  never in `AuthorityFor`.
- **No silent change of meaning.** `analysis`, `planning`, `actions.analyze`, `actions.plan` are
  rejected with a message, not mapped to the new names.
- **Resume continues, never re-asks.** `task.clarification` is written once, before the decision.
- `internal/workflow` and `internal/policy` import only `domain`; `internal/report` only `domain`.
- Any wording change in `internal/decision/questions.go` changes `questions_hash`; thresholds are
  recalibrated afterwards (D10).
- Every new `run.json` and `decision.json` field is validated strictly on load; every new config
  key is declared in the schema.
- Tests need no network and no real CLI.
- Each task ends green on `scripts/check.sh` (root and `bench/`).
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add an
  `## As built` section to the task file with the date and anything built differently, and why.
