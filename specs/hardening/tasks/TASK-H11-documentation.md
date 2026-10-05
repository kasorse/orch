# H11 — Documentation

**Size** S (0.5–1 d) · **Depends on** H10

## Goal

MVP.md becomes the normative home of phase 0; CLAUDE.md, README and the example config match
what was built; TODO.md reflects the status.

## Steps

1. **MVP.md**
   - §7 fail-fast chain: the CLI version check after `ValidateAuthorities`; the sandbox probe
     now runs by default.
   - §18 test: the pre-run test (`test.baseline`), the test environment allowlist and
     `test.env_pass`.
   - §19 review: `ReviewDiff` (new files, deny list, summarize, budget, artifact `diff`).
   - §25 limits: `limits.prompt.*`.
   - §37 resume: the pre-run test re-runs for a run without executions; `resumes[].provider_cli`.
   - §38.3: sandbox default, allowlist (and why D7 does not apply to tests), secret scan.
   - Scenario list: scenario 23.
2. **CLAUDE.md** — invariants: prompt on stdin (never argv); test command gets an allowlisted
   env and runs under bwrap unless `none` is explicit; the CLI version is checked against the
   adapter's verified range before any call; added content is scanned for secrets after
   implement and test; review diff bounded and deny-list-aware. Run artifacts: `provider_cli`,
   `pre_run_test`, `secret_scan_baseline`, `secret_findings`, `logs/pre-run-test.*`.
3. **README** — prerequisites (bwrap by default, AppArmor note, macOS → `none`), the new
   config keys, the "unverified CLI" message and what to do.
4. **orchestrator.example.yaml** — every new key with a one-line comment.
5. **specs/hardening/** — README status "implemented", "As built" in each task.
6. **TODO.md** — S1–S5 `[x]` with a pointer to `specs/hardening/`; S6 per H12.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty

## As built

Done 2026-10-06.

- **MVP.md**: a new normative **§41 Hardening** (prompt delivery, bounded content, the test
  environment and sandbox, the CLI version, the pre-run test with `--allow-red-tests`, the
  secret scan, persistence, not covered), plus edits where the old text would now be wrong:
  §4 (the `--allow-red-tests` flag), §5 (the config sample), §7 (version check and the
  sandbox probe by default), §18 (environment, bounded output, pre-run test), §19 (what the
  review diff is), §29 (hardening scenarios 16c/d, 23–28), §37.2 (the pre-run test and the
  secret baseline in the shared tail), §38.2 (prompt on stdin, version range, `TestEnv` for
  the test command), §38.3 (sandbox default, pre-run test under the guard, secret scan),
  §38.4 (audit fields), §38.5 (`none` and the scan's limits). The spec's per-section list
  (§17) was followed, but resume's details live in §41.5/§41.6 rather than in §37 itself.
- **CLAUDE.md**: the hardening spec in the source list; the version check in the fail-fast
  invariant; `TestEnv`, the default sandbox, the pre-run test and the secret scan in the guard
  invariant; prompt on stdin and bounded data in the invocation invariant; the new run
  artifacts; and a rule never to commit token-shaped literals in tests.
- **README**: prerequisites (claude version range, bwrap), the config sample and key
  descriptions, the version check and the secret scan in the security model, the sandbox as
  the default with the test-command allowlist, "What is not protected", a new section
  "Tests that already fail", the run artifacts, and how the new stops read (including the
  provider error's `prompt: N bytes on stdin`).
- **orchestrator.example.yaml**: `allow_unverified_cli` added (the other keys came with their
  tasks).
- **Statuses**: `specs/hardening/README.md` and `SPEC.md` say implemented; TODO.md S1–S5 are
  ticked with a pointer to §41; S6/H12 done by the owner. H13 is optional.
