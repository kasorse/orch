# T04 — Scrub parent-session variables from children

**Phase** 2 · **Size** S · **Depends on** — (parallel with T03) · **Blocks** T09

## Goal

No child process — provider CLI or test command — inherits the IPC channel of a Claude Code
session that launched the orchestrator (`SPEC.md` §7.1, D7).

## Files

- `internal/execution/process.go` — `Spec.Env`; `cmd.Env = spec.Env` when non-nil
- `internal/execution/env.go` (+ test) — **new**: `ScrubbedEnv() []string`
- `internal/execution/fake.go` — record `Env`
- `internal/provider/{claude,codex,gemini}/client.go`, `internal/testexec/runner.go`

## Steps

1. `Spec.Env []string`, nil = inherit. Field comment: nil must keep meaning "inherit", or git
   calls break in a way that looks like a git bug.
2. `ScrubbedEnv` = `os.Environ()` minus exact names `CLAUDECODE`,
   `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_CODE_SESSION_ID`,
   `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_PID`. Exact names, never prefixes — say why (a prefix
   list cannot be audited by reading it).
3. Every adapter and `testexec.Runner` set `Env: execution.ScrubbedEnv()`. `repository` git
   calls keep nil.

## Acceptance

- With those variables planted via `t.Setenv`, the fake executor sees none of them in
  provider and test specs, and still sees `PATH` and `HOME`.
- `repository` specs still have `Env == nil`.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
