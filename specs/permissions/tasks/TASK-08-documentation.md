# T08 — MVP.md §38, README "Security model", CLAUDE.md invariants

**Phase** 2 (lands with the code it describes) · **Size** S–M · **Depends on** T01–T05, T07

## Goal

An operator reading only README can say correctly what each step may do and what is not
protected; MVP.md is normative; CLAUDE.md keeps future changes aligned.

## Steps

1. **MVP.md §38 "Execution authority"** (normative): the four profiles, the §5.1 table,
   modes do not change authority, fail-closed rule and its position in the fail-fast chain,
   the guards (§7), the audit fields. Touch up §7 (add `ValidateAuthorities` to the chain),
   §15 (adapters declare `Supports` and translate authority), §22 (`Authority`,
   `ProviderRef.Authority`, `Run.PreRunRef`), §27 (the `authority:` line after `workflow:`,
   `Restore point:` line, manual gate summary), §21 (run.json fields).
2. **README "## Security model"** after Configuration: one paragraph (steps get the least
   tools their job needs; no step has a shell; unsupported → refuse); the table; modes; the
   guards; **"What is not protected"** — `SPEC.md` §10 verbatim; the `--setting-sources ""`
   auth note (§6.2); how to run the live suite.
3. **CLAUDE.md** "Non-negotiable invariants": no `bypassPermissions`; authority comes from
   `domain.AuthorityFor` / explicit `AuthorityNone`, never from a model, provider default or
   config; no profile has a shell tool, and argument-pattern Bash rules are never a boundary;
   flags only in `buildArgs`; an adapter that cannot enforce must refuse; `implement`'s
   result is guarded before `test`. Add `pre_run_ref` / `authority` to the run-artifacts
   description.
4. `orchestrator.example.yaml`: `security:` block with comments.
5. `specs/permissions/README.md`: flip status to "implemented" with the landing commit.

## As built

- MVP.md: new §38; touched §4 (modes don't change authority), §5 (example: review on claude,
  `security:` block), §7 (authority check with an example error), §15, §16 (clean start,
  restore point), §21, §22, §23 (`AuthorityError`, the real claude command line), §24, §25.1
  and §27 (examples moved off codex; `authority:`/`Restore point:` lines), §30.
- README: "Security model" with the may/may-not table and "What is not protected"; config,
  validation chain, dirty-tree paragraph, run.json, live suite.
- CLAUDE.md: two new invariants (least authority; guard implement's result), fail-fast chain,
  fencing, artifacts, tests.
- Found while writing the docs: `apperr` joined argv with bare spaces, so the `command:` line
  lost `--setting-sources ""` and split the `--settings` JSON — no longer pasteable. Args
  that a shell would split or drop are now single-quoted (the `<prompt: N chars>` marker is
  left as is).

## Acceptance

- Someone who did not write the code, reading README only, can state what `implement` may
  and may not do and why `test` is the remaining risk.
- No document describes behaviour the code does not have at the time it lands.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
