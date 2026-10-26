# BM16 — Pilot, then the full run

**Size** M (mostly waiting; 2–4 days of wall time) · **TODO** B2 · **Depends on** BM13,
BM14, BM15 · **Blocks** BM17

## Goal

B2's "done when": one command runs 30 tasks × the configs (A, B, C, D and B′ — Q10) × 3
repetitions and produces the summary (`SPEC.md` §13.10–§13.11).

## Steps

1. **Pilot** in the container: 5 tasks across languages and categories (incl. one question
   and one ru pair), configs A, B, C, D (+ B′ — Q10), 1 rep, `--max-cost` from a rough guess.
   Fix what breaks (infra errors are bugs in setup, tasks or the runner, not results).
2. From the pilot: cost and time per cell per config → the budget for the full run (`--max-cost`
   with a 30% margin), agreed with the owner before starting.
3. **Full run**: 30 × 5 × 3, sequential (or `-j` if the rate limits seen in the pilot allow).
   Re-run until no `infra_error` is left.
4. `orchbench summarize`; commit `bench/results/<date>-full/summary.md` (only it).
5. `orchestrator runs report --dir <out>/runs` attached to the summary as an appendix.

## Acceptance

- `summary.md` with every section of §10, based on all cells, no `infra_error` left (or each
  remaining one explained).

## Gates

None beyond the commands above; the money spent is recorded in the summary.

## Progress (2026-10-12): the first round

Not the run this task asks for: 8 tasks, not 30 (BM15 is not done), all Go, from one private
repository (this one; mined with `orchbench mine`, 8 kept of 32 drafts), no question, no
en/ru pair, no B′. It ran the whole pipeline for real and answered the stage-threshold
question; the task stays open.

**Setup.** The tasks, a bare mirror of the repository (`repo: /tasks/ai_orchestrator.git`,
since the container has no SSH agent) and the results live outside the repository's tracked
files, as private tasks must. Container image from `36ef43b`; claude 2.1.287; sonnet for the
code, haiku for the decision. A claude subscription instead of an API key: `run.sh` passes
`CLAUDE_CODE_OAUTH_TOKEN` (`claude setup-token`). The Go module cache is the tasks'
`sandbox.rw_binds: [~/.cache/go]`, a directory made once on the cache volume; without it the
bind is skipped and the orchestrator's `validate` refuses the config.

**Results**, 7 tasks × 3 reps per config (the eighth, `d7403e6`, ran into the subscription's
limit; see below):

| config | pass | cost per cell | time per cell |
|---|---|---|---|
| A (claude with a shell) | 21/21 | $0.20 | 57 s |
| B (auto) | 20/21 | $0.40 | 2 m 56 s |
| C (all six stages) | 21/21 | $0.49 | 3 m 11 s |
| D (development + testing) | 21/21 | $0.17 | 1 m 40 s |

B's one failure is the test-name collision below, not a wrong solution. On these tasks the
configs do not differ in pass rate; they differ in cost, and the extra stages buy nothing.
B's decision (claude/haiku) called every task `medium` and enabled analysis and review in
every run, planning in about half (0.10–0.85 on the same task across reps): the thresholds were
raised (MVP.md §9.1).

**Found, and fixed the same day:**

- **The final check left the model's new test files in place.** `ResetTests` restored the
  test files of `base_commit` and wrote the oracle's, but a test file the model added stayed.
  Twice B wrote its own `TestCommitImplementation` / `TestScenario31_AutoCommit` in a new file,
  the oracle file declared the same name, and the package no longer compiled: a fail with a
  correct solution. Configs with review touch tests more often (mean `tests_reset` B 2.1, C 1.8,
  A 1.1, D 1.0), so this biased against them. Now a test file that is neither at `base_commit`
  nor present after `setup_command` is removed before the oracle is written (SPEC.md §7.5,
  `check.TestFiles`, `check.ResetTests`).
- **A subscription's usage limit was recorded as `fail`, not `infra_error`.** claude answers
  `You've hit your session limit · resets …`; the runner's rate-limit patterns knew "usage" and
  "rate" limits only, so A, C and D cells ended as `fail` with $0 (B as `infra_error`, its
  decision call failing first), and the same `run` command skipped them. The pattern now takes
  any `hit your … limit`: such cells are `infra_error`, retried by the same command. The run
  does not stop at a limit: each remaining cell fails in seconds at $0, which costs nothing.
- **The subscription's limit is the real budget**, not `--max-cost`: 40–55 cells fit in one
  window here (B and C cells on the larger tasks take 3–5 minutes each).

**Left:** the 30 tasks (BM15), B′, `d7403e6` and the round again with the
new thresholds on a new `--out` (the B config's hash changed).

## As built (2026-10-24): round 2

The full run, `round2`, on 2026-10-20 … 2026-10-24: 31 tasks × A, B, C, D × 3 reps, 372 cells,
0 `infra_error`, about $92. B′ was not run: the config was dropped on 2026-10-20 together with
E–H (TODO.md, phase 1). B is the owner's config and C the example's defaults, so the round
measures configurations as they are used, not one stage setting at a time. The summary is
committed as `bench/results/2026-10-24-round2/summary.md`, and the per-cell results stay in the
private directory. The `runs report` appendix (step 5) was not made. The round ran before
`specs/clarify`, on the workflow with `analysis` and `planning`.
