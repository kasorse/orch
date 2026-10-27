# Decision B4 — go / pivot (2026-10-26)

The rule was fixed before the data (`specs/benchmark/SPEC.md` §12, Q9). This file applies it to
the second round (`round2`) and records what follows for TODO.md (BM17).

Signed off: — (the owner)

## The data

- **Round 2**, run 2026-10-20 … 2026-10-24 in the container: 31 tasks (14 Go from this
  repository, 9 Python, 8 Node; 18 bugfix, 10 feature, 3 question), configs A–D, 3 reps,
  372 cells, 0 `infra_error`. claude 2.1.287, sonnet for the code, a subscription token.
  About $92 at list prices (A $18.5, B $29.8, C $26.1, D $17.6).
- Configs (`bench/configs/`, the hashes are in the round's `manifest.json`): **A** bare
  claude with a shell; **B** the owner's config (decision on opus, `by_complexity`,
  `escalate`); **C** the defaults of `orchestrator.example.yaml`; **D** implement + test only.
- The summary is [`results/2026-10-24-round2/summary.md`](results/2026-10-24-round2/summary.md);
  the per-cell results and the tasks from the private repository stay outside git.
- **Before clarify.** The round ran on the workflow with `analysis` and `planning`. `specs/clarify`
  (2026-10-25) replaced them with the clarify step and `investigation`, so B and C as they are
  now have not been measured.

## The numbers the rule needs

| config | pass rate | cost/task | time/task | vs A, pp [95% CI] |
|---|---|---|---|---|
| A | 88% | $0.20 | 1m16s | — |
| B | 90% | $0.32 | 2m14s | +2 [−2, +8] |
| C | 91% | $0.28 | 2m27s | +3 [−2, +11] |
| D | 80% | $0.19 | 1m51s | −9 [−22, +2] |

- B vs C cost: B is 1.14 times C (+$0.04 per task, paired).
- B′ (auto with `jev.mock`): not run. The config was dropped on 2026-10-20 (TODO.md, phase 1).
- en/ru pairs: none in the set.
- Stage choice of B against `expected_stages`: exact in 18 of 93 cells; recall of analysis
  0.11, of planning 0.14, of review 0.14; development and testing 1.00.

## Verdict

**B is not "not worse than A".** The lower bound of the interval (−2 pp) clears −5 pp, but
B's mean cost ($0.32) is above A's ($0.20). By §12 this is the second branch: **the value is in
the gates and the governance, not in the adaptive choice of stages.** The decision layer gets
simpler and the effort goes to phases 2–3. G5 (calibration) and Q3 (complexity → model) are
not the next steps.

In pass rate, A, B and C cannot be told apart on this set. The orchestrator does not lose to
bare Claude Code without a shell, and it does not win either.

## What the overall numbers hide

- **B's extra cost is one task.** On `ai-orchestrator-52e2eec` (feature) B chose `complex`:
  planning and review on opus, implement on sonnet through `by_complexity.complex`. That cost
  $3.86 per cell against A's $0.74, and it failed in all three reps, like every config did.
  Without that task B costs $0.20 per task and A $0.18. The `complex` route spent five times
  as much and gained nothing. That rests on one task, but nothing in the round argues for it.
