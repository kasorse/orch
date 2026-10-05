# Hardening — reliable prompts and safe defaults (phase 0)

Status: **ready for implementation** (2026-10-05). The design questions are answered
(`SPEC.md` §13); the author's defaults are in §14. Covers TODO.md phase 0 (S1–S6).

| document | role |
|---|---|
| [`SPEC.md`](SPEC.md) | the specification |
| [`tasks/`](tasks/) | 13 implementation tasks |

## The problem in one paragraph

A large diff or test log breaks the provider call (the prompt is one argv string over
Linux's 128 KiB limit) and review never sees the content of new files. The test command runs
model-written code with the developer's credentials in its environment and, by default,
without a sandbox. The orchestrator trusts any version of the claude CLI although its
permission flags were verified on two. Tests are never run before the change, so a red suite
is blamed on `implement`. Nothing stops a secret written into the repository from reaching the
review model. This phase fixes all of it without widening any authority.

## Tasks

| task | title | TODO | size | depends on |
|---|---|---|---|---|
| [H01](tasks/TASK-H01-prompt-on-stdin.md) | prompt on stdin, fake CLI reads stdin | S1 | S | — |
| [H02](tasks/TASK-H02-bounded-test-output.md) | `limits.prompt`, bounded test output ✓ | S1 | S | — |
| [H03](tasks/TASK-H03-review-diff.md) | review diff: new files, deny list, summarize, budget ✓ | S1 | M | H02 |
| [H04](tasks/TASK-H04-test-env-allowlist.md) | test environment allowlist, `test.env_pass` ✓ | S2 | M | — |
| [H05](tasks/TASK-H05-sandbox-default.md) | sandbox on by default, explicit `none` warns ✓ | S2 | S | H04 |
| [H06](tasks/TASK-H06-cli-version-check.md) | provider CLI version check, override, record | S3 | M | H01 |
| [H07](tasks/TASK-H07-pre-run-test.md) | pre-run test (`test.baseline`, `--allow-red-tests`) ✓ | S4 | M | H04, H05 |
| [H08](tasks/TASK-H08-secretscan-package.md) | `internal/secretscan` rules and tests ✓ | S5 | M | — |
| [H09](tasks/TASK-H09-secret-scan-wiring.md) | secret scan in the run: baseline, stop/warn, redaction ✓ | S5 | M | H03, H07, H08 |
| [H10](tasks/TASK-H10-integration.md) | integration scenarios | — | M | H01–H09 |
| [H11](tasks/TASK-H11-documentation.md) | MVP.md, CLAUDE.md, README, example config, TODO.md | — | S | H10 |
| [H12](tasks/TASK-H12-branch-cleanup.md) | delete merged branches (local + origin) | S6 | S | — |
| [H13](tasks/TASK-H13-weekly-live-check.md) | weekly live permission check in CI (optional) | S3 | S | H06 |

```
H01 ── H06 ───────────────────────────── H13 (optional)
H02 ── H03 ──────────────┐
H04 ── H05 ── H07 ───────┼── H09 ── H10 ── H11
H08 ─────────────────────┘
H12 (any time)
```

Estimate: about 8–10 engineering days. H01, H02, H04, H08 and H12 can start in parallel.
Recommended order for one person: H12 → H01 → H06 → H02 → H03 → H04 → H05 → H07 → H08 → H09
→ H10 → H11 (→ H13).

## Ground rules for every task

- `internal/workflow` and `internal/policy` import nothing new; `internal/secretscan` imports
  only the standard library.
- Provider-specific code stays in `internal/provider/<name>`. No authority is widened.
- Nothing new runs before the fail-fast chain finishes; nothing new is called before approval.
- Artifacts and logs keep full data; only prompts are bounded.
- A secret's value is never printed, logged, persisted or put in a prompt.
- Tests need no network. Integration scenarios 1–22 keep passing (their config sets
  `test.sandbox: none` explicitly once H05 lands, except the sandbox scenario).
- Each task ends green on `go build ./...`, `go vet ./...`, `go test ./...`, and
  `gofmt -l .` empty. Tasks that touch an adapter's flags (H01) also re-run
  `go test -tags security ./test/security/...` on the installed CLI.
- When a task is done: tick it here and in TODO.md, and add an "As built" section to the task
  describing anything built differently and why.
