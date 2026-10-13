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
