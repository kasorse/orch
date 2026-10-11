# QL14 — Documentation

**Size** S (1 d) · **TODO** — · **Depends on** QL13 · **Blocks** QL15

## Goal

MVP.md becomes the normative home of this phase. CLAUDE.md, the README and the example config
match what was built (`SPEC.md` §18).

## Files

- `MVP.md`: new §44 "Quality loop"; §12, §18, §19, §31.1, §37, §38.1, §41.5, §43
- `CLAUDE.md`: invariants, run artifacts, the pointer to `specs/quality/`
- `README.md`, `orchestrator.example.yaml`
- `TODO.md`: phase 2 statuses
- `specs/quality/README.md`, `SPEC.md`: Status lines

## Steps

1. MVP.md §44: stage context; checks; verify (tools, filter, wiring, guard, limits, record);
   review criteria, `fix_medium` and extra reviewers; the no-progress stop; test names;
   `by_complexity`. Include the field reference for every new `run.json`/`decision.json`
   field.
2. Edit the other MVP sections as listed in `SPEC.md` §18. §31.1 gets the sentence about a
   static table keyed by the decided complexity. §31.4 stays as it is, and a short note
   says parsed test names never decide.
3. CLAUDE.md:
   - **Invariants:** verify is not a shell (D16): fixed tools, command from config, filter
     only via `ORCH_TEST_FILTER`, sandbox mandatory, guard before every call, and the
     `violation` marker fails the step. Checks are exit codes, with a guard after each one.
     Parsed test names never decide. `by_complexity` is static routing.
   - **Run artifacts:** add the new fields and logs.
   - Add the spec pointer line at the top.
4. README: a short section with a config example for checks, verify and `by_complexity`.
5. Example config: every new key with its default and a one-line comment, including the note
   to raise the implement timeout with verify on.
6. TODO.md: tick Q1, Q2 (minus the benchmark part, which stays with QL15), Q3 (same), Q5, Q6
   and Q7.

## Acceptance

Every key in `SPEC.md` §11 and every field in §12 is documented in MVP.md and the example
config. `grep -n "specs/quality" CLAUDE.md MVP.md` finds the pointers.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty (the example config
is loaded by an existing test)

## As built (2026-10-11)

Done as planned, except as listed.

- **Gates:** only documentation and comments changed, but nothing was run: the four gates
  (`scripts/check.sh`) are the operator's. They passed on 2026-10-11 (root and `bench/`,
  outside the sandbox). The example config changed in comments only, so the test that loads it is unaffected.
- **TODO.md is not ticked.** Step 6 asked for Q1, Q2, Q3, Q5, Q6 and Q7. Each of them still waits
  on something this task cannot close: the live suite of QL06 (Q2), the gates of QL08–QL10 and
  QL13 (Q1, Q3, Q5, Q6, Q7) and the benchmark comparison of QL15 (Q2, Q3). They stay `[~]`; the
  phase header says the documentation is done and what each one waits for.
- **Much of the text already existed.** QL01–QL12 had each documented their own part in
  `CLAUDE.md`, `README.md`, the example config and MVP.md §17 and §43 (the stage context, the
  criteria, the `Verify`, `Failing tests` and `Reviewers` rows, the checks, the no-progress
  stop, the `by_complexity` and `fix_medium` keys). QL14 added what was missing and
  put it in one place: MVP.md §44 is the normative home and the field reference, and the other
  sections point to it.
- **MVP.md:** §44.1–§44.10 (stage context, checks, verify, review, the configuration summary, no
  progress, test names, `by_complexity`, the field reference, not covered); §12, §18, §19, §31.1,
  §31.4 (a note: the rule is unchanged, parsed names never decide), §37.5–§37.6 (`fixable`,
  the resume of a no-progress stop, the counters), §38.1 (the verify tools under `edit`, with a
  pointer to D16), §38.3 (a guard after each check), §41.5, §43 and §26 (`guard`, `verify`,
  `testreport` in the package list, not in the task's list). The criteria text in §17 stayed
  as QL01/QL02 wrote it.
- **CLAUDE.md:** a pointer line to §44 and `specs/quality/`, three invariants (verify is not a
  shell, parsed names never decide, Go decides the quality loop), the checks' guard in the
  guard invariant, `by_complexity` under static routing, and the new run.json, decision.json
  and log names in the run artifacts.
- **README.md:** a section "Getting more from the same stages" with one config example for
  `checks`, `verify`, `by_complexity`, `fix_medium` and `models`, and the new logs and fields in
  the run artifacts.
- **Example config:** every key already had a comment and its default; added the default to the
  `checks`, `by_complexity` and `models` comments and the note to raise the implement timeout
  with verify on.
- **Not done here:** D16's results (QL06) and the benchmark configurations (QL15).
