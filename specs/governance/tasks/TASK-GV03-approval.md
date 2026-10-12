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

## As built (2026-10-13)

Built as planned, except as listed. The code and its tests were written without being able to
run anything: `scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run
for this task. It is the operator's to run, and GV03 is not ticked in `README.md` or TODO.md
G1 until it passes.

- **Helpers in `domain/pathrules.go`.** `LastImplementPathRules(run)` is the last `implement`
  execution's matches (any status; an earlier implement's do not count) and
  `ManualApprovalMatches` keeps the `approval: manual` ones. `ComputeOutcome`,
  `report.writePathRules` and `app.commitOffer` all use them, so the three agree.
- **`CommitOffer.PathRules`** carries only the manual matches (`[]domain.PathRuleMatch`);
  `AskCommit` prints them after the warnings under `policy.paths requires your approval for:`,
  one rule per line with its files below. The offer is derived from the executions, not from
  `outcome`, so it works before the outcome is written.
- **Where the check sits.** After `commitOffer` is built (so after the status and branch
  checks: with no changes the run still says `No changes to commit.`) and before the `auto`
  switch. The reason lists every manual match: `policy.paths requires manual approval:
  migrations/** (1 file), **/*.sql (2 files); commit what you want yourself.`
- **Summary line** is a row `Approval | Commit needs manual approval (policy.paths)`, shown
  when `outcome.approval_required` is true.
- **Tests:** `domain/outcome_test.go` (the last implement no longer matching after an earlier
  one did), `interaction/commit_test.go` (the offer with two rules, as a full-output golden),
  `app/commit_approval_test.go` (a real repository: `auto_commit` with approval leaves HEAD
  and prints the reason; without approval it commits; the offer's rules; the reason text) and a
  case in `report/summary_test.go`.
- **Docs:** MVP.md §42.2, §42.6, §43.2 (the Approval row), §43.3 (`outcome.approval_required`),
  CLAUDE.md and README.md. MVP.md §45 stays with GV08.
