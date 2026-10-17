# WT10 — scenarios, bench configs, documentation

**Size** M (2 d) · **Depends on** all · **Blocks** —

## Goal

The worktree run is covered end to end, documented as normative, and the benchmark is unaffected
(`SPEC.md` §14).

## Files

- `test/integration`: the scenarios of `SPEC.md` §14 not already added by WT04–WT08, with
  `XDG_STATE_HOME` in `t.TempDir()`; existing scenarios that check the working directory pin
  `execution.isolation: inplace` in their config
- `bench/configs/*.yaml`: `execution.isolation: inplace` (the bench's check reads the workspace
  tree); the bench's golden files if they print the config
- MVP.md: new §47 (from `SPEC.md` §6–§13); §5 (the keys); §37.4/§37.8 (resume in the worktree);
  §38.3 (the clean-start rule per mode, the `.git` pointer); §41.3 (the `.git` file bind,
  setup's network); §42.1/§42.5/§42.6 (the run's branch); §43 (the worktree line)
- CLAUDE.md: the invariant "the operator's tree is never touched in worktree mode", the pointer
  check, setup only before any step, the new run artifacts (`worktree`, `logs/setup.*`)
- README: how to use the branch; `runs clean`
- `orchestrator.example.yaml`: both modes explained
- TODO.md: Q4 `[x]`; W1 notes what this delivered (the branch)

## Acceptance

`scripts/check.sh` passes outside the sandbox, root and `bench/`; the bench's pipeline test runs
with the pinned configs.

## Gates

`scripts/check.sh`
