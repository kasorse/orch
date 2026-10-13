# Spec: Decision shape — size, templates, escalation

Status: **in progress** (2026-10-13): DS01–DS04 and DS06 are built (their task files say how). The owner decided O1–O3 (§11); the gate
O3 passed on the data of §1.1. The author's defaults are in §12. Once DS07 is done, MVP.md (§9, §9.1, §31.1, §44.8) becomes the
normative home and this file stays the design record.

The spec touches these MVP.md sections:
- §8–§9: the decision flow and the policy;
- §9.1: the example thresholds;
- §14: complexity;
- §31.1: static routing (only DS06);
- §44.8: complexity chooses the model;
- §45 / `specs/governance/` §7: calibration.

---

## 1. Problem

The decision asks one complexity distribution and six yes/no stage probabilities, then picks a
level by absolute thresholds and a stage by its own threshold plus `force[complexity]`. In
practice the seven answers carry one signal:

1. **The stage questions restate complexity.** `planning` is "yes" when the work "spans several
   steps, files or components"; `review` when the change is "non-trivial or risky". The
   complexity criteria (`complexityCriteria`) say the same thing. One model answers all of them
   from one context, so they move together (a halo effect): a small task gets every stage low,
   a large one every stage high.
2. **`force` already ties stages to the level.** `force.complex: [analysis, planning, review]`
   overrides whatever the stage answers say for a complex task.
3. **Absolute thresholds on verbalized probabilities sit inside clusters.** A model writes
   0.1, 0.7, 0.85, 0.9, not a smooth scale. MVP.md §9.1 records the first benchmark round: with
   the earlier thresholds analysis and review were on in every run (0.65–0.95 and 0.80–0.95),
   planning was a coin toss (0.10–0.85 on the same task), and **every task was `medium`**
   (P(medium) ≈ 0.85). Raising the thresholds to 0.81 moved the coin, it did not add signal.
4. **Complexity does not separate tasks, so `by_complexity` has nothing to route on.** Phase 2
   added per-level models (MVP.md §44.8); with one level in practice they are one model.
5. **The decision costs more than it decides.** `auto` cost about 2.4× development + testing on
   that round, with no task solved that development + testing missed.

### 1.1 Measured (2026-10-13)

A one-off script computed what DS01 will print, over every non-manual `decision.json` in the
repository: 43 local runs decided by claude/opus (`.orchestrator/runs`, the QL/BM/GV tasks of
this repository) and 22 benchmark runs decided by claude/haiku (`bench/results`, config B).
`decisions calibrate` itself proposed no threshold change.

| | claude/opus (43) | claude/haiku (22) |
|---|---|---|
| size: min / q25 / median / q75 / max | 0.47 / 1.08 / 1.25 / 1.42 / 1.78 | 0.90 / 1.00 / 1.04 / 1.14 / 1.35 |
| interquartile range | 0.35 | 0.14 |
| levels under the policy of the day | 4 simple / 29 medium / 10 complex | 22 medium |
| r(planning, size) | **0.94** | 0.29 |
| r(review, size) | **0.83** | 0.04 |
| r(testing, size) | 0.75 | −0.37 |
| r(analysis, size) | 0.45 | 0.20 |
| mean P: analysis / review / testing / development | 0.78 / 0.83 / 0.88 / 0.95 | 0.83 / 0.86 / 0.89 / 0.98 |
| cuts for 50/35/15 (midpoints) | medium 1.24, complex 1.48 | 1.04 / 1.18 (noise) |

What it says:
- **Opus:** planning and review repeat size (§1, point 1, holds). Size spreads, and it is stable: the
  same task got the same size on every repeat (QL06 1.25 ×3, GV06 1.50 ×3, BM06 1.45 ×2), while
  its level flipped with the policy of the day.
- **Haiku** (`bench/configs/B.yaml`: `decision.model: haiku`): on the benchmark tasks size barely
  spreads and every stage is "yes" at nearly every run - a constant, not a halo. **The two columns
  are not a model comparison:** opus decided this repository's spec tasks, haiku the benchmark's,
  so the narrow spread may be the task set's as much as the model's. Comparing the models needs
  both on the same tasks (config B with `decision.model: opus` on the same set).
