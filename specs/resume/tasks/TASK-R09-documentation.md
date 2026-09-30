# R09 — MVP.md §37/§38.5, CLAUDE.md, README

**Size** S (0.5 d) · **Depends on** R01–R08

## Goal

The normative docs describe resume as built. This folder becomes the design record, as
`specs/permissions/` is for §38.

## Files

- `MVP.md` — §37 rewritten; §38.5 residual risk; §4 (CLI), §21 (persistence), §23/§24
  (errors, exit codes), §27 (output) where they list commands or run fields
- `CLAUDE.md` — "Run artifacts" (new `run.json` fields), a line under invariants on resume
- `README.md` — usage
- `specs/resume/README.md` — status, and an "As built" note in each task that deviated

## Steps

1. §37: title "Resume by Run ID". Remove "planned / not in initial MVP delivery". Normative
   summary of `SPEC.md` §6–§13: the resumability table, the entry state, rehydration,
   safety order (lock → validate → fingerprint → baseline → HEAD), limits, mode, errors.
   Link to `specs/resume/`.
2. §38.3/§38.5: the run-start baseline and the resume-inputs guard patterns; the residual
   risk of an orphaned provider CLI after SIGKILL.
3. CLAUDE.md: `run.json` gets `head_ref`, `resume_baseline`, `resumes`, and per-execution
   `attempt`. One invariant bullet: resume never re-decides, and compares the fingerprint
   before any `git status`.
4. The README usage block: `orchestrator resume <run-id>`.

## Acceptance

- No doc still describes resume as planned.
- Every claim in §37 matches the code: file and function names are checked with grep.

## Gates

`gofmt -l .` empty (no code change expected)
