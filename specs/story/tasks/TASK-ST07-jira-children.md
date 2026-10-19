# ST07 — Jira children

**Size** M (2.5 d) · **Depends on** ST06 · **Blocks** ST10

## Goal

`story run` creates the missing child issues once, before the first dispatch (`SPEC.md` §12.2).

## Files

- `internal/jira/create.go`: `(*Client).Create(ctx, Issue) (key string, err)` —
  `POST /rest/api/2/issue` to `base_url` only, no redirects, unexported wire types, the 201
  answer strictly validated (`key` `<PROJECT>-<n>`, the project the one sent), errors
  (`errorMessages`, `errors`) redacted and truncated; retried only on 429/503 with
  `Retry-After`; never after a transport error once the request may have been sent
- `internal/jira/wiki.go`: the task's sections (title, goal, acceptance, notes) rendered as
  wiki markup — headings, lists, code blocks, inline code; text escaped where wiki syntax would
  change its meaning
- `internal/app/story_jira.go`: for each task without `jira` in the story's order: the intent
  saved, `Create`, the key into `story.json` and `story.SetJira` into the task file, the intent
  cleared; an intent without a key on start → the stop of §12.2; `link: parent` or the field;
  components by service name (a key that names no service of the story is an error before any
  request); the project from the parent's key
- MVP.md §39 and §30 notes are ST10's

## Acceptance

- Fake Jira: both link modes in the request body; a 400 with `errors` named in the message, the
  credentials absent from it; a 429 with `Retry-After` retried once; a timeout after the request
  was sent not retried, the intent kept, the next start stopped with the message; a redirect not
  followed; keys written to the task files without touching anything else in them.
- A story without a parent or without `jira.create` sends nothing.

## Gates

`scripts/check.sh`

## As built

2026-10-19. Written without running anything (no shell in the run that wrote it): `scripts/check.sh`
is left to the run's test step, and gofmt was kept by hand. The first `scripts/check.sh` run passed
vet and every test package but failed gofmt on `internal/exitcode/exitcode_test.go` (a missing space
in one table row); that row was fixed by hand and the rerun is the run's test step. MVP.md §39 and §30, CLAUDE.md and the
example config are ST10's and not touched.

- **`jira.(*Client).Create`** (`internal/jira/create.go`): `POST <base_url>/rest/api/2/issue` and
  nothing else, no redirect, the body a `fields` map (so a custom link field's id is a key) with
  `parent: {key}` or `<field>: <parent key>`, labels and components left out when empty. A 201 must
  carry a `key` that is `<PROJECT>-<n>` for the project sent (`domain.ValidTicketKey` plus the
  prefix; `story.ValidJiraKey` would be an import cycle); unknown fields are accepted. A 400 is
  `JiraRejected` with Jira's `errorMessages` and `errors` (`field: message`, in field order) as its
  detail, redacted and then cut. Only a 429 or 503 with a `Retry-After` that fits one attempt's
  timeout is retried, up to `max_retries` times.
- **Possibly sent** (`apperr.JiraError`): gains `Op` (`"create"` words a few headlines for a
  creation: not found, rejected, invalid answer, cancelled) and `NotSent` (the request was never
  handed to the transport). `jira.MayExist(err)` is the one answer the app acts on: false for
  `NotSent` and for the answers that refuse a request (3xx, 400, 401, 403, 404, 429, 503), true for
  everything else - a timeout, a connection error, a read error, 500/502/504, an invalid 201, a
  cancellation mid-request. When in doubt it keeps the intent. A refused connection before any byte
  is written is also treated as possibly sent: telling it apart is not worth a duplicate. `Fetch`
  and `Create` share `do` (one round trip: headers, timeout, the 1 MiB read, 3xx and transport
  errors) and `statusError`; `Fetch`'s behaviour and tests are unchanged. `JiraRejected` is exit
  code 2 (`exitcode`).
- **`jira.Wiki`** (`internal/jira/wiki.go`): the task's Markdown body, line by line - `##` to `h2.`,
  `-`/`*`/`+` and `1.` lists to `*` and `#` with their nesting (`**`, `#*`), fenced blocks to
  `{code}`/`{code:lang}` verbatim (a `{code` inside is broken so it cannot end the block),
  `` `x` `` to `{{x}}`, `**x**` to `*x*`, `>` to `bq.`, `---` to `----`. Every other character wiki
  would read as markup (`{ } [ ] * _ + - ^ ~ | ! ?`) gets a backslash, so a model's text starts no
  macro, link, mention, table or image; a backslash is followed by a zero-width space so it can
  never escape what follows, and a line that would start `h1.`..`h6.`, `bq.` or `#` is
  neutralised. Links `[text](url)` are not converted, only escaped. It ends with
  `Repository: … · Spec: … · Depends on: …`.
- **`story run`** (`internal/app/story_jira.go`, `story_run.go`): the stub is split in two.
  `checkJiraChildren` runs where the stub was, before the first start's `[Y/n]`, and sends nothing:
  an intent without a key (stub's message) stops the story, a key in the task's front matter
  (a key or `none`) answers it and clears the intent, a `jira.create.components` key that names no
  service of the story is a config error, and a story with `jira.create` but no `jira_parent`
  prints a note. `createJiraChildren` runs after `approve()`, so nothing is written to Jira before
  the operator's answer, and before the first dispatch. One task at a time in the story's order,
  for a task that is still pending and has no key: the intent is saved, `Create` is called, the key
  is saved in `story.json` (intent cleared) and then written into the task file (`story.SetJira`,
  `ReplaceFile`, the runner's copy refreshed). The key is recorded first so it is never lost; a
  crash before the file write is repaired at the next start (a pending task whose record has a key
  and whose file has none gets the line). A refused request clears the intent (and drops an entry
  the creation made); a request that may have been sent keeps it, and the error says to look for
  the issue and set `jira:` by hand. A failure halts the story (`failed`, `stopped` on Ctrl-C)
  before any dispatch; the keys already recorded stay. A task that was dispatched is never touched.
- **Credentials**: `story run` now passes `DependencyOptions{Jira: true}` when `jira.create` is set,
  so the Jira section and its variables are checked in the fail-fast chain (SPEC §10.1 point 1)
  even for a story with no parent; the client is built in `story_jira.go`, the second place the
  variables are read.
- **Decision**: the creation moved after `approve()` rather than staying where the stub was, since
  the stub's place is before the confirmation and SPEC §6 says nothing outside the machine is
  written before it. The plan shown at the confirmation therefore has no keys for the tasks still
  to be created.
- **Not covered**: no integration scenario drives the whole `story run` (the refused confirmation
  sending nothing is by construction of `run()`, not asserted); the tests call
  `checkJiraChildren` and `createJiraChildren` on a runner read from disk, a new runner standing
  for the next start.
- Tests: `internal/jira/{create,wiki}_test.go`, `internal/app/story_jira_test.go`,
  `internal/apperr/jira_test.go`, `internal/exitcode/exitcode_test.go`.
