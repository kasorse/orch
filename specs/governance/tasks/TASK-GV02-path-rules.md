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
