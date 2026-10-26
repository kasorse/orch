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
