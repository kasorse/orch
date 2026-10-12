# GV03 — `approval: manual` in the commit offer, `outcome.approval_required`

**Size** S (1 d) · **TODO** G1.3 · **Depends on** GV02 · **Blocks** GV04, GV07

## Goal

A change that matched an `approval: manual` rule is never committed without a person's yes
(`SPEC.md` §4.5).

## Files

- `internal/domain/outcome.go`: `Outcome.ApprovalRequired`, from the last `implement`'s
  `path_rules`
- `internal/app/commit.go`: under `auto_commit`, skip with the reason; interactively, pass the
  matched rules to the offer
- `internal/interaction`: `AskCommit` prints the matched rules and their files before the
  question
- `internal/report/summary.go`: the line "Commit needs manual approval (policy.paths)"

## Steps

1. `ComputeOutcome` sets `ApprovalRequired` when the last `implement` execution has a match
   with `approval: manual`.
2. In `offerCommit`, after the baseline check and before the `auto` switch: if the run needs
   approval and `auto` is on, call
   `skip("policy.paths requires manual approval: <match> (<n> files); commit what you want yourself.")`.
3. `commitOffer` carries the matches, and `AskCommit` lists them.

## Acceptance

- `app` unit: `auto_commit` with approval → no `CommitAll`, the reason printed; without
  approval → commit as before.
- `interaction` golden: the offer with two matched rules.
- `domain`: the outcome with or without a matching rule, and with the last implement not
  matching after an earlier one did.

## Gates

`scripts/check.sh`
