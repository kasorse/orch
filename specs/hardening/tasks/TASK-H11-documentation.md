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
