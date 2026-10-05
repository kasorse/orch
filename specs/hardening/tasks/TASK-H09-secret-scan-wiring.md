# H09 — Secret scan in the run

**Size** M (1.5–2 d) · **TODO** S5 · **Depends on** H03, H07, H08 · **Blocks** H10

## Goal

What `implement` or the test command adds to the working tree is scanned; a new finding stops
the run (`mode: stop`) or is recorded and redacted from the review prompt (`mode: warn`)
(`SPEC.md` §9.1, §9.3–§9.5).

## Files

- `internal/config/config.go`, `validator.go`: `security.secret_scan`
- `internal/repository/git.go`: `AddedContent(ctx, exclude) ([]AddedFile, error)` — tracked
  `+` lines with new-file line numbers (parse `git diff HEAD -U0` hunk headers) and untracked
  non-ignored files' full content
- `internal/action/secrets.go` (new) + test: `Dispatcher.ScanSecrets`
- `internal/action/implement.go`, `testaction.go`: call it after the guard check
- `internal/action/review.go` / `ReviewDiff` caller: `Redact` in warn mode
- `internal/app/app.go`: run-start baseline
- `internal/domain/run.go`: `SecretScanBaseline`, `SecretFindings`
- `internal/persistence`: validation
- `internal/logging`: warn lines, header `secret scan: off`

## Steps

1. Config: `security.secret_scan.mode` (`stop` default, `warn`, `off`) and `allow_paths`
   (valid globs; `**` supported the same way `protected_paths` does — reuse that matcher).
2. `repository.AddedContent`: one `git diff HEAD -U0 --no-color -- . :(exclude)<runs>` parse
   (`+++ b/<path>`, `@@ -a,b +c,d @@`, `+` lines; skip `+++`), plus untracked via
   `git ls-files --others --exclude-standard -z` read from disk (Lstat; skip symlinks). The
   deny list is **not** applied here — scanning is local.
3. `Dispatcher.ScanSecrets(ctx, run, stepName) error`:
   scan → drop `allow_paths` → drop fingerprints in `run.SecretScanBaseline` → if none, nil.
   `stop`: return `WorkflowError` with `file:line (rule)` per finding (max 20 listed, then
   `... and N more`) + restore point, using the guard's violation wording style.
   `warn`: append new findings to `run.SecretFindings` (dedupe by fingerprint; keep the
   fingerprint in memory only, not persisted per finding), print one warning line each, return
   nil.
4. Call sites: `executeImplement` after the guard check passes; `executeTest` after its guard
   check (before `applyTest`). Not called when `mode: off`.
5. Run-start baseline in `executeWorkflow`: after the pre-run test if it ran, otherwise right
   after the resume baseline; only for a run without executions and `mode != off`.
   `run.SecretScanBaseline` = sorted unique fingerprints; `SaveRun`.
6. Warn mode redaction: the review diff text goes through `secretscan.Redact` before
   `BuildReviewPrompt`. (Stop mode never reaches review with a new finding; pre-existing
   baseline findings are redacted too — redaction is unconditional in `warn` and `stop`.)
7. Persistence validation per `SPEC.md` §11.

## Acceptance

- Integration (fake implement writes `AKIAZ7Q2...` into `pkg/cfg.go`): `stop` → exit 4, the
  message has `pkg/cfg.go:N (aws-access-key-id)`, no review call, the value appears in no file
  under the run directory (grep the run dir in the test).
- Same with `warn` → run continues, `run.json.secret_findings` has one entry, the review prompt
  (`FAKE_PROMPTS_DIR/review.txt`) contains `[REDACTED:aws-access-key-id]` and not the value.
- A secret already in the working tree at start (`require_clean_worktree: false`) → baseline
  holds it, no finding.
- `allow_paths: ["testdata/**"]` → a key under `testdata/` is ignored.
- A test command that writes a key into a tracked file → caught after the test step.
- `mode: off` → no scan, header says so.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