- **D loses only on the questions.** 9 of its 19 failures are `answer`: D has no read-only
  path, so it scores 0% on questions. On the 28 code tasks D passes 74 of 84 cells, against
  73 for A, B 75 and C 76, at the lowest cost ($0.197 against A's $0.205). The one job the
  decision visibly earns its keep on is telling a question from a change.
- **Complexity hardly varies.** B called 90 of 93 cells `simple`, C 90 `simple` and 3 `medium`.
  `by_complexity` and the stage templates almost never fired, so this round says little about
  them either way.
- **The extra stages buy nothing.** C ran analysis in 52 code cells and gained 1 pp over B,
  inside the noise. `expected_stages` are labels, not proof that a stage is needed: a low recall
  against them is not a loss.
- **The set separates little.** Two tasks (`52e2eec`, `graphql-js-4c9d754`) fail in every
  config, 25 pass in nearly every cell, and the differences come from 4 tasks. A next round needs
  harder tasks before it can show a gain.
- **Regressions are rare:** one `regression` failure in all 372 cells (A).
- **Decision overhead is small:** 9% of B's cost and 5% of its time, 2.0 decision calls per run
  (context depth + classification; every task was English, so translation never ran).

## Consequences for TODO.md

1. **Phase 3 (delivery) is next.** The verdict puts the value in what the orchestrator adds
   around the model: gates, commit, MR, governance. `specs/delivery` is that work.
2. **Q8: simplify the decision.** The default becomes one cheap classification: context depth
   folded into it or turned off, still with the question-vs-change distinction, which is what
   the round shows the decision is good for. `complex` and its `by_complexity` route stay opt-in,
   not defaults. Q8 changes `questions.go`, which means a recalibration (CLAUDE.md).
3. **Translation stays as it is.** Without an en/ru pair there is no data to turn it off.
4. **Next run: E alone, added to round 2, before Q8 changes a default.** E (`configs/E.yaml`) is
   the pipeline as the owner runs it now: B plus the clarify step, `verify`, the graphify MCP
   server (the config's `setup` builds the graph offline with `graphify update .`), a fix loop of
   3 and no pre-run test. The build also brings the replace tool, the parallel-calls rule
   (2026-10-25) and `investigation` in place of analysis and planning. The tasks are the same
   and A does not depend on the orchestrator's build, so round 2's cells are the reference:

   ```
   orchbench run --tasks '/tasks/*.yaml' --out /results/round2 --configs E --reps 3 --allow-mixed
   orchbench summarize /results/round2
   ```

   `--allow-mixed` is needed because the orchestrator binary differs; `manifest.json: mixed`
   records it. `ai-orchestrator-27b3ee5` is out of the set (2026-10-26): its suite hit the
   repository's `test/integration` timeout flake in both E pilots, once as the final check's
   `regression`. Its round-2 cells are removed from `round2/results.jsonl` (a copy is kept), so
   E and A–D are compared on the same 30 tasks; without it round 2 reads A 88%, B 90%, C 91%,
   D 79%, the differences to A as above. E against A answers whether the current pipeline beats bare Claude Code; E against
   round 2's B answers what the work since round 2 adds, as one bundle. The run does not split
   the build's share from the config's; that needs B, C and D on the new build, and nobody needs
   that split yet. Two conditions: the claude CLI in the image stays 2.1.287 (another version
   would make A no reference), and optionally A is run again on 5–6 tasks into a new `--out` to
   rule out a change of the models behind the aliases.
5. **Round 3, a new full round, once the set has grown:** en/ru pairs (the translation question
   of Q8), the questions up to 6, harder tasks (today 25 of 31 pass nearly everywhere). Every
   config on one build.
6. Q2 (verify) was not on in any round-2 config; the E run measures it, inside the bundle. Q3's
   code is done; this round measured its `complex` route only on the one task above.
7. G5 and P3 (presets) wait for round 3: there is nothing to calibrate while complexity is
   `simple` almost everywhere.

## The E run (2026-10-26 … 27)

E ran alone into `round2` as planned: 30 tasks × 3 reps, 90 cells, 0 `infra_error`, one container
from 2026-10-26 22:01Z to 2026-10-27 01:35Z (orchestrator at 319fc92, claude 2.1.287). The
summary with all five configs is
[`results/2026-10-27-round2-e/summary.md`](results/2026-10-27-round2-e/summary.md).

| config | pass rate | cost/task | time/task | vs A, pp [95% CI] |
|---|---|---|---|---|
| A | 88% | $0.20 | 1m17s | — |
| B | 90% | $0.33 | 2m15s | +2 [−2, +8] |
| **E** | **86%** | **$0.31** | **2m43s** | **−2 [−10, +6]** |

**E does not beat A, and it does not beat round 2's B.** By the §12 rule it stays in the
second branch: the bundle (clarify, verify, graphify, the fix loop of 3) does not buy pass
rate on this set, and it costs about as much as B.

What the numbers are made of:

- **5 of E's 13 failures are `regression`, all on the Go tasks of this repository, and all of
  them are the `test/integration` flake**, not a change the model made: `context deadline
  exceeded` in a scenario's 30 s run timeout, fake CLIs that could not be spawned, `WaitDelay
  expired`, a timing assertion (scenario 13). In each of the five the run's own `test` step had
  passed on the same tree (status `completed`, 0 fix iterations). In two more cells the flake
  hit the run's `test` step: implement rightly changed nothing, the run stopped `no progress`,
  and the final check passed. A–D had one `regression` in 360 cells.
- **The Go suites ran 1.5–2.6 times slower under E** than under A and B on the same tasks
  (`test/integration`: 7740c60 120 s against 56 s, 52e2eec 119 s against 46 s); Python and Node
  suites ran at the same speed. The code graph in the tree is not the cause: the suite of
  7740c60 takes 51 s with and without `graphify-out` in the same container. graphify-mcp exits
  when its stdin closes, so no server outlives a session. The cause is not found; the run was on
  the owner's laptop at night, next to other work, and the flake is timing-bound.
- **Without the flake E would be at 91%** (82 of 90) and +3 pp over A paired, inside the noise
  either way. The real failures are the two tasks every config fails (`52e2eec`,
  `graphql-js-4c9d754`) and `sqlglot-5b7b413` (E 1 of 3, A 2 of 3).
- **Cost: E $0.31 against B $0.33.** Without `52e2eec`, E costs $0.22 per task, B $0.21 and A
  $0.18. The `complex` route on that task cost $2.97 per cell (B $3.86, A $0.74) and failed
  again. The decision cost $0.026 per cell (2.0 calls), as in B.
- **Clarify never ran:** the first clarity check was clear in all 90 cells, so its cost and its
  effect are both zero on this set. Every task here is a well-formed commit message; a set
  without unclear tasks cannot measure clarify.
- **graphify was used in every step after the hint change:** one `query_graph` per implement
  (plus 0.1 `get_node`), 1.0–2.0 graph calls per investigate and review. It did not replace the
  search: implement still made 3.1 Grep and 4.6 Read calls. No `mcp_failed` in any cell.
- **verify: 1.2 `run_tests` calls per implement, 0.1 `replace`.** Fix iterations fell from 0.2
  (B) to 0.1, with the fix loop raised to 3; the gain does not reach the pass rate.
- **Stage choice:** the three questions went to `investigation` alone (9 of 9, all passed);
  `52e2eec` was `complex` again (investigation + development + testing + review); everything else
  development + testing.

Consequences, on top of the ones above:

8. **Q2 (verify) is measured and shows no gain** on this set, inside the bundle. It stays
   because it costs little (0.1 fix iterations fewer, no extra failure), not because the
   benchmark proved it.
9. **The flake must go before round 3.** It hides the difference the round is meant to show:
   five E cells lost to it. Either the `test/integration` timeouts of the task snapshots stop
   depending on the machine's load (a task setup that raises them, as the run timeout of the
   snapshot cannot be changed in the past), or the round runs on a quiet machine and records its
   load.
10. **Round 3 needs unclear tasks** for clarify, besides the en/ru pairs and harder tasks.
11. **Q8 is not blocked by E:** the decision costs the same in E as in B, and nothing in the run
    argues against the simpler default.
