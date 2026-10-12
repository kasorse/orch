# GV04 — org config layer: find, strict schema, merge, record

**Size** M (3–4 d) · **TODO** G2 · **Depends on** GV01, GV02 · **Blocks** GV07

## Goal

An org file sets floors, ceilings and additions that the repository config can only tighten. A
conflict is a config error naming both files. The run records which org file it ran under
(`SPEC.md` §6).

## Files

- `internal/config/org.go` (new):
  - `OrgConfig`, version 1, strict, with only the keys of `SPEC.md` §6.2;
  - `FindOrgConfig(etcPath string, getenv func(string) string) (path string, ok bool)`;
  - `LoadLayered(repoPath, orgPath string) (Config, OrgSource, error)`
- `internal/config/validator.go`: the `allowed_models` check over every route, the
  translator and the LLM decision provider
- `internal/app`: `loadAndValidateConfig` uses `LoadLayered`; `validate` prints the org
  source; record `org_config` on the run and on each resume; print the resume warning when
  the sha256 changed
- `internal/domain/run.go`: `OrgConfigRef {path, sha256}` on `Run` and on each resume entry
- `internal/persistence/validate.go`

## Steps

1. Define `OrgConfig` and decode it strictly. Version 1 is required, and any other key is an
   error.
2. `FindOrgConfig`: `/etc/orchestrator/org.yaml` if it exists, else
   `ORCHESTRATOR_ORG_CONFIG` if set, else none. The `/etc` path is a parameter, so tests
   inject it.
3. Merge per `SPEC.md` §6.2, one function per row with its own error. Each error names the key,
   the repository file and the org file.
4. Add `allowed_models` to `Validate`, because the merge needs the full config first. A route
   without a model is an error when `allowed_models` is set.
5. Add record and resume handling.

## Acceptance

- Unit: one test per row of the §6.2 table, both the weaker case (error) and the equal or
  stricter case (ok).
- `FindOrgConfig`: `/etc` wins over the environment variable; the environment variable is
  used when `/etc` is absent; neither gives none.
- An unreadable org file, invalid YAML, or an unknown key → error. The run never falls back to
  "no org layer".
- `validate` prints `org config: /etc/orchestrator/org.yaml`.

## Gates

`scripts/check.sh`

## As built (2026-10-13)

Built as planned, except as listed. The code and its tests were written without being able to
run anything: `scripts/check.sh` (root and `bench/`, outside the sandbox) has **not** been run
for this task. It is the operator's to run, and GV04 is not ticked in `README.md` or TODO.md
G2 until it passes.

- **Schema.** `config.OrgConfig` has org-only section structs (`OrgSecurity`, `OrgTest`,
  `OrgPolicy`, `OrgLimits`), so `policy.stages.thresholds`, `auto_commit` and every other
  key outside §6.2 is an unknown key. The version is probed before the strict decode, so a
  version 2 file says "version must be 1" instead of listing its keys. The org file may only
  state the strict side of a floor (`require_clean_worktree: true`, `allow_unverified_cli:
  false`, `test.sandbox: bwrap`); the other value is a config error in the org file itself.
- **Merge.** One function per §6.2 row in `internal/config/org.go`; the conflicts of all rows
  are joined into one `ConfigError` whose every line names the repository file and the org
  file. A repository without `limits.max_cost_usd` inherits the org's ceiling (the budget
  still applies) rather than being an error. A `secret_scan` mode the repository wrote that is
  not a mode is left to `Validate`. An org `secret_scan` without a `mode` sets no floor but
  still owns `allow_paths`.
- **`allowed_models`** is carried on `Config` as `AllowedModels` / `AllowedModelsFrom`
  (`yaml:"-"`, so the repository file cannot set it) and checked by `allowedModelProblems` in
  `Validate`. A `by_complexity` entry with no model inherits the base model, which is checked
  on its own; the Jev decision model is not a CLI model and is not checked.
- **Find.** `FindOrgConfig` treats a stat error other than "does not exist" as "exists", so
  loading fails instead of falling back to the environment variable. The path is recorded
  absolute (`filepath.Abs`).
- **Record.** `domain.OrgConfigRef` on `Run` and on each `Resume`; persistence checks an
  absolute clean path and a 64-digit lowercase hex sha256. A resume compares its sha256 with
  the run's start record (not the previous resume's) and warns on any difference, present or
  absent included.
- **`validate`** prints `org config: <path>`, or `org config: none`, through
  `ConsoleReporter.OrgConfig`.
- **Tests:** `config/org_test.go` (every row, both cases; find; file errors), `config/
  validator_test.go` (`allowed_models`), `persistence/orgconfig_test.go`,
  `app/orgconfig_test.go`, a case in `logging/progress_test.go`, and
  `test/integration/org_config_test.go` (validate output, a conflict, run and resume records,
  the warning). The integration tests name the org file through `ORCHESTRATOR_ORG_CONFIG`, so
  they assume no `/etc/orchestrator/org.yaml` on the host; GV07 adds the `/etc` override.
- **Docs:** MVP.md, CLAUDE.md, README.md and the example config stay with GV08.
