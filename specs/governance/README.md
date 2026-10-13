# Governance — control for teams (phase 4)

Status: **in progress** (specified 2026-10-12; 2026-10-13): GV02, GV03, GV04, GV05, GV06 and
GV07 are written and their tests with them, and GV08 has written the documentation (MVP.md §45
and the sections it touches, CLAUDE.md, README, `orchestrator.example.yaml`, `org.example.yaml`),
but `scripts/check.sh` has not been run over any of it yet, so nothing is ticked below or in
TODO.md G1, G2, G3 and G5 (G1.1, G1.2 and G1.4 are what GV02 covers, G1.3 what GV03 covers, G2
what GV04 covers, G3 what GV01 covers, G5.1 and G5.2 what GV05 covers, G5.3 what GV06 covers;
GV07 and GV08 have no TODO.md item). Once the script passes, tick the tasks and G1, G2, G3 and
G5 (G4 is already `[-]`), and change this line to done.

The owner went through TODO.md G1–G5. G1, G2, G3 and G5 are in this spec. G4 (audit export) is
dropped (`SPEC.md` §9): a local CLI on each developer's machine gives no central trail, and a
hosted runner already keeps the run directory. The decisions are in `SPEC.md` §10, and the
author's defaults are in §11. What was built differently, and why, goes into each task's
"As built" section.

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 8 implementation tasks |

## The problem in one paragraph

A team lead can forbid a path (`security.protected_paths`), but cannot ask for more care
there: "a change under `migrations/` needs review and testing, and a person's yes before the
commit". All config lives in the repository, so whoever edits `orchestrator.yaml` can turn off
the sandbox or the secret scan. A run has a step limit and no cost limit. The stage
thresholds were set by hand, and no tool reads the run history to say whether they fit. This
phase:
- adds path rules checked on the actual diff;
- adds an org config file that a repository can only tighten;
- adds a per-run cost budget from the cost the CLI already reports;
- adds a read-only calibration report, with opt-in exploration as its only source of
  counterfactuals.

## Tasks

| task | title | TODO | size | depends on |
|---|---|---|---|---|
| [GV01](tasks/TASK-GV01-budget.md) | `limits.max_cost_usd`: fail fast, engine stop, resume | G3 | S | — |
| [GV02](tasks/TASK-GV02-path-rules.md) | `policy.paths`: config, start warning, check after implement, record | G1.1–2, G1.4 | M | — |
| [GV03](tasks/TASK-GV03-approval.md) | `approval: manual` in the commit offer, `outcome.approval_required` | G1.3 | S | GV02 |
| [GV04](tasks/TASK-GV04-org-config.md) | org config layer: find, strict schema, merge, record | G2 | M | GV01, GV02 |
| [GV05](tasks/TASK-GV05-calibrate.md) | `policy_hash`, `orchestrator decisions calibrate` | G5 | M | — |
| [GV06](tasks/TASK-GV06-explore.md) | `policy.explore`: opt-in stage exploration | G5.3 | S | GV05 |
| [GV07](tasks/TASK-GV07-integration.md) | integration scenarios 47–56 | — | M | GV01–GV06 |
| [GV08](tasks/TASK-GV08-documentation.md) | MVP.md §45 and others, CLAUDE.md, README, example config, TODO.md | — | S | GV07 |

```
budget   GV01 ──────────┐
paths    GV02 → GV03    ├→ GV04
         GV02 ──────────┘
calib    GV05 → GV06
all      GV01…GV06 → GV07 → GV08
```

Estimate: about 12–15 engineering days. Three tracks can start on day one: GV01, GV02 and
GV05. Recommended order for one person: GV01 (the smallest, with the most direct value) →
GV02 → GV03 → GV04 → GV05 → GV06 → GV07 → GV08. GV05 is worth the most after a few dozen real
runs exist, so it can wait for them.

## Ground rules for every task

- **The diff is the truth.** Path rules match what git says changed, never a model's list of
  files.
- **No workflow rebuilt mid-run.** A rule that needs a missing stage stops the run with a
  message. It never inserts the stage.
- **Go decides, from config and the persisted run.** Rules recorded at run start are what a
  resume applies. Every new stop is derivable from `run.json`.
- **Fail closed.** An unenforceable budget, an unreadable org file and a repository setting
  weaker than the org's are each a config error before any call.
- **Calibration only proposes.** Nothing writes config. Exploration only turns stages on.
- `internal/workflow` and `internal/policy` import only `domain`. `internal/report` imports
  only `domain`.
- Provider-specific behavior stays in `internal/provider/<name>` (`ReportsCost`).
- Every new `run.json` and `decision.json` field is validated strictly on load. Every new
  config key is declared in the schema (strict parsing). The org file has its own strict
  schema.
- Tests need no network and no real CLI.
- Each task ends green on `scripts/check.sh` (`go build`, `go vet`, `go test`, an empty
  `gofmt -l .`, root and `bench/`).
- When a task is done: tick it here and in TODO.md, update the `Status:` line above, and add
  an `## As built` section to the task file with the date and anything built differently,
  and why.
