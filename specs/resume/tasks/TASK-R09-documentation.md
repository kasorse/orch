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

## As built

- MVP.md §37 is rewritten as "Resume by Run ID" in twelve subsections (CLI, order of
  operations as `resumeTask` runs it, resumability, loading and validation, entry state
  and prompt context, limits and timeout, mode, safety, locking, persistence, errors, not
  covered). §38.2 adds the runs directory in the deny list. §38.3 adds the resume-input
  guard patterns and the run-start baseline. §38.4 adds the new `run.json` fields.
  §38.5 adds the orphaned-CLI residual risk.
- Also touched in MVP.md: §4 (the `resume` command), §21 (resumable, locked, new fields),
  §22 (`Resume`), §23/§24 (`RunStateError`, exit 1), §25 (a resume's timeout budget), §27
  (the resume overview, from the `resume_limit_raised` golden), §29 (resume scenarios).
- §37 describes the code as built where it differs from `SPEC.md`: the Entry-before-
  baseline order, `pre_run_ref` checked after the fingerprint, and a resume changing
  nothing before approval (R06). A pruned snapshot commit making the run `corrupt` is
  stated in "Not covered".
- CLAUDE.md: the resume docs in the header, `workflow.Entry` in the engine paragraph, one
  invariant bullet, and the new `run.json` fields.
- README: the usage line, a "Resuming a run" section, the `run.json` fields, the
  resume-input guard, and the SIGKILL residual risk.
- Every function, file and message §37/§38 names was grepped in non-test code. The
  unused `RunNotFoundError` of the old sketch is gone.

## Acceptance

- No doc still describes resume as planned.
- Every claim in §37 matches the code: file and function names are checked with grep.

## Gates

`gofmt -l .` empty (no code change expected)