- **Size predicts fix iterations only weakly.** The share of runs with `fix_iterations > 0` is
  2/7 below 1.0, 5/14 in [1.0, 1.25), 6/16 in [1.25, 1.5) and 1/2 above. Size is good for
  sharing cost (which model a task gets), not for guessing which task will fail. That is the
  case for escalation on evidence (§8).
- The local runs say `failed` with no fix iteration in 8 cases, mostly environment and config
  failures. That is why *hard* (§7.3) does not count `failed`.

## 2. How comparable systems decide (summary)

From the author's knowledge, not a fresh survey:

- **Model routers** (RouteLLM, NotDiamond, Martian) score one number — "how much does this need
  the strong model" — and set the cut by a **target share of traffic** ("30% to the strong
  model"), calibrated on history. The spread across levels is guaranteed by construction.
- **Cascades** (FrugalGPT, AutoMix) predict little up front: a cheap model tries first, and a
  **verifier** escalates on failure. This orchestrator already has the verifier that matters:
  the test command's exit code and the review verdict.
- **Coding agents** (Agentless, SWE-agent, OpenHands, Aider, Claude Code) do not pick stages
  with a classifier. They run a fixed skeleton (localize → repair → validate), let the user
  pick a mode (plan mode, architect mode), and adapt through the test/fix loop. What a
  classifier does reliably is answer **factual** questions: is this a question or a change, is
  outside knowledge needed, is the place to change already known.

Taken together: one size number cut by quantiles, a fixed template per level, a few factual
questions, and escalation on a verified failure.

## 3. What already works

- **The score question** (`ComplexityScoreQuestion`) already returns a distribution over
  ordered levels; Jev also returns a `score` in `[0, 2]` (`ComplexityDecision.Score`).
- **`policy.stages.force[level]`** is already a per-level stage set, in the repository config
  and in the org layer (additions only, `org.example.yaml`).
- **`actions.<name>.by_complexity`** routes model and effort by level; `ProviderRef.ModelSource`
  records `base` or `by_complexity.<level>`; `decision.json: models` lists the routes.
- **`decisions calibrate`** (`internal/report/calibrate.go`) reads `decision.json` and
  `run.json`, groups by provider, model and `policy_hash`, and only proposes.
- **Outcomes per run**: `outcome.status`, `fix_iterations`, `tests_passed_final`,
  `review_verdict`, `usage.cost_usd`.
- **`policy.explore`** turns a stage just below its threshold on for a share of runs.
- **The bench** compares the chosen stage set with `expected_stages` and reports the
  complexity distribution per config (`bench/internal/summary`).

## 4. Principles

1. **One signal, one question.** Size is asked once. A stage question stays only if its answer
   is a fact about the task that size does not already carry.
2. **Levels are relative, and calibrated.** A cut is a quantile of past sizes for a target
   share, not a number guessed against a model's verbal scale. "Complex" means "the top share
   of this repository's tasks". The spec says so in the docs, not only here.
3. **Escalate on evidence, not on a guess.** A failed test or review, read from exit codes and
   the verdict, is what raises the model (DS06). Never a model's own opinion.
4. **Go decides, from config and the persisted run.** Every new rule is a pure function over the
   decision, the config and `run.json`, and is derivable on resume.
5. **Calibration only proposes.** `decisions calibrate` prints a diff; nothing writes config.
6. **Measure before changing.** DS01 runs on the decisions that exist today. If the size number
   does not spread, §5 alone does not help (§9).

---

## 5. Size and level (DS02)

### 5.1 The size number

For the primary complexity source (choice or score), Go computes

```
size = Σ i · P(level_i)  = P(medium) + 2 · P(complex)        in [0, 2]
```

from the probabilities, for every provider alike. Jev's own `score` is still recorded and never
used: one formula for every provider keeps sizes comparable. The shadow source gets its own
size and level, and `agree` compares levels as today.

### 5.2 Cuts

```yaml
policy:
  complexity:
    cuts: {medium: 0.60, complex: 1.15}   # size >= complex -> complex; >= medium -> medium; else simple
```

- Validation: `0 < medium < complex <= 2`. Both are inclusive, like every threshold.
- The old keys `policy.complexity.medium` and `.complex` are declared in the schema and rejected
  with a message that names `cuts` and this spec. A silent change of meaning under the same key
  is the one migration not allowed.
- The example values are `medium: 1.24, complex: 1.48`: the 50/35/15 quantiles of the 43
  claude/opus decisions of §1.1. They hold for that decision model only, and the example says so.

### 5.3 Record

