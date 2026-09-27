# T07 — Environment allowlist (`execution.Spec.Env`)

**Phase** 2 · **Size** M · **Depends on** T05, T06 · **Blocks** T10, T15

## Goal

Stop handing every credential in the operator's environment to a CLI that reads
attacker-influenced files. Fixes F4, and F11 (the wildcard defect found while resolving Q2).

## Files

- `internal/execution/process.go` — `Spec.Env`, `cmd.Env` wiring
- `internal/execution/fake.go` — record `Env` so tests can assert on it
- `internal/provider/env.go` — **new**: base list, per-provider map, deny list
- `internal/action/context.go`, `internal/classifier/classifier.go` — assemble and pass
- tests

## What to do

1. `Spec.Env []string`; `OSProcessExecutor` sets `cmd.Env = spec.Env` only when non-nil.
   **Nil must keep today's full inheritance**, so `internal/repository`'s git calls and
   `internal/testexec` are untouched unless explicitly opted in. Say so in the field comment
   — a later "consistency" change that made nil mean "empty" would break git in a way that
   looks like a git bug.
2. Lists live in `internal/provider` (only the provider knows which auth variable it needs);
   the *values* are read in `action`/`classifier`. Use `SPEC.md` §8.5 verbatim:
   - Base: `PATH`, `HOME`, `TERM`, `LANG`, `LC_ALL`, `TMPDIR`, `USER`, `XDG_CONFIG_HOME`,
     `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` + lowercase spellings.
   - `claude`: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`,
     `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`,
     `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`, `CLAUDE_CODE_USE_BEDROCK`,
     `CLAUDE_CODE_USE_VERTEX`, `AWS_PROFILE`, `AWS_REGION`, `AWS_DEFAULT_REGION`,
     `CLOUD_ML_REGION`, `GOOGLE_CLOUD_PROJECT`, `ANTHROPIC_VERTEX_PROJECT_ID`.
   - `codex`: `OPENAI_API_KEY`, `OPENAI_BASE_URL`. `gemini`: `GEMINI_API_KEY`,
     `GOOGLE_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT`.
   - Plus `security.env_allowlist`.
3. **Explicit names only — no prefix wildcards** (F11). A `CLAUDE_CODE_*` glob would forward
   `CLAUDE_CODE_MESSAGING_SOCKET` and `CLAUDE_CODE_MESSAGING_TOKEN`, the IPC channel of a
   parent Claude Code session, into the child. Put that sentence in the code as the reason
   the map is a list of names.
4. Deny list, checked **last** so it overrides both the per-provider map and
   `security.env_allowlist`: `SSH_AUTH_SOCK`, `GH_TOKEN`, `GITHUB_TOKEN`, `GITLAB_TOKEN`,
   `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`,
   `CLAUDE_CODE_MESSAGING_SOCKET`, `CLAUDE_CODE_MESSAGING_TOKEN`, `CLAUDE_CODE_SESSION_ID`,
   `CLAUDE_CODE_CHILD_SESSION`, `CLAUDECODE`, `CLAUDE_PID`.
5. Record forwarded **names** (never values) for T12's audit record and T10's authority
   block. MVP.md §23's redaction rule applies: no value ever reaches a log, an error message,
   or `policy.json`.
6. Because the env is now explicit, a missing auth variable produces an auth failure at the
   first call rather than at config time. That is what `orchestrator validate` printing the
   forwarded names (T05 step 6, T10) is for — wire the renderer here.

## Acceptance

- A provider child receives exactly the computed set: assert with the fake executor that
  `Spec.Env` contains `PATH`/`HOME` and does **not** contain `SSH_AUTH_SOCK`,
  `AWS_SECRET_ACCESS_KEY`, or `CLAUDE_CODE_MESSAGING_TOKEN`, with those planted in the
  process environment during the test.
- `security.env_allowlist: [AWS_SECRET_ACCESS_KEY]` is rejected at config validation (T05),
  and were it to reach here it would still be dropped — test both layers.
- `internal/repository` and `internal/testexec` still pass `Env: nil` and behave identically
  (their existing tests must pass untouched).
- No value appears in any log, error, or artifact: grep the captured test output for a planted
  sentinel value.

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
