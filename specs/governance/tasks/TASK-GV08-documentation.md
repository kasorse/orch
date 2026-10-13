# GV08 — MVP.md §45 and others, CLAUDE.md, README, example config, TODO.md

**Size** S (1 d) · **TODO** — · **Depends on** GV07 · **Blocks** —

## Goal

MVP.md becomes the normative home of everything this spec built. CLAUDE.md, the README and
the example config describe it.

## Files

- `MVP.md`:
  - a new §45 "Governance": path rules, the org layer, the budget, calibration and
    exploration, and the honesty note of `SPEC.md` §3.6;
  - updates to §7 (fail fast: budget, org file), §9 (exploration, `policy_hash`), §12 and §25
    (`max_cost_usd`), §37 (`options.path_rules`, `org_config`), §42 (approval) and §43
    (summary sections, `outcome.approval_required`)
- `CLAUDE.md`:
  - the governance pointer in the spec list;
  - the invariants: path rules from the diff with no workflow rebuild, the org layer only
    tightens, the budget fails closed, calibration only proposes;
  - the new run artifacts
- `README.md`: a short governance section
- `orchestrator.example.yaml`: `policy.paths`, `limits.max_cost_usd` and `policy.explore`,
  commented out; a separate `org.example.yaml`
- `TODO.md`: tick G1, G2, G3 and G5 (G4 is already `[-]`)
- `specs/governance/README.md`: the `Status:` line

## Acceptance

Every config key of this spec appears in the example files and in MVP.md, and every new
`run.json` and `decision.json` field appears in MVP.md §43 and in CLAUDE.md's artifact list.

## Gates

`scripts/check.sh`

## As built (2026-10-13)

Built as planned, except as listed. Only documentation and `org.example.yaml` changed.
`scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run, and nothing
else was run either. It is the operator's to run, and GV08 is not ticked in `README.md`, and
TODO.md G1, G2, G3 and G5 stay `[ ]`, until it passes (the earlier tasks wait on the same run).
The `Status:` line of `README.md` says so and says what to tick.

- **Already there.** GV02, GV03, GV05 and GV06 had put their parts in `orchestrator.example.yaml`
  (`policy.paths`, `policy.explore`, `limits.max_cost_usd`), MVP.md §42 and §43 (the Approval row,
  the Path rules section, `outcome.approval_required`, `options.path_rules`,
  `executions[].path_rules`), the `decisions calibrate` entry in §4 and the README usage, and the
  artifact list and the commit invariant in CLAUDE.md. GV08 added what was still missing: MVP.md
  §45, the notes in §5, §7, §9, §12, §25 and §37 and the `org_config`, `policy_hash` and
  `stages[].explored` fields in §43.3; CLAUDE.md's pointer, invariant and artifact entries;
  the README section; `org.example.yaml`.
- **MVP.md §45** is one section with six subsections (path rules, the budget, the org layer,
  calibration and exploration, the honesty note, not covered); the other sections point to it
  rather than repeat it.
- **`org.example.yaml`** sets every key of the org schema, with values chosen as an example
  (`.github/**`, `go.mod`, `sonnet`, `opus`, `haiku`); nothing loads it, so no test covers it.