`decision.json: complexity` gains `size` (per source and for the primary) and `cuts`. `reason`
reads `size 0.85 >= medium cut 0.60`. `policy_hash` covers `cuts`.

`decision.json` gains `questions_hash`: the sha256 of the shared questions as rendered
(`internal/decision/questions.go`). Sizes from different wordings are not comparable, and
calibration groups by it (§7.1).

## 6. Workflow shape (DS03, DS04)

### 6.1 Three questions instead of six

Kept, with their current texts and IDs, so the history of these three stays comparable:

| question | asks | effect |
|---|---|---|
| `stage_development` | must repository files change? | below threshold: answer-only (§6.3) |
| `stage_research` | is outside knowledge needed? | adds research |
| `stage_analysis` | is the cause or the place to change not yet known? | adds analysis |

Dropped: `stage_planning`, `stage_testing`, `stage_review`. Their answers were the size signal
(§1.1). Planning and review now come from the level; testing comes with development.

### 6.2 The rule

```
enabled = force[level]
        ∪ { s ∈ {research, analysis, development} : P(s) ≥ threshold(s) }
        ∪ { testing  if development is enabled }
if development is not enabled: remove testing and review (there is no diff to test or review)
if nothing is enabled: analysis (the existing fallback)
```

`policy.stages.force` keeps its key and its meaning — "stages on at this level" — and becomes
the template. The org layer's additions to it keep working unchanged.

```yaml
policy:
  stages:
    thresholds: {research: 0.81, analysis: 0.81, development: 0.81}
    force:
      medium:  [analysis, review]
      complex: [analysis, planning, review]
```

- `thresholds.planning`, `.testing` and `.review` are declared and rejected with a message: those
  stages are not asked any more.
- `force` may name any stage. A forced `development` is allowed, and is the operator's choice.
- Every outcome keeps a reason: `forced: complexity=medium`, `P 0.90 >= threshold 0.81`,
  `with development`, `dropped: no code change`. Planning, testing and review have no
  probability (`nil`, as for a manual decision).

### 6.3 What changes for a run

| task | before | after |
|---|---|---|
| simple change, place known | implement → test | the same |
| simple change, cause unknown | implement → test (analysis 0.75 < 0.81) | analyze → implement → test |
| medium change | usually implement → test (as simple) | analyze → implement → test → review |
| complex change | analyze → plan → implement → test → review | the same, plus research when asked |
| a question | analyze (fallback) | analyze (rule: no code change) |

### 6.4 What is lost

- **A task that asks only for a review or a test run** ("review my uncommitted changes")
  used to get `review` alone through `stage_review`. Now it gets analysis. `run --stage review`
  covers it, and §12 A3 records it.
- **Testing is no longer separable from development.** A documentation-only change runs the
  tests. That is cheap, and MVP.md §18 makes the test command the judge anyway.

### 6.5 Manual choice and exploration

- The manual choice after a provider failure (`policy.ApplyForced`) and the review-screen edit
  (`Decision.WithEdit`) are unchanged. The operator picks stages, and `force[level]` still
  applies.
- `policy.explore` keeps working on the stages that are asked (research, analysis,
  development). Planning, testing and review have no probability, so they are never explored.
  The existing raise/lower proposals for testing and review do not apply to runs with the new
  questions (§7.3).

## 7. Calibration (DS01, DS05)

### 7.1 Grouping

Size calibration groups by decision provider, decision model and `questions_hash`, **not** by
`policy_hash`. Size does not depend on the cuts, so applying a proposal must not reset the
history. Runs without `questions_hash` (before DS02) form one legacy group per model, and DS01
reads exactly that group.

### 7.2 Diagnosis (DS01): `decisions calibrate` prints the shape of the decisions

Read-only, over the decisions that exist today. Per group:
- a histogram of `size` (bins of 0.1), and its median and interquartile range;
- the share of each level under the current policy;
- for each of the six stage questions, Pearson's r between its probability and `size`, with
  r ≥ 0.8 marked `redundant with size`;
- the quantile cuts for the target shares (default `50/35/15`, `--size-target`).

This tells the owner whether §1 holds on their data, and gives DS02 its example cuts.

### 7.3 Cut proposals (DS05)

Two rules, both printed with their evidence. Neither writes anything.

