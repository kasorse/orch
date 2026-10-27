# Benchmark summary

Based on 372 cells from 31 tasks × 4 configs × up to 3 reps: 325 pass, 47 fail, 0 infra_error (counted, left out of every rate and cost).

Baseline A · seed 1 · 10000 bootstrap resamples · a group under 10 tasks is marked insufficient data

## Overall (31 tasks × 3 reps)

| config | pass rate | cost/task mean | cost/task median | cost/solved | time/task | LLM calls | fix iterations | infra |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A | 88% (87–90) | $0.20 | $0.17 | $0.23 | 1m16s | 1.0 | 0.0 | 0 |
| B | 90% (87–94) | $0.32 | $0.18 | $0.35 | 2m14s | 3.2 | 0.2 | 0 |
| C | 91% (90–94) | $0.28 | $0.27 | $0.31 | 2m27s | 3.8 | 0.3 | 0 |
| D | 80% (77–81) | $0.19 | $0.16 | $0.24 | 1m51s | 1.2 | 0.2 | 0 |

## Failures by fail_reason

| config | fails | oracle | regression | answer | tree_changed | mean tests_reset |
| --- | --- | --- | --- | --- | --- | --- |
| A | 11 | 10 | 1 | 0 | 0 | 15.5 |
| B | 9 | 9 | 0 | 0 | 0 | 15.8 |
| C | 8 | 8 | 0 | 0 | 0 | 16.0 |
| D | 19 | 10 | 0 | 9 | 0 | 15.7 |

## By category

A group under 10 tasks is shown with its numbers and marked insufficient data: no conclusion from it.

### bugfix (18 tasks)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 89% (89–89) | $0.16 | 0 |
| B | 89% (83–94) | $0.19 | 0 |
| C | 93% (89–94) | $0.25 | 0 |
| D | 87% (83–89) | $0.15 | 0 |

### feature (10 tasks)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 83% (80–90) | $0.28 | 0 |
| B | 90% (90–90) | $0.61 | 0 |
| C | 87% (80–90) | $0.37 | 0 |
| D | 90% (90–90) | $0.27 | 0 |

### question (3 tasks, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 100% (100–100) | $0.14 | 0 |
| B | 100% (100–100) | $0.15 | 0 |
| C | 100% (100–100) | $0.16 | 0 |
| D | 0% (0–0) | $0.11 | 0 |

## By language

A group under 10 tasks is shown with its numbers and marked insufficient data: no conclusion from it.

### go (14 tasks)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 88% (86–93) | $0.26 | 0 |
| B | 93% (93–93) | $0.49 | 0 |
| C | 90% (86–93) | $0.34 | 0 |
| D | 86% (86–86) | $0.23 | 0 |

### node (8 tasks, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 88% (88–88) | $0.11 | 0 |
| B | 83% (75–88) | $0.12 | 0 |
| C | 88% (88–88) | $0.19 | 0 |
| D | 71% (62–75) | $0.11 | 0 |

### python (9 tasks, insufficient data)

| config | pass rate | cost/task mean | infra |
| --- | --- | --- | --- |
| A | 89% (89–89) | $0.19 | 0 |
| B | 93% (89–100) | $0.23 | 0 |
| C | 96% (89–100) | $0.28 | 0 |
| D | 78% (67–89) | $0.19 | 0 |

## Paired vs A (per task, majority over reps)

A task counts when both configs have a result for it. Difference of pass rates in percentage points, with a 95% percentile-bootstrap interval over tasks (10000 resamples, seed 1).

| config | tasks | wins | ties | losses | pass-rate difference | 95% CI | tasks without A | tasks without config |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| B | 31 | 2 | 29 | 0 | +2 pp | [−2, +8] | 0 | 0 |
| C | 31 | 2 | 29 | 0 | +3 pp | [−2, +11] | 0 | 0 |
| D | 31 | 2 | 25 | 4 | −9 pp | [−22, +2] | 0 | 0 |

## Decision overhead (B)

9% of cost, 5% of time; 2.0 decision calls per run (93 cells)

## B vs C cost

B $0.32 per cell, C $0.28 per cell: B costs 1.14x C. Paired over 31 tasks: B − C = +$0.04 per task.

## Stage choice (B, C) vs expected_stages

The chosen stages against expected_stages as sets, over the cells that have expected_stages.

### B

exact match 18/93 cells

| stage | TP | FP | FN | precision | recall |
| --- | --- | --- | --- | --- | --- |
| analysis | 9 | 0 | 72 | 1.00 | 0.11 |
| planning | 3 | 0 | 18 | 1.00 | 0.14 |
| development | 84 | 0 | 0 | 1.00 | 1.00 |
| testing | 84 | 0 | 0 | 1.00 | 1.00 |
| review | 3 | 0 | 18 | 1.00 | 0.14 |

### C

exact match 46/93 cells

| stage | TP | FP | FN | precision | recall |
| --- | --- | --- | --- | --- | --- |
| analysis | 56 | 7 | 25 | 0.89 | 0.69 |
| planning | 0 | 0 | 21 | — | 0.00 |
| development | 84 | 0 | 0 | 1.00 | 1.00 |
| testing | 84 | 0 | 0 | 1.00 | 1.00 |
| review | 0 | 2 | 21 | 0.00 | 0.00 |

## Complexity (B, C)

| config | complexity | cells | pass rate |
| --- | --- | --- | --- |
| B | simple | 90 | 93% |
| B | complex | 3 | 0% |
| C | simple | 90 | 91% |
| C | medium | 3 | 100% |

## Translation (en/ru)

no complete en/ru pair
