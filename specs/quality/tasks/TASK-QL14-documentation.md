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