1. **By target share.** The cuts are the `target` quantiles of the group's sizes; each cut is the
   midpoint between the two neighbouring sizes, moved to the nearest gap between different sizes
   when they are equal (DS01 "As built"). At least `--min-runs` (default 10) per proposed
   level.
2. **By outcome.** A run is *hard* when `fix_iterations > 0` or its status is `stopped` by a
   limit. `failed` is not counted: in the data of §1.1 it is mostly the environment. Per size
   bin, the report prints runs and the hard share. The medium cut is proposed down to the highest bin edge `e` for which runs below
   `e` have a hard share above `--simple-max-hard` (default 0.2), with `--min-runs` runs. If the
   hard share does not grow with size at all, the report says `size does not predict outcome`
   and proposes nothing by this rule.

**Refusal.** If the interquartile range of `size` is below 0.2, the report prints
`size does not separate tasks` and proposes no cut. Quantiles of noise would only split noise
(§9).

The old stage-threshold proposals stay, for groups that asked the six questions.

## 8. Escalation on a failed check (DS06)

```yaml
actions:
  implement:
    by_complexity: { ... }
    escalate: true          # default false
```

- With `escalate: true`, an `implement` that follows a failed `test` or a failing `review`
  (the last execution before it that is not an implement, read from `run.json`) routes as the **next** level up:
  simple → `by_complexity.medium`, medium → `by_complexity.complex`, complex stays. One step,
  however many fix iterations follow, so the cost stays predictable.
- `provider.model_source` records `escalated.<level>`.
- Never under `run --stage` (no level: the base route, as today).
- The provider never changes, as with `by_complexity`. `allowed_models`, `max_cost_usd` and the
  CLI version check already cover every `by_complexity` model.
- A resume derives the route from the persisted history, so it routes the same way.
- `decision.json: models` does not list it (DS06 "As built"): `run.json` records it per step.

This needs the owner's reading of MVP.md §31.1 (§11, O1): it is a config table keyed by the
attempt, as `by_complexity` is keyed by the level. It is not "best model" selection.

## 9. When this does not help

- If the decision model gives nearly the same distribution for every task, size does not spread
  and the cuts split noise. DS01 shows it, and §7.3 refuses. The next step is then a concrete
  size question ("how many files will change: 1 / 2–3 / 4–10 / more"), asked instead of the
  criteria question. It is not in this spec: it changes the wording, so it needs its own
  measurement.
- Escalation does not help a task that no model solves. It only moves the cost to where the
  check failed.

## 10. Persistence

| file | field | written | validated on load |
|---|---|---|---|
| decision.json | `complexity.size`, `complexity.sources.<src>.size` | with the decision | in `[0, 2]` |
| decision.json | `complexity.cuts` | with the decision (absent for `manual`) | `0 < medium < complex <= 2` |
| decision.json | `questions_hash` | with the decision (absent for `manual`) | 64 hex |
| decision.json | `stages[].probability` | as today | may be absent for planning, testing and review in a non-manual decision |
| run.json | `executions[].provider.model_source` | as today | also `escalated.<level>` |

Old runs load as before: six probabilities and no `size` are a legacy decision.

## 11. Owner's decisions

- **O1 (2026-10-13): yes.** Escalation (§8) is static routing by attempt under MVP.md §31.1:
  the next level's `by_complexity` route, from config and the persisted history.
- **O2 (2026-10-13): yes.** The default target shares are `50/35/15`.
- **O3 (2026-10-13): passed** on the data of §1.1. The gate was: at least two of planning,
  testing and review with r ≥ 0.8, or one level with more than 70% of the runs. Opus has
  planning 0.94 and review 0.83; haiku has 100% medium. DS01 is still built, so the check is
  repeatable.
- **Following from §1.1:** the example cuts are `medium: 1.24, complex: 1.48`, for an opus
  decision model on this repository's tasks; the example says so. The refusal threshold is an
  interquartile range of 0.2, so the benchmark group's 0.14 is refused. Whether that is haiku or
  the benchmark's tasks is open (§1.1).

## 12. Author's defaults (change before the task starts if wrong)

- A1: the size comes from the probabilities, never from Jev's `score` (§5.1).
- A2: thresholds for research, analysis and development keep their current values. No new data
  justifies moving them.
- A3: review-only and test-only tasks go through `run --stage` (§6.4).
- A4: escalation is one step, implement only, opt-in (§8).
- A5: both complexity questions (choice and score) stay. Dropping one is a separate wording
  change.
