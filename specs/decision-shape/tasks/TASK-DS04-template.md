# DS04 — The stage rule: `force` as the template

**Size** M (2 d) · **Depends on** DS03 · **Blocks** DS07

## Goal

Stages = `force[level]` ∪ the asked stages over their threshold ∪ testing with development;
without development, testing and review are dropped (`SPEC.md` §6.2).

## Files

- `internal/policy/engine.go`: `SelectStages` implements §6.2; reasons
  `forced: complexity=…`, `P … >= threshold …`, `with development`, `dropped: no code change`;
  outcomes for planning, testing and review carry no probability or threshold
- `internal/config/decision.go`: `stages.thresholds` accepts research, analysis and development;
  planning, testing and review declared and rejected with a message
- `internal/policy/explore.go`: skip stages with no probability
- `internal/logging/review.go`, `internal/interaction`: the review screen and the manual choice
  print `—` for a stage with no probability (as for `manual`)
- `orchestrator.example.yaml`: `force.medium: [analysis, review]` and the new comment

## Steps

1. The rule and its reasons.
2. Config keys and validation.
3. Check the org layer's `policy.stages.force` additions still merge into the template.

## Acceptance

- Unit, one per row of `SPEC.md` §6.3, plus: a forced review with P(development) below the
  threshold is dropped with `dropped: no code change`; nothing enabled → analysis.
- `ApplyForced` and `WithEdit` behave as before.
- The same decision always gives the same workflow.

## Gates

`scripts/check.sh`

## As built (2026-10-13)

Built with DS03 (see its "As built" for why). `scripts/check.sh` passed on 2026-10-13.

- **`SelectStages`** walks `StageOrder`, takes the asked stages' probabilities against their
  thresholds, adds `ForceStages[level]`, then turns testing on with development and testing and
  review off without it. Reasons: `p=… >= …`, `forced: complexity=…`, `with development`,
  `dropped: no code change`, and `not asked, not in the template` for a stage that is neither.
  A dropped review keeps `forced: true` with `enabled: false`, as an excluded forced stage does.
- **`policy.Explore` never explores development** (a new rule, not in the plan): turning
  development on after the policy ran would skip "testing with development". Its threshold is
  never calibrated anyway (governance SPEC §7.1).
- **Config:** `stages.thresholds` needs research, analysis and development; planning, testing
  and review are refused with a message naming `force`. The org layer's `force` additions are
  unchanged and merge into the template as before.
- **Templates:** `force.medium: [analysis, review]` in the example, the bench configs, their
  goldens, `bench/docker/B.container.yaml`, `B2.yaml` and the integration config.
- **Not changed:** `ApplyForced` and `WithEdit` (the manual choice and the review-screen edit).
  A stage picked by hand stays as picked, so a manual choice of development without testing is
  still possible there; the operator sees it.
- **MVP.md** got only the three-stage `thresholds` line in its §9 example (the regex that
  rewrote every config caught it); the rest of the text stays for DS07.
