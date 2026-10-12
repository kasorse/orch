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
