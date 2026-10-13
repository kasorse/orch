# GV02 — `policy.paths`: config, start warning, check after implement, record

**Size** M (3 d) · **TODO** G1.1–2, G1.4 · **Depends on** — · **Blocks** GV03, GV04, GV07

## Goal

Path rules require stages for changes that match a pattern. They are checked on the run's
net diff after each `implement` and recorded on the execution. A resume applies the rules the
run started with (`SPEC.md` §4.1–§4.4).

## Files

- `internal/config/decision.go`: `PolicyConfig.Paths []PathRuleConfig` (`match`,
  `require_stages`, `approval`, plus a declared `deny_change` that is rejected)
- `internal/config/validator.go`:
  - `ValidateProtectedPattern`;
  - stage names and no duplicates;
  - `approval` is `manual` or empty;
  - a rule is not empty;
  - the `deny_change` message
- `internal/domain`:
  - `PathRule`, `PathRuleMatch {match, files, require_stages, missing, approval}`;
  - `RunOptions.PathRules`, `StepExecution.PathRules`;
  - a pure `MatchPathRules(rules, files, workflow) []PathRuleMatch`
- `internal/action/implement.go`: after the guard and the secret scan pass, get the files of
  `DiffNumstat(pre_run_ref or before, after, runsExclude)`, then `MatchPathRules`, then
  record. If any rule has missing stages, fail the step with the `SPEC.md` §4.2 message.
- `internal/app`:
  - record `options.path_rules` at run start;
  - the start warning after `workflow.Build` for a workflow with `implement`;
  - the resume warning when the config's rules differ
- `internal/report/summary.go`: a "Path rules" section for the last `implement`
- `internal/persistence/validate.go`: the new fields (`SPEC.md` §8)

## Steps

1. Add the config type, its validation, and the example config.
2. Write `domain.MatchPathRules` with a table test:
   - no match;
   - one match;
   - two rules on one file;
   - the stage present or missing;
   - the `MaxDiffPaths` cut.
3. Record the rules in `RunOptions` at run start. `Dispatcher` reads them from the run, never
   from config.
4. Add the check in `implement`. Reuse the `after` tree `stepDiff` already computes; do not
   compute it twice.
5. Add the start warning and the resume warning, both on stderr with the other `Warning:`
   lines.
6. Add the summary section, then persistence validation, then a load test with an old run that
   has no fields.

## Acceptance

- Unit: `MatchPathRules` table; config errors (empty rule, unknown stage, `deny_change`
  message, `approval: auto`).
- Action: a fake repo where implement adds `migrations/a.sql`. With a rule requiring testing
  and a workflow without testing, the step fails with the message. With testing, the step
  passes and `path_rules` is recorded.
- Resume: a run started with rule X, then the config changed to rule Y. The run still applies
  X and prints the warning.

## Gates

`scripts/check.sh`

## As built (2026-10-13)

Built as planned, except as listed. The code and its tests were written without being able to
run anything: `scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run
for this task. It is the operator's to run, and GV02 is not ticked in `README.md` or TODO.md G1
until it passes.

- **Fail closed when git cannot list the files.** `stepDiff` is a best-effort record and never
  fails a step. The path check needs its file list, so with rules in the run and no list (the
  tree before the call could not be read, or `stepDiff` failed) the step fails with
  `policy.paths cannot be checked: git could not list the changed files`. A run without rules
  behaves as before. This is not in the spec.
- **No second diff.** `stepDiff` now also returns the paths of the run's net diff (its
  `DiffNumstat(pre_run_ref, after)` list, or the step's own list without a `pre_run_ref`), and
  `executeImplement` hands them to `checkPathRules` after the secret scan. Paths are the top
  level relative ones git gives, the same as the guard matches.
- **Stages go through the action.** `domain.MissingStages` asks `Stage.Action()` and
  `Workflow.Contains`, not `Step.Stage`, so a workflow whose steps name no stage, or one a
  manual-mode gate edited, is judged by the actions it runs. An unknown stage counts as missing.
- **An empty rule with `deny_change`** reports only the `deny_change` message, not "is empty"
  as well. `deny_change: null` decodes to nil and is not seen by the validator; such a rule is
  reported empty unless it has stages or an approval.
- **Start warning** is printed by `warnPathRules` right after `out.announce` and again in
  `onEdit` after the workflow is rebuilt, from `run.Options.PathRules`. The resume warning sits
  after `reporter.NextStep`, next to the model change warnings.
- **`approval: manual` is only recorded** here; the commit offer (GV03) and
  `outcome.approval_required` are not touched.
- **Tests** are `domain/pathrules_test.go`, `config/pathrules_test.go`,
  `action/pathrules_test.go` (adds the net-diff revert case and the fail-closed case),
  `app/pathrules_test.go`, `persistence/pathrules_test.go`, a case in `report/summary_test.go`,
  and `test/integration/pathrules_test.go`, which runs the resume case end to end (rule X
  fails the first implement, the config is changed to rule Y, the resume warns and still
  applies X). The integration config writer got a `pathRules` option.
- **Docs:** MVP.md §43 (a "Path rules" section for `summary.md` and the two `run.json` fields),
  CLAUDE.md's run.json field list and `orchestrator.example.yaml` (a commented `policy.paths`
  block). MVP.md §45 stays with GV08.

**Gates passed 2026-10-13:** `scripts/check.sh` (root and `bench/`, outside the sandbox) passed over the
whole phase, GV01–GV08, before the merge into `master`.
