# Decision shape — size, templates, escalation

Status: **in progress** (2026-10-13): DS02, DS03, DS04 and DS06 done. The owner decided O1–O3 (`SPEC.md` §11):
escalation yes, target shares 50/35/15, and the gate passed on the measured data (`SPEC.md`
§1.1). Not yet in TODO.md.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 7 implementation tasks |

## The problem in one paragraph

The decision asks one complexity distribution and six stage probabilities, but all seven carry
one signal: how big the task looks. Simple tasks get every stage low, complex ones every stage
high, `force[complex]` overrides the rest, and the first benchmark round called every task
`medium` (MVP.md §9.1), so `by_complexity` routes everything to one model. This spec:
- turns complexity into one number, `size`, cut into levels by quantiles calibrated on history;
- keeps only the three factual stage questions (development, research, analysis) and takes
  planning, testing and review from a per-level template (the existing `force`);
- adds cut proposals to `decisions calibrate`, by target share and by outcome;
- adds opt-in escalation: a fix after a failed check uses the next level's model.

## Tasks

| task | title | size | depends on |
|---|---|---|---|
| [DS01](tasks/TASK-DS01-diagnose.md) | `decisions calibrate`: size histogram, stage/size correlation, quantile cuts | S | — |
| [DS02](tasks/TASK-DS02-size.md) | `size`, `policy.complexity.cuts`, `questions_hash` ✓ | M | — |
| [DS03](tasks/TASK-DS03-questions.md) | three stage questions: domain, Jev, LLM, mock, goldens ✓ | M | — |
| [DS04](tasks/TASK-DS04-template.md) | the stage rule: `force` as template, testing with development ✓ | M | DS03 |
| [DS05](tasks/TASK-DS05-calibrate-cuts.md) | cut proposals by share and by outcome, refusal on no spread | M | DS01, DS02 |
| [DS06](tasks/TASK-DS06-escalate.md) | `actions.implement.escalate` ✓ | S | DS02 |
| [DS07](tasks/TASK-DS07-integration-docs.md) | scenarios, MVP.md, CLAUDE.md, README, example, bench configs, TODO.md | M | DS02–DS06 |

```
DS01 ────────→ DS05 ─┐
DS02 ─┬→ DS05        │
      └→ DS06 ───────┼→ DS07
DS03 → DS04 ─────────┘
```

Estimate: about 9–12 engineering days. The gate (O3) already passed on a one-off measurement
(`SPEC.md` §1.1), so DS01, DS02 and DS03 can start together. Size predicts fix iterations only
weakly on that data, so DS06 (escalation on a failed check) is worth the most after DS02:
recommended order DS02 → DS06 → DS03 → DS04 → DS01 → DS05 → DS07.

## Ground rules for every task

- **Go decides, from config and the persisted run.** Size, level, stages and the escalated
  route are pure functions of the decision, the config and `run.json`.
- **Providers return probabilities only.** Jev's `score` and `choice` are recorded, never used.
- **No silent change of meaning.** A key whose meaning changes is rejected with a message, not
  reinterpreted.
- **Calibration only proposes.** Nothing writes config.
- `internal/workflow` and `internal/policy` import only `domain`. `internal/report` imports
  only `domain`.
- Any wording change in `internal/decision/questions.go` recalibrates thresholds and changes
  `questions_hash`.
- Every new `decision.json` and `run.json` field is validated strictly on load. Every new
  config key is declared in the schema.
- Tests need no network and no real CLI.
- Each task ends green on `scripts/check.sh` (root and `bench/`).
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add
  an `## As built` section to the task file with the date and anything built differently,
  and why.
