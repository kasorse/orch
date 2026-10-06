# BM17 — `bench/DECISION.md`

**Size** S (0.5 d) · **TODO** B4 · **Depends on** BM16 · **Blocks** BM18, TODO phase 2 order

## Goal

Write down the go / pivot decision by the rule fixed before the data (`SPEC.md` §12, Q9).

## Steps

1. Copy the numbers the rule needs from `summary.md`: B vs A pass-rate difference and its
   95% interval, mean cost of B and A; B vs C cost; B′ vs B; the en/ru pairs; stage-choice
   precision/recall.
2. Apply §12:
   - **B not worse than A** (interval's lower bound ≥ −5 pp **and** cost B ≤ cost A) → the
     adaptive pipeline is a selling point; next G5 (calibration) and Q3 (complexity → model).
   - **Otherwise** → the value is in gates and governance; the decision layer is simplified
     (default one cheap classification or `--stage`); effort to phases 2–3.
   - Translation and the context-depth round: keep or turn off by default (Q8) by the pair
     and B′ data.
3. Also record what surprised: per-category or per-language results that go against the
   overall one, failure reasons (`regression` share), the decision overhead.
4. Update TODO.md: B4 ticked, the next phase's order adjusted by the decision.

## Acceptance

- `bench/DECISION.md` names the rule, the numbers, the verdict and the consequences for
  TODO.md; the owner signs off.
