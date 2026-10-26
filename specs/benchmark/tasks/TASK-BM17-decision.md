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

## As built (2026-10-26)

`bench/DECISION.md`: the second branch of §12. B is not worse than A in pass rate (+2 pp,
[−2, +8]), but it costs more ($0.32 against $0.20), so the value is in the gates and the
governance. TODO.md now has B4 ticked, phase 3 next, and Q8 raised and moved after a run of the
new config E (the current pipeline, clarify included) added to `round2`. Without B′ and without an en/ru pair, the translation and context-depth
questions have no data: translation stays as it is, and context depth goes into Q8's single
classification. The owner's sign-off line is still empty.
