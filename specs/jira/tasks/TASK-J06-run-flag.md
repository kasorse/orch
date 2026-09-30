# J06 — `run --jira`: CLI, `taskFromJira`, console output, exit codes, resume line

**Size** M (1 d) · **Depends on** J01–J05 · **Blocks** J07

## Goal

`orchestrator run --jira <KEY|URL> ["notes"]` works end to end in the order of
`SPEC.md` §10. `run` without `--jira` and `resume` behave exactly as before, except for one
header line on a resumed Jira run.

## Files

- `internal/app/app.go`: `parseArgs`, `Run`, `runTask`
- `internal/app/jira.go` (new): `taskFromJira`
- `internal/logging/jira.go` (new): `FetchingTicket`, `TicketFetched`, `TicketMoved`,
  `TaskBlock`
- `internal/logging/resume.go`: the source line in `ResumeHeader`
- `internal/logging/jira_test.go`, `internal/logging/testdata/task_block*.golden`,
  `resume_*.golden` (one new), `internal/app/*_test.go`

## Steps

1. `parseArgs`: it already returns five values, and a sixth makes call sites unreadable,
   so fold them into `type cliArgs struct{configPath string; mode domain.RunMode;
   verbose bool; jiraRef string; rest []string}`. Handle `--jira X` and `--jira=X` like
   `--mode`. A second `--jira` is an error. An empty value is an error.
2. `Run`:
   - `--jira` with any command other than `run` → `ConfigError`: `--jira is only valid
     with run`.
   - `run` with `--jira`: the positional text is optional and becomes `notes`. Update the
     usage strings to show `run --jira <KEY|URL> ["notes"]`.
   - `run` without `--jira`: unchanged (`len(rest) < 2` → usage).
3. `runTask` gains `jiraRef, notes string`. It passes `config.DependencyOptions{Jira:
   jiraRef != ""}` to `loadAndValidateConfig`, and right after, when `jiraRef != ""`, calls
   `key, err := jira.ParseRef(jiraRef, cfg.Jira.BaseURL)`. That is still before any
   request.
4. After `repo.IsDirty` and before `store.NewRun`, when a key is set:
   `ticket, text, err := taskFromJira(decideCtx, cfg, key, notes, reporter)`. A failure is
   returned directly, with no `failRun`, because no run exists (default D7).
   `taskFromJira`:
   - builds `jira.NewCredentials(cfg.Jira.Auth, os.Getenv(cfg.Jira.EmailEnv),
     os.Getenv(cfg.Jira.TokenEnv))`. This is the only place the values are read.
   - builds `jira.NewHTTPClient(jira.Options{…, UserAgent: "orchestrator/" + Version},
     creds)`.
   - calls `reporter.FetchingTicket(key, host)`, then `client.Fetch`, then
     `reporter.TicketFetched(ticket, elapsed)`, plus `reporter.TicketMoved` when
     `RequestedKey != ""`.
   - calls `jira.Render(ticket, notes, cfg.Jira.MaxBytes)`, then
     `reporter.TaskBlock(ticket.Key, text)`.
5. The task becomes `domain.Task{Description: text, Source: &domain.TaskSource{Kind:
   "jira", Key, RequestedKey, URL, FetchedAt, Notes: notes != ""}, CreatedAt: now}`. After
   `store.NewRun` and `Lock`, call `store.SaveTicket(run.ID, ticket)`. A failure there goes
   through `failRun`, because the run now exists. Everything after is unchanged.
6. Console (`SPEC.md` §13): `TaskBlock` writes `Task (from KEY):` and then the text
   through `logging.NewPrefixWriter(out, "  | ")`, always, in every mode and regardless
   of `--verbose`. Status lines follow the `✓` style of `ConfigValid`.
7. `ResumeHeader`: when `run.Task.Source != nil`, add `Task from PMC-2789, fetched
   2026-09-30 10:12 (not re-fetched)` after the run line. Use local time, formatted
   `2006-01-02 15:04`. `resume` itself gets no other change and still passes
   `Jira: false` (it needs no credentials).

## Acceptance

- `parseArgs` table: `--jira X`, `--jira=X`, `--jira` in any position, a second `--jira`,
  an empty value, `--jira` with `resume`/`validate`/`version`.
- `run --jira K` with no text → notes empty. `run --jira K a b` → notes `a b`.
- Golden files: `task_block.golden`, `task_block_moved.golden`,
  `resume_jira_source.golden`.
- An `app` unit test (or J07 scenario) shows that a `ParseRef` failure makes no request
  and creates no run directory.
- Existing `logging` and `app` goldens are unchanged.

## As built

_Filled in when the task is done._

## Gates

`go build ./...` · `go vet ./...` · `go test ./...` · `gofmt -l .` empty
