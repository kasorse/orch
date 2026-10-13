# DS02 — `size`, `policy.complexity.cuts`, `questions_hash`

**Size** M (2–3 d) · **Depends on** — (gate O3 passed) · **Blocks** DS05, DS06, DS07

## Goal

The level comes from one number, `size = P(medium) + 2·P(complex)`, cut by `policy.complexity.cuts`
(`SPEC.md` §5). Every decision records its size, its cuts and the hash of the questions it was
asked.

## Files

- `internal/domain/policy.go`: `DecisionPolicy.ComplexityCuts {Medium, Complex float64}`
  replaces `ComplexityThreshold`; `ComplexityOutcome.Size`, `ComplexitySourceOutcome.Size`,
  `ComplexityOutcome.Cuts`; `Size(ComplexityDecision) float64`
- `internal/policy/engine.go`: `SelectComplexity` walks the cuts over `Size`; the reason
  `size 0.85 >= medium cut 0.60`
- `internal/config/decision.go`: `ComplexityPolicyConfig.Cuts`; `medium` and `complex` declared
  and rejected with a message naming `cuts` and this spec; validation `0 < medium < complex <= 2`
- `internal/decision/questions.go`: `QuestionsHash()` — sha256 over the rendered questions, in
  a fixed order
- `internal/domain/trace.go`, `internal/persistence/validate.go`: `size` in `[0, 2]`, `cuts`,
  `questions_hash` 64 hex; absent for `manual`; old decisions load
- `orchestrator.example.yaml`: `cuts: {medium: 1.24, complex: 1.48}` with a comment: the
  50/35/15 quantiles of 43 claude/opus decisions (2026-10-13), valid for that decision model
  and this repository's tasks only (`SPEC.md` §1.1)

## Steps

1. `Size` and the new `SelectComplexity`; the shadow source gets its own size and level.
2. Config: the new key, the rejected keys, the validation, `PolicyHash` over the cuts
   (`TestPolicyHash_PinnedEncoding` changes on purpose: say so in "As built").
3. Trace and persistence fields.
4. The org layer has no complexity keys today; check that nothing there changes.

## Acceptance

- Unit: sizes at a cut select the higher level (inclusive); the shadow disagreeing sets
  `agree: false`; `medium: 0.7` in a config is an error naming `cuts`.
- `persistence`: `size` 2.1 → corrupt; a pre-DS02 decision loads.
- The same decision always gives the same level.

## Gates

`scripts/check.sh`

## As built (2026-10-13)

Built as planned, except as listed. `scripts/check.sh` (root and `bench/`) passed on
2026-10-13, run outside the bwrap sandbox.

- **No complexity `reason` string.** `SPEC.md` §5.3 asked for `size 0.85 >= medium cut 0.60`.
  The trace records each source's `size` and the policy's `cuts`, which say the same thing, so
  a third field that must agree with them was not added.
- **Size per source only.** `complexity.sources.<src>.size`, with no separate top-level size: the
  primary's is `sources[primary].size`.
- **`questions_hash` follows `policy_hash`.** `classification.Result.QuestionsHash` is set in
  `Classify` and copied into the trace, so a decision chosen by hand after a provider failure
  (`source: user`) carries it too, and a `Result` built by hand does not.
- **Old keys** `policy.complexity.medium` and `.complex` are `any` in the schema and rejected with
  a message naming `cuts`. The org layer has no complexity keys and did not change.
- **`TestPolicyHash_PinnedEncoding` changed on purpose** (`ComplexityThreshold` →
  `ComplexityCuts`): every `policy_hash` recorded before this task is a group of its own.
- **Bench configs moved here, not in DS07.** `bench/internal/config` validates `B`–`F` and `Bmock`
  with the built `bin/orchestrator`, so `bench/configs/*.yaml`, their goldens,
  `bench/docker/B.container.yaml` and the alternative `bench/results/go/tasks/configs/B2.yaml`
  got `cuts: {medium: 1.24, complex: 1.48}` in this task. `bin/orchestrator` must be rebuilt
  (`go build -o bin/orchestrator ./cmd/orchestrator`), or that test runs the old binary and
  reports `cuts` as an unknown key.
- **README** got the new snippet and one sentence now; the rest of the documentation stays in
  DS07.
- **Effect on the measured history.** Under the example cuts, the 43 claude/opus decisions of
  `SPEC.md` §1.1 split 21 simple / 16 medium / 6 complex, instead of 4 / 29 / 10. Integration
  scenarios needed no new expectations: their fake answers land on the same levels under the
  test cuts `0.5` / `1.4`.
