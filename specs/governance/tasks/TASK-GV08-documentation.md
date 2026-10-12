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
