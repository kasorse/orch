# R05 — Runs directory in every workflow call's deny list + live check

**Size** S (0.5 d) · **Depends on** R01 · **Blocks** R09

## Goal

Defense in depth for `SPEC.md` §9.3. The file tools of a workflow step cannot read, edit or
write the runs directory. This also keeps other runs' prompts, project context and logs out
of a step's reach.

## Files

- `internal/action/context.go` — `DenyRead` gets the runs directory
- `internal/action/dispatcher.go` or `New` — where the runs directory is passed in
- `internal/app/app.go` — pass the runs directory (relative to the top level) to the Dispatcher
- `test/security/claude_test.go` — live cases

## Steps

1. The Dispatcher gets a `RunsDir string` (repository-relative, slash-separated; empty when
   the runs directory is outside the worktree). `call` appends it to
   `projectctx.DenyPatterns(...)` for `provider.Request.DenyRead`. The claude adapter
   already renders each pattern as `Read(**/p)` and `Read(**/p/**)`, and a `Read` deny rule
   blocks Edit and Write too. No change to `buildArgs`.
2. Do not add the runs directory to `projectctx`'s own deny list in this task. Whether
   project context should skip `.orchestrator/` is a separate question: it is usually
   git-ignored, so it is not listed anyway.
3. Live suite: under `read`, reading `.orchestrator/runs/x/run.json` is refused. Under
   `edit`, editing and writing it are refused, and a normal source edit still works.

## Acceptance

- Unit: the recorded argv for every workflow action contains the runs directory rules.
  The translator and decision calls (`none`, no file tools) are unchanged.
- `go test -tags security ./test/security/...` passes on the verified claude version. If a
  case fails, the finding is recorded, and `Supports` is not relaxed.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty ·
live suite run once and its result noted in the commit message
