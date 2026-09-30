# J07 — Integration scenarios 19a–19h

**Size** M (1 d) · **Depends on** J06 · **Blocks** J08

## Goal

The feature is covered black-box through `app.Run`, with a fake Jira on loopback and the
existing fake executables. Nothing touches the network or needs a real token
(`SPEC.md` §16).

## Files

- `test/integration/jira_test.go` (new): the `fakeJira` helper
- `test/integration/scenario19_jira_*_test.go` (new)
- `test/integration/helpers_test.go`: `configOptions.jira` (render a `jira:` block when
  set) and `configOptions.testCommand` reuse

## Steps

1. `fakeJira`, in the shape of `fakeJev` (`test/integration/jev_test.go`):
   `startJira(t, issues map[string]string)` serves canned v2 JSON per key on
   `httptest.NewServer` (loopback `http`, which J01 allows). It records every request
   (path, query, `Authorization`) under a mutex. It can be scripted with `Fail []int`
   statuses for the first requests, and `Moved map[string]string`. It sets
   `JIRA_EMAIL`/`JIRA_API_TOKEN` with `t.Setenv` and checks Basic auth against them.
   `Requests() int` gives the count.
2. `configOptions.jira *jiraOptions{baseURL, auth, fields}`: `writeConfig` renders the
   section only when it is set, so existing scenarios keep an identical config.
3. Scenarios (each its own `t.Run` or file, with `--mode auto` from `runOrchestrator`):
   - **19a success**: `run --jira PMC-1`, Jev `scriptBugfixMedium`. Exit 0. Each prompt in
     `FAKE_ARGS_DIR` contains `PMC-1: <summary>` and the description. The Jev request
     body contains the rendered text. `run.json` `task.source.key == "PMC-1"`.
     `ticket.json` exists. stdout has the `Task (from PMC-1):` block. The fake server saw
     exactly 1 request.
   - **19b notes**: `run --jira PMC-1 "only the FE part"`. The implement prompt ends its
     task section with `Operator notes:\nonly the FE part`. `source.notes == true`.
   - **19c not found**: 404 → exit 2. The runs directory holds no new run. The fake
     providers' invocation log is empty.
   - **19d missing token**: `JIRA_API_TOKEN` unset → exit 2, the message names the
     variable, 0 requests, no provider call.
   - **19e foreign host**: `--jira https://evil.example/browse/PMC-1` → exit 2, 0
     requests.
   - **19f resume**: a Jira run stopped by a failing provider (reuse scenario 7's setup),
     then `resume <id>` after `t.Setenv("JIRA_API_TOKEN", "")`. The resume succeeds, the
     fake server count is still 1, stdout has `Task from PMC-1, fetched`.
   - **19g credential scrub**: `test.command` is `env > "$ENV_OUT"`, with `ENV_OUT` set
     through `t.Setenv` to a temp path. After the run, the file has no `JIRA_API_TOKEN`,
     `JIRA_EMAIL` or `JEV_API_KEY` line, and still has `PATH`. The fake provider script
     also dumps its env when `FAKE_ENV_DIR` is set (small addition to `fakeexec`), and
     the same assertion applies to it.
   - **19h no --jira**: a config with a `jira` section and no Jira variables, and a plain
     `run "task"` → exit 0, 0 requests, `run.json` has no `task.source`.
4. Retry and redaction are unit-tested in J02. Here, add one case: 503 then 200 with
   `max_retries: 1` → exit 0, 2 requests.

## Acceptance

- All 19a–19h (plus the retry case) pass under `go test ./test/integration/...`, with no
  network (the server binds `127.0.0.1` only).
- Scenarios 1–18 pass unchanged. `git diff` of their files is empty apart from
  `helpers_test.go` and `fakeexec`.
- `go test -race ./test/integration/...` is clean (the fake server's recording is
  concurrent).

## As built

_Filled in when the task is done._

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
