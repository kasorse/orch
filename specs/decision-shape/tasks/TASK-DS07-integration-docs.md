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

## As built (2026-10-14)

Built as planned, except as listed. `scripts/check.sh` (root and `bench/`) passed on
2026-10-14, run outside the bwrap sandbox, after rebuilding `bin/orchestrator`.

- **Scenarios 57-62** are one file, `test/integration/scenario57_decision_shape_test.go`; the
  integration config gained an `escalate` option. 57: simple with P(analysis) high → analyze,
  implement, test, testing `with development`. 58: medium with P(analysis) low → the template's
  analysis and review, implement on the medium model. 59: P(development) low on a medium size →
  analyze only, the answer printed, review `dropped: no code change`. 60: the old complexity
  keys and a threshold for review fail with a config error naming the replacement, with no Jev
  request and no provider call. 61: the first implement on the simple model leaves the test
  red, the run stops at `max_steps: 2`, and the resume's fix runs on the medium model, recorded
  as `escalated.medium` (the escalation and the resume are one scenario, not two). 62: three
  answered runs of sizes 0.2, 0.6 and 1.2 give `cuts for 34/33/33: medium 0.40, complex 0.90`
  and the diff from the cuts they were decided with.
- **Docs.** MVP.md §8.2 (the three questions), §9 (the rule), §9.1 (rewritten: the example
  values, why the shape changed, where the cuts come from), the example config in §5, §44.2
  (checks run with every workflow that has development), §44.8 (escalation), §45.4 (the size
  section of calibrate), the field references (§45 fields, §44.9 `model_source`), and the
  `decisions calibrate` usage lines. §31.1 was done in DS06. README: the example, the policy
  bullets, the decision steps, `escalate`, calibrate. CLAUDE.md: the spec list, the decision
  flow, the policy rules, static routing, the run artifacts. `org.example.yaml`: the force
  comment. TODO.md: G6, ticked, with the open question.
- **Bench configs** were already updated in DS02 and DS04 (their test validates them against the
  built binary). `specs/benchmark/SPEC.md` still describes the first round's six thresholds; it
  is the benchmark's own record of what that round ran, and is left as written.
