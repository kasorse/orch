# CA05 — `run.json`, `resumes[]`, `decision.json`, `summary.md`

**Size** S (0.5 d) · **Depends on** CA03 · **Blocks** CA06

## Goal

Every run says which credential mode it ran under, and every claude call which key source the
CLI reported (`SPEC.md` §10, §11).

## Files

- `internal/domain/run.go`: `Run.ClaudeAuth`, `Resume.ClaudeAuth`, execution
  `provider.api_key_source`
- `internal/persistence` (+ test): round trip; strict validation of the new fields (mode in
  the closed set; no other keys)
- `internal/logging` (decision trace) and its golden files: `api_key_source` in each usage block
- `internal/report` / summary renderer (+ golden): the `Claude auth:` line, the
  "(estimate; billed to the subscription)" note in `login`
- `internal/app/resume.go`: re-resolve, record, the "changed since the run started" notice

## Steps

1. Write the fields at run start and on each resume; never read them back for a decision.
2. Old `run.json` files without the field load and resume (the field is optional).
3. Regenerate golden files with `-update`; read the diff before committing.

## Acceptance

- Round trip of a run with two resumes in different modes; `summary.md` shows both.
- A `run.json` from before this change still resumes.
- `grep` of the whole run directory for the test key value finds nothing.

## As built

- `Run.ClaudeAuth` / `Resume.ClaudeAuth` are `*domain.ClaudeAuth` (`claude_auth`, omitted when nil),
  validated by `ClaudeAuth.Validate` in `persistence.validateRun`. The execution's
  `api_key_source` was already on `CallReport` (CA03), so it sits in `call.api_key_source`, not
  `provider.api_key_source`; `domain.Usage` gained `api_key_source`, set by `parseStream`, which
  is what the decision usage blocks in `decision.json` carry (no golden files needed changing).
- `summary.md` gets a `Claude auth` row only when the run recorded one, and the cost note when
  any part ran in `login`.
- `resume` records its own mode and prints the change notice against the last recorded mode.
- Left to the operator: `scripts/check.sh` (root and `bench/`); the task is not marked done
  until it passes. No test greps a run directory for the key value yet (CA06).

## Gates

`scripts/check.sh`
