# Benchmark summary

Based on 16 cells from 2 tasks × 4 configs × up to 2 reps: 8 pass, 6 fail, 2 infra_error (counted, left out of every rate and cost).
2 result lines replaced by a later line for the same cell (redo)

Baseline A · seed 1 · 10000 bootstrap resamples · a group under 10 tasks is marked insufficient data

## Overall (2 tasks × 2 reps)

| config | pass rate | cost/task mean | cost/task median | cost/solved | time/task | LLM calls | fix iterations | infra |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A | 100% (100–100) | $0.10 | $0.10 | $0.10 | 1s | 1.0 | 0.0 | 0 |
| B | 0% (0–0) | $0.10 | $0.10 | — | 2s | 3.0 | 0.0 | 0 |
| C | 50% (50–50) | $0.10 | $0.10 | $0.20 | 3s | 3.0 | 0.0 | 0 |
| D | 100% (100–100) | $0.10 | $0.10 | $0.10 | 4s | 3.0 | 0.0 | 2 |

## Failures by fail_reason

| config | fails | oracle | regression | answer | tree_changed | mean tests_reset |
| --- | --- | --- | --- | --- | --- | --- |
| A | 0 | 0 | 0 | 0 | 0 | 0.0 |
| B | 4 | 2 | 0 | 2 | 0 | 0.0 |
| C | 2 | 0 | 2 | 0 | 0 | 0.0 |
| D | 0 | 0 | 0 | 0 | 0 | 0.0 |

## By category

A group under 10 tasks is shown with its numbers and marked insufficient data: no conclusion from it.

### bugfix (1 task, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 100% (100–100) | $0.10 | 0 |
| B | 0% (0–0) | $0.10 | 0 |
| C | 0% (0–0) | $0.10 | 0 |
| D | — | — | 2 |

### question (1 task, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 100% (100–100) | $0.10 | 0 |
| B | 0% (0–0) | $0.10 | 0 |
| C | 100% (100–100) | $0.10 | 0 |
| D | 100% (100–100) | $0.10 | 0 |

## By language

A group under 10 tasks is shown with its numbers and marked insufficient data: no conclusion from it.

### sh (2 tasks, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 100% (100–100) | $0.10 | 0 |
| B | 0% (0–0) | $0.10 | 0 |
| C | 50% (50–50) | $0.10 | 0 |
| D | 100% (100–100) | $0.10 | 2 |

## Paired vs A (per task, majority over reps)

A task counts when both configs have a result for it. Difference of pass rates in percentage points, with a 95% percentile-bootstrap interval over tasks (10000 resamples, seed 1).

| config | tasks | wins | ties | losses | pass-rate difference | 95% CI | tasks without A | tasks without config |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| B | 2 | 0 | 0 | 2 | −100 pp | [−100, −100] | 0 | 0 |
| C | 2 | 0 | 1 | 1 | −50 pp | [−100, 0] | 0 | 0 |
| D | 1 | 0 | 1 | 0 | 0 pp | [0, 0] | 0 | 1 |

## Decision overhead (B)

10% of cost, 25% of time; 2.0 decision calls per run (4 cells)

## B vs C cost

B $0.10 per cell, C $0.10 per cell: B costs 1.00x C. Paired over 2 tasks: B − C = +$0.00 per task.

## Stage choice (B, Bmock) vs expected_stages

The chosen stages against expected_stages as sets, over the cells that have expected_stages.

### B

no cell has expected_stages

## Complexity (B, Bmock)

| config | complexity | cells | pass rate |
| --- | --- | --- | --- |
| B | medium | 4 | 0% |

## Translation (en/ru)

no complete en/ru pair
