# DS07 — Integration scenarios and documentation

**Size** M (2 d) · **Depends on** DS02–DS06

## Goal

End-to-end coverage with the fake executables and the fake Jev, and the docs that make MVP.md
the normative home.

## Scenarios (from 57)

- 57: a simple size with P(analysis) high → analyze → implement → test.
- 58: a medium size → analyze → implement → test → review, with the medium model.
- 59: P(development) low → analyze only, final answer printed.
- 60: a config with `policy.complexity.medium` or `stages.thresholds.review` fails before any
  call, naming the replacement.
- 61: `escalate: true`, the first test fails → the second implement records
  `escalated.medium` and the medium model's argv (`FAKE_ARGS_DIR`); a resume in between routes
  the same.
- 62: `decisions calibrate` over prepared run directories prints the shape and a cut proposal.

## Documentation

- MVP.md §9 (the rule), §9.1 (the example values and where they came from), §14 (size), §31.1
  (per O1), §44.8 (escalation); a pointer to `specs/decision-shape/`.
- CLAUDE.md: the "Policy rules" paragraph and the spec list.
- README: the policy section.
- `orchestrator.example.yaml`, `org.example.yaml` (the force comment), `bench/configs/*.yaml`
  (new keys; bench is a separate module, run its gates).
- TODO.md: an entry for this spec, ticked.

## Gates

`scripts/check.sh`, root and `bench/`, outside the sandbox.
