# Benchmark summary

Based on 31 cells from 4 tasks × 4 configs × up to 2 reps: 14 pass, 16 fail, 1 infra_error (counted, left out of every rate and cost).
partial: 31 of 32 cells, 1 missing from the grid of tasks × configs × reps

Baseline A · seed 1 · 10000 bootstrap resamples · a group under 3 tasks is marked insufficient data

## Overall (4 tasks × 2 reps)

| config | pass rate | cost/task mean | cost/task median | cost/solved | time/task | LLM calls | fix iterations | infra |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A | 25% (0–50) | $0.60 | $0.50 | $2.40 | 2m00s | 1.0 | 0.0 | 0 |
| B | 67% (33–100) | $1.00 | $1.00 | $1.40 | 5m00s | 5.0 | 0.4 | 1 |
| Bmock | 25% (0–50) | $0.60 | $0.60 | $2.40 | 3m00s | 3.0 | 0.0 | 0 |
| C | 67% (33–100) | $2.00 | $2.00 | $2.80 | 15m00s | 8.0 | 0.3 | 0 |

## Failures by fail_reason

| config | fails | oracle | regression | answer | tree_changed | mean tests_reset |
| --- | --- | --- | --- | --- | --- | --- |
| A | 6 | 2 | 2 | 1 | 1 | 0.4 |
| B | 2 | 1 | 0 | 1 | 0 | 0.1 |
| Bmock | 6 | 2 | 2 | 1 | 1 | 0.1 |
| C | 2 | 0 | 1 | 0 | 1 | 0.0 |

## By category

A group under 3 tasks is shown with its numbers and marked insufficient data: no conclusion from it.

### bugfix (2 tasks, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 25% (0–50) | $0.50 | 0 |
| B | 50% (0–100) | $1.00 | 1 |
| Bmock | 25% (0–50) | $0.60 | 0 |
| C | 75% (50–100) | $2.00 | 0 |

### feature (1 task, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 50% (0–100) | $0.90 | 0 |
| B | 100% (100–100) | $1.00 | 0 |
| Bmock | 50% (0–100) | $0.60 | 0 |
| C | 100% (100–100) | $2.00 | 0 |

### question (1 task, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 0% (0–0) | $0.50 | 0 |
| B | 50% (0–100) | $1.00 | 0 |
| Bmock | 0% (0–0) | $0.60 | 0 |
| C | 50% (0–100) | $2.00 | 0 |

## By language

A group under 3 tasks is shown with its numbers and marked insufficient data: no conclusion from it.

### go (3 tasks)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 33% (0–67) | $0.63 | 0 |
| B | 75% (50–100) | $1.00 | 1 |
| Bmock | 33% (0–67) | $0.60 | 0 |
| C | 75% (50–100) | $2.00 | 0 |

### python (1 task, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 0% (0–0) | $0.50 | 0 |
| B | 50% (0–100) | $1.00 | 0 |
| Bmock | 0% (0–0) | $0.60 | 0 |
| C | 50% (0–100) | $2.00 | 0 |

## Paired vs A (per task, majority over reps)

A task counts when both configs have a result for it. Difference of pass rates in percentage points, with a 95% percentile-bootstrap interval over tasks (10000 resamples, seed 1).

| config | tasks | wins | ties | losses | pass-rate difference | 95% CI | tasks without A | tasks without config |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| B | 4 | 2 | 2 | 0 | +50 pp | [+50, +50] | 0 | 0 |
| Bmock | 4 | 0 | 4 | 0 | 0 pp | [0, 0] | 0 | 0 |
| C | 4 | 2 | 2 | 0 | +50 pp | [+50, +50] | 0 | 0 |

## Decision overhead (B)

10% of cost, 15% of time; 3.0 decision calls per run (7 cells)

## B vs C cost

B $1.00 per cell, C $2.00 per cell: B costs 0.50x C. Paired over 4 tasks: B − C = −$1.00 per task.

## Stage choice (B, Bmock) vs expected_stages

The chosen stages against expected_stages as sets, over the cells that have expected_stages.

### B

exact match 4/7 cells

| stage | TP | FP | FN | precision | recall |
| --- | --- | --- | --- | --- | --- |
| research | 1 | 0 | 1 | 1.00 | 0.50 |
| analysis | 4 | 0 | 1 | 1.00 | 0.80 |
| development | 5 | 0 | 0 | 1.00 | 1.00 |
| testing | 5 | 0 | 0 | 1.00 | 1.00 |
| review | 0 | 1 | 0 | 0.00 | — |

### Bmock

exact match 2/8 cells

| stage | TP | FP | FN | precision | recall |
| --- | --- | --- | --- | --- | --- |
| research | 0 | 0 | 2 | — | 0.00 |
| analysis | 2 | 0 | 4 | 1.00 | 0.33 |
| development | 6 | 0 | 0 | 1.00 | 1.00 |
| testing | 6 | 0 | 0 | 1.00 | 1.00 |

## Complexity (B, Bmock)

| config | complexity | cells | pass rate |
| --- | --- | --- | --- |
| B | simple | 4 | 75% |
| B | medium | 3 | 67% |
| Bmock | simple | 4 | 25% |
| Bmock | medium | 4 | 25% |

## Translation (en/ru)

| pair (en / ru) | B stages en | B stages ru | same stages | A both passed | B both passed | Bmock both passed | C both passed |
| --- | --- | --- | --- | --- | --- | --- | --- |
| go-retry / go-retry-ru | analysis,development,testing | analysis,development,testing,review | no | no | no | no | no |

same stages 0/1 pairs; both passed: A 0/1, B 0/1, Bmock 0/1, C 0/1
