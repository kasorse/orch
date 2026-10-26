---
id: JR06
repo: orche
depends_on: [JR05]
jira: none
---
# JR06 — run --jira: CLI, taskFromJira, console output, exit codes, resume line

**Size** M

## Goal

`orchestrator run --jira <KEY|URL> ["notes"]` works end to end in the order of `SPEC.md` §10.
`run` without `--jira` and `resume` behave exactly as before, except for one header line on a
resumed Jira run.

## Files

- `internal/app/app.go`: `parseArgs`, `Run`, `runTask`.
- `internal/app/jira.go` (new): `taskFromJira`.
- `internal/logging/jira.go` (new): `FetchingTicket`, `TicketFetched`, `TicketMoved`,
  `TaskBlock`.
- `internal/logging/resume.go`: the source line in `ResumeHeader`.
- `internal/app/args_test.go` (new), `internal/logging/jira_test.go`,
  `internal/logging/testdata/task_block.golden`, `task_block_moved.golden`,
  `resume_jira_source.golden`.

## Steps

1. `parseArgs` returns `cliArgs{configPath string; mode domain.RunMode; verbose bool; jiraRef
   string; rest []string}`. The value flags share one helper for `--flag X` and `--flag=X`, and
   the existing messages do not change. `--jira`'s value is trimmed; an empty or blank one gives
   `--jira requires an issue key or URL`, a second one `--jira given twice`.
2. `Run` refuses `--jira` with any command other than `run` before anything else (`--jira is only
   valid with run`): no config is read, nothing is printed. With `--jira` the positional text is
   optional and becomes the notes; without it `run` is unchanged. The general and the `run` usage
   lines show `run --jira <KEY|URL> ["notes"]`.
3. `runTask(ctx, configPath, text, jiraRef, ...)`: `text` is the typed task, or the notes with
   `--jira`. It passes `DependencyOptions{Jira: jiraRef != ""}` (a closure) to
   `loadAndValidateConfig` and right after calls `jira.ParseRef(jiraRef, cfg.Jira.BaseURL)`,
   still before any request.
4. After `repo.IsDirty` and before `store.NewRun`, under `decideCtx`:
   `ticket, text, err := taskFromJira(...)`. A failure is returned directly, with no `failRun`:
   no run exists (D7). `taskFromJira` is the only place the credential values are read
   (`os.Getenv(cfg.Jira.EmailEnv)`, `os.Getenv(cfg.Jira.TokenEnv)`); it builds the client with
   `UserAgent: "orchestrator/" + Version` and `EmailEnv`/`TokenEnv` for messages, prints
   `FetchingTicket(key, host[:port] of base_url)`, fetches, prints `TicketFetched` (with
   `formatDuration`) and `TicketMoved` when the key moved, renders with `cfg.Jira.MaxBytes`, and
   prints `TaskBlock`.
5. The task is `domain.Task{Description: text, Source: &domain.TaskSource{Kind: "jira", Key,
   RequestedKey, URL, FetchedAt, Notes}, CreatedAt: now}`; `Notes` is true only for notes
   non-empty after trimming. After `store.NewRun` and `Lock`, `store.SaveTicket`; a failure there
   goes through `failRun`. Everything after is unchanged.
6. Console (`SPEC.md` §13): `FetchingTicket` opens with a blank line; `TaskBlock` prints `Task
   (from KEY):` and the text through `logging.NewPrefixWriter(out, "  | ")` in every mode
   whatever `--verbose` (blank lines as `  | `), ends the last line if needed and prints a blank
   line before `Run:`; `TicketMoved` goes to stdout inside the block. Status lines follow the `✓`
   style of `ConfigValid`.
7. `ResumeHeader`: when `run.Task.Source != nil`, right after `Resuming run ...`: `Task from
   PMC-2789, fetched 2026-09-30 10:12 (not re-fetched)`, local time. `resume` changes nothing
   else and passes no Jira dependency (it needs no credentials).

## Documentation

- MVP.md §2 (the Jira REST API is the second permitted HTTP integration: read-only, one GET per
  `run --jira`), §4 (`--jira`), §27 (the ticket block and the resume source line), §30 (replace
  "GitHub PR integration / GitLab integration" with "GitHub/GitLab integration; Jira beyond
  reading one issue for `run --jira` (§39)"), §38.5 (ticket text is operator-equivalent input
  written by anyone who can edit the ticket, and goes to the decision and translation providers).
- CLAUDE.md: the decision-flow diagram gains `[--jira] → fetch the issue, render it as the task
  (internal/jira; failure → exit, no run)` with `app.taskFromJira` named under it; the invariant
  "Jira: read-only, one GET, before the run exists, never on resume" (credentials env-only, sent
  only to `jira.base_url`, redacted, scrubbed); "Out of scope" names the Jira read.
- README.md: prerequisites (Jira credentials only for `run --jira`); the usage synopsis; a
  "Taking the task from a Jira ticket" section (examples, the config block, Cloud setup with an
  API token, `JIRA_EMAIL`, `JIRA_API_TOKEN`, `auth: basic`, Data Center with a PAT and
  `auth: bearer`, custom field ids from `/rest/api/2/field`, resume, failures); "What is sent to
  Jira"; the "not protected" line for ticket text; `ticket.json` and `task.source` among the run
  artifacts; the limitations.

## Acceptance

- `parseArgs` table: `--jira X`, `--jira=X`, `--jira` in any position, a second `--jira`, an
  empty or blank value; `--jira` with `resume`, `validate` or `version` is a usage error that
  prints nothing.
- `run --jira K` with no text gives empty notes; `run --jira K a b` gives notes `a b`.
- Golden files `task_block.golden`, `task_block_moved.golden`, `resume_jira_source.golden`; the
  existing `logging` goldens are unchanged.
- `scripts/check.sh` passes. The end-to-end checks (a `ParseRef` failure makes no request and no
  run directory, a full `run --jira`) are JR07's scenarios.

## Notes

Do not edit anything under `specs/jira/`.

## As built (2026-10-26)

Built by hand on 2026-09-30, before this spec was rewritten as a story, as task J06 of the old
`specs/jira` plan: commit cf79455 on `master` ("Take the task from a Jira ticket with run --jira (J06)"). Marked done by hand on 2026-10-26:
no story run built it.
